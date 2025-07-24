package players

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/disgoorg/snowflake/v2"
)

type PlayerConfigurationProvider struct {
	cancelCtx        context.CancelFunc
	mu               sync.RWMutex
	usernameToUserId map[string]snowflake.ID
}

func NewConfigurationProvider(logger *slog.Logger, fileName string, pollInterval time.Duration) *PlayerConfigurationProvider {
	ctx, cancelCtx := context.WithCancel(context.Background())

	pcp := &PlayerConfigurationProvider{
		cancelCtx:        cancelCtx,
		usernameToUserId: map[string]snowflake.ID{},
	}
	go pcp.reloadWorker(ctx, logger.With("fileName", fileName), fileName, pollInterval)

	return pcp
}

func (pcp *PlayerConfigurationProvider) GetUserId(username string) (snowflake.ID, bool) {
	pcp.mu.RLock()
	defer pcp.mu.RUnlock()

	userId, found := pcp.usernameToUserId[username]
	return userId, found
}

func (pcp *PlayerConfigurationProvider) StopReloading() {
	pcp.cancelCtx()
}

func (pcp *PlayerConfigurationProvider) reloadWorker(ctx context.Context, logger *slog.Logger, fileName string, pollInterval time.Duration) {
	logger.Debug("Reload worker started")
	defer logger.Debug("Reload worker stopped")

	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	for {
		if newUsernameToUserId, err := readPlayerConfigurationFile(fileName); err != nil {
			logger.Error("Failed to read player configuration", slog.Any("err", err))
		} else {
			pcp.mu.Lock()
			pcp.usernameToUserId = newUsernameToUserId
			pcp.mu.Unlock()

			logger.Debug("Reloaded player configuration", slog.Int("newLength", len(newUsernameToUserId)))
		}

		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
