package proxy

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"sync"
	"time"

	"github.com/szczursonn/mc-discord-auth-proxy/internal/auth"
)

type proxy struct {
	ctx         context.Context
	logger      *slog.Logger
	wg          sync.WaitGroup
	serverAddr  *net.TCPAddr
	authHandler *auth.Handler
}

func Run(ctx context.Context, logger *slog.Logger, selfAddr *net.TCPAddr, serverAddr *net.TCPAddr, authHandler *auth.Handler) {
	p := &proxy{
		ctx:         ctx,
		logger:      logger,
		serverAddr:  serverAddr,
		authHandler: authHandler,
	}
	defer p.wg.Wait()

	listener, err := net.ListenTCP("tcp", selfAddr)
	if err != nil {
		logger.Error("Failed to start TCP listener", slog.Any("err", err))
		return
	}

	shutdownChan := make(chan struct{}, 1)
	go func() {
		select {
		case <-ctx.Done():
		case <-shutdownChan:
		}
		listener.Close()
	}()
	defer func() {
		shutdownChan <- struct{}{}
	}()

	logger.Info("Ready to accept connections", slog.String("selfAddr", selfAddr.String()), slog.String("serverAddr", serverAddr.String()))

	for {
		conn, err := listener.AcceptTCP()
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				logger.Info("TCP listener stopped")
			} else {
				logger.Error("Failed to accept TCP connection", slog.Any("err", err))
			}
			return
		}

		p.wg.Add(1)
		go p.connectionWorker(conn)
	}
}

func (p *proxy) connectionWorker(clientConn *net.TCPConn) {
	logger := p.logger.With(slog.String("clientAddr", clientConn.RemoteAddr().String()))
	logger.Info("Handling new incoming connection")

	shutdownChan := make(chan struct{}, 1)
	go func() {
		select {
		case <-p.ctx.Done():
		case <-shutdownChan:
		}

		clientConn.Close()
		logger.Info("Connection closed")
		p.wg.Done()
	}()
	defer func() {
		shutdownChan <- struct{}{}
	}()

	replayBuffer := &bytes.Buffer{}
	bufferedClientConnReplayTee := bufio.NewReader(io.TeeReader(clientConn, replayBuffer))
	const initialReadTimeout = 20 * time.Second
	clientConn.SetReadDeadline(time.Now().Add(initialReadTimeout))

	firstTwoBytes, err := bufferedClientConnReplayTee.Peek(2)
	if err != nil {
		logger.Error("Failed to check first 2 bytes for legacy ping", slog.Any("err", err))
		return
	}

	if firstTwoBytes[0] != 0xFE || firstTwoBytes[1] != 0x01 {
		handshakePacketLength, err := readVarInt(bufferedClientConnReplayTee)
		if err != nil {
			logger.Error("Failed to read handshake packet length", slog.Any("err", err))
			return
		}
		handshakePacketDataReader := io.LimitReader(bufferedClientConnReplayTee, int64(handshakePacketLength))

		if _, err := readVarInt(handshakePacketDataReader); err != nil {
			logger.Error("Failed to read handshake packet id", slog.Any("err", err))
			return
		}

		if _, err := readVarInt(handshakePacketDataReader); err != nil {
			logger.Error("Failed to read handshake packet protocol version", slog.Any("err", err))
			return
		}

		if _, err := readString(handshakePacketDataReader); err != nil {
			logger.Error("Failed to read handshake packet server address", slog.Any("err", err))
			return
		}

		if _, err := readUnsignedShort(handshakePacketDataReader); err != nil {
			logger.Error("Failed to read handshake packet server port", slog.Any("err", err))
			return
		}

		intent, err := readVarInt(handshakePacketDataReader)
		if err != nil {
			logger.Error("Failed to read handshake packet intent", slog.Any("err", err))
			return
		}

		if restOfDataLength, err := io.Copy(io.Discard, handshakePacketDataReader); err != nil {
			logger.Error("Failed to read handshake packet rest of data", slog.Any("err", err))
			return
		} else if restOfDataLength > 0 {
			logger.Warn("Extra data encountered in handshake packet", slog.Int64("length", restOfDataLength))
		}

		if intent == 2 {
			loginStartPacketLength, err := readVarInt(bufferedClientConnReplayTee)
			if err != nil {
				logger.Error("Failed to read login start packet length", slog.Any("err", err))
				return
			}
			loginStartPacketDataReader := io.LimitReader(bufferedClientConnReplayTee, int64(loginStartPacketLength))

			if _, err := readVarInt(loginStartPacketDataReader); err != nil {
				logger.Error("Failed to read login start packet id", slog.Any("err", err))
				return
			}

			username, err := readString(loginStartPacketDataReader)
			if err != nil {
				logger.Error("Failed to read login start packet username", slog.Any("err", err))
				return
			}
			logger = logger.With(slog.String("username", username))

			if _, err := io.Copy(io.Discard, loginStartPacketDataReader); err != nil {
				logger.Error("Failed to read login start rest of data", slog.Any("err", err))
				return
			}

			const authTimeout = 30 * time.Second
			ctx, cancelCtx := context.WithTimeout(p.ctx, authTimeout)
			deadConnectionCheckerEndChan := make(chan struct{})

			go func() {
				defer func() {
					clientConn.SetReadDeadline(time.Time{})
					deadConnectionCheckerEndChan <- struct{}{}
				}()

				for {
					const pollInterval = 500 * time.Millisecond
					clientConn.SetReadDeadline(time.Now().Add(pollInterval))

					n, err := bufferedClientConnReplayTee.WriteTo(io.Discard)
					if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
						logger.Debug("dead connection checker: alive!")
					} else if err != nil || n == 0 {
						// 0-byte read means EOF - bufio returns an error on too many 0-byte reads from the underlying reader
						logger.Debug("dead connection checker: dead!", slog.Any("err", err))
						cancelCtx()
						return
					}

					select {
					case <-ctx.Done():
						return
					default:
					}
				}
			}()

			logger.Info("Requesting login...")
			err = p.authHandler.RequestLogin(ctx, username, clientConn.RemoteAddr().(*net.TCPAddr).IP)
			cancelCtx()
			<-deadConnectionCheckerEndChan

			if err != nil {
				if errors.Is(err, auth.ErrLoginRequestRejected) {
					logger.Info("Login request rejected")
				} else if errors.Is(err, auth.ErrUserNotWhitelisted) {
					logger.Info("User is not whitelisted")
				} else if errors.Is(err, context.DeadlineExceeded) {
					logger.Info("Login timed out")
				} else if !errors.Is(err, context.Canceled) {
					logger.Error("Failed to request login", slog.Any("err", err))
				}
				return
			}

			logger.Info("Login successful!")
		}
	}

	clientConn.SetReadDeadline(time.Time{})

	serverConn, err := net.DialTCP("tcp", nil, p.serverAddr)
	if err != nil {
		logger.Error("Failed to open server connection", slog.Any("err", err))
		return
	}
	defer serverConn.Close()

	if _, err := io.Copy(serverConn, replayBuffer); err != nil {
		logger.Error("Failed to replay initial data", slog.Any("err", err))
		return
	}

	errChan := make(chan error, 2)
	go pipeConn(clientConn, serverConn, errChan)
	go pipeConn(serverConn, clientConn, errChan)

	if err := <-errChan; err != nil {
		logger.Error("Proxy pipe terminated with error", slog.Any("err", err))
	}
}

func pipeConn(src *net.TCPConn, dst *net.TCPConn, errChan chan error) {
	_, err := src.WriteTo(dst)
	errChan <- err
}
