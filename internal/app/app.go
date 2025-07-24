package app

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"time"

	"github.com/lmittmann/tint"
	"github.com/szczursonn/mc-discord-auth-proxy/internal/auth"
	"github.com/szczursonn/mc-discord-auth-proxy/internal/config"
	"github.com/szczursonn/mc-discord-auth-proxy/internal/geoip"
	"github.com/szczursonn/mc-discord-auth-proxy/internal/players"
	"github.com/szczursonn/mc-discord-auth-proxy/internal/proxy"
)

const configFilePath = "./proxy-config.toml"
const playersFilePath = "./proxy-players.csv"
const playersFilePollInterval = 30 * time.Second
const loggerKeyModule = "module"

func Run() {
	cfg, err := config.LoadConfig(configFilePath)

	logger := initLogger(err == nil && cfg.Debug)

	if err != nil {
		logger.Error("Failed to load config", slog.Any("err", err))
		return
	}
	logger.Debug("Config loaded")

	playersConfigProvider := players.NewConfigurationProvider(logger.With(loggerKeyModule, "players"), playersFilePath, playersFilePollInterval)
	defer playersConfigProvider.StopReloading()

	ctx, cancelCtx := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancelCtx()

	go func() {
		<-ctx.Done()
		logger.Info("Shutting down...")
		cancelCtx()
	}()

	authHandler, err := auth.NewHandler(ctx, logger.With(loggerKeyModule, "auth"), cfg.DiscordToken, playersConfigProvider, geoip.NewClient(&http.Client{
		Timeout: cfg.GeoIPTimeout,
	}), cfg.Whitelist)
	if err != nil {
		logger.Error("Failed to start auth handler", slog.Any("err", err))
		return
	}
	defer authHandler.Shutdown()

	proxy.Run(ctx, logger.With(loggerKeyModule, "proxy"), cfg.SelfAddr, cfg.ServerAddr, authHandler)
}

func initLogger(isDebug bool) *slog.Logger {
	var handler slog.Handler
	if isDebug {
		handler = tint.NewHandler(os.Stdout, &tint.Options{
			Level: slog.LevelDebug,
		})
	} else {
		handler = slog.NewJSONHandler(os.Stdout, nil)
	}

	logger := slog.New(handler)
	slog.SetDefault(logger)

	return logger
}
