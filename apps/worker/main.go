package main

import (
	"log/slog"
	"os"

	"github.com/hibiken/asynq"
	"nova.local/core/internal/jobs"
	"nova.local/core/internal/platform/config"
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

	server := asynq.NewServer(
		asynq.RedisClientOpt{Addr: redisAddr},
		asynq.Config{Concurrency: 4, Logger: slogAsynqLogger{logger: logger}},
	)
	logger.Info("NOVA worker is starting", "redis", redisAddr)
	if err := server.Run(jobs.Handler(logger)); err != nil {
		logger.Error("worker stopped", "error", err)
		os.Exit(1)
	}
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
