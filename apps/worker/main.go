package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"time"

	"github.com/hibiken/asynq"
	"github.com/jackc/pgx/v5/pgxpool"
	"nova.local/core/internal/integrations/telegram"
	"nova.local/core/internal/jobs"
	"nova.local/core/internal/platform/config"
	"nova.local/core/internal/storage"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	cfg, err := config.Load()
	if err != nil {
		logger.Error("invalid configuration", "error", err)
		os.Exit(1)
	}
	redisAddr, err := cfg.RedisAddress()
	if err != nil {
		logger.Error("redis configuration failed", "error", err)
		os.Exit(1)
	}
	ctx := context.Background()
	db, err := pgxpool.New(ctx, cfg.DatabaseURL)
	if err != nil {
		logger.Error("database configuration failed", "error", err)
		os.Exit(1)
	}
	defer db.Close()
	if err := db.Ping(ctx); err != nil {
		logger.Error("database is unavailable", "error", err)
		os.Exit(1)
	}
	store := storage.New(db)
	heartbeatStop := jobs.StartHeartbeat(context.Background(), logger, store, jobs.HeartbeatConfig{
		ServiceName: jobs.ServiceWorker,
		InstanceID:  WorkerInstanceID(),
		Metadata: map[string]string{
			"role":      "asynq",
			"imageTag":  cfg.GitDeployedCommitSHA,
			"timezone":  cfg.Timezone,
			"redisAddr": redisAddr,
		},
		Interval: 15 * time.Second,
	})
	defer heartbeatStop()

	var telegramClient *telegram.Client
	telegramChatID, hasTelegramChatID := cfg.TelegramNotificationChatID()
	if cfg.TelegramEnabled && cfg.TelegramBotToken != "" {
		telegramClient, err = telegram.NewClient(cfg.TelegramBotToken)
		if err != nil {
			logger.Warn("Telegram reminder delivery is disabled", "error", err)
		}
	} else if cfg.TelegramEnabled {
		logger.Warn("TELEGRAM_ENABLED is true but TELEGRAM_BOT_TOKEN is not configured")
	}
	if cfg.TelegramEnabled && !hasTelegramChatID {
		logger.Warn("Telegram reminder delivery has no configured chat id")
	}

	server := asynq.NewServer(
		asynq.RedisClientOpt{Addr: redisAddr},
		asynq.Config{Concurrency: 4, Logger: slogAsynqLogger{logger: logger}},
	)
	logger.Info("NOVA worker is starting", "redis", redisAddr)
	if err := server.Run(jobs.Handler(logger, store, jobs.HandlerConfig{
		Telegram:       telegramClient,
		TelegramChatID: telegramChatID,
	})); err != nil {
		logger.Error("worker stopped", "error", err)
		os.Exit(1)
	}
}

func WorkerInstanceID() string {
	hostname, err := os.Hostname()
	if err != nil || hostname == "" {
		hostname = "unknown-host"
	}
	return fmt.Sprintf("%s:%s", hostname, strconv.Itoa(os.Getpid()))
}

type slogAsynqLogger struct{ logger *slog.Logger }

func (l slogAsynqLogger) Debug(args ...any) { l.logger.Debug("asynq", "args", args) }
func (l slogAsynqLogger) Info(args ...any)  { l.logger.Info("asynq", "args", args) }
func (l slogAsynqLogger) Warn(args ...any)  { l.logger.Warn("asynq", "args", args) }
func (l slogAsynqLogger) Error(args ...any) { l.logger.Error("asynq", "args", args) }
func (l slogAsynqLogger) Fatal(args ...any) {
	l.logger.Error("asynq fatal", "args", args)
	os.Exit(1)
}
