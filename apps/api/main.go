package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/hibiken/asynq"
	"github.com/jackc/pgx/v5/pgxpool"
	redisclient "github.com/redis/go-redis/v9"
	"nova.local/core/internal/agent"
	"nova.local/core/internal/ai/openaiadapter"
	"nova.local/core/internal/platform/config"
	"nova.local/core/internal/platform/httpserver"
	"nova.local/core/internal/storage"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	cfg, err := config.Load()
	if err != nil {
		logger.Error("invalid configuration", "error", err)
		os.Exit(1)
	}

	ctx := context.Background()
	db, err := pgxpool.New(ctx, cfg.DatabaseURL)
	if err != nil {
		logger.Error("database configuration failed", "error", err)
		os.Exit(1)
	}
	defer db.Close()

	redisAddr, err := cfg.RedisAddress()
	if err != nil {
		logger.Error("redis configuration failed", "error", err)
		os.Exit(1)
	}
	redisClient := redisclient.NewClient(&redisclient.Options{Addr: redisAddr})
	defer redisClient.Close()
	reminderQueue := asynq.NewClient(asynq.RedisClientOpt{Addr: redisAddr})
	defer reminderQueue.Close()

	store := storage.New(db)
	if err := store.EnsureUser(ctx, cfg.NovaUserID); err != nil {
		logger.Warn("single-user bootstrap is waiting for database schema", "error", err)
	}

	var responder *openaiadapter.Client
	if cfg.OpenAIAPIKey != "" {
		responder, err = openaiadapter.New(cfg.OpenAIAPIKey, cfg.OpenAITextModel)
		if err != nil {
			logger.Warn("OpenAI adapter is disabled", "error", err)
		}
	} else {
		logger.Warn("OPENAI_API_KEY is not configured; deterministic routes remain available")
	}

	server := &http.Server{
		Addr: ":" + cfg.APIPort,
		Handler: httpserver.New(cfg, db, redisClient, httpserver.Dependencies{
			Store:     store,
			Responder: responder,
			Models: agent.ModelCatalog{
				Simple: cfg.OpenAITextModel, Medium: cfg.OpenAIMediumModel, Strong: cfg.OpenAIStrongModel,
			},
			UserID:        cfg.NovaUserID,
			Logger:        logger,
			ReminderQueue: reminderQueue,
		}).Handler(),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	serverErr := make(chan error, 1)
	go func() { serverErr <- server.ListenAndServe() }()
	logger.Info("NOVA API is listening", "addr", server.Addr)

	shutdownCtx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	select {
	case err := <-serverErr:
		if !errors.Is(err, http.ErrServerClosed) {
			logger.Error("server stopped unexpectedly", "error", err)
			os.Exit(1)
		}
	case <-shutdownCtx.Done():
		gracefulCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := server.Shutdown(gracefulCtx); err != nil {
			logger.Error("graceful shutdown failed", "error", err)
			os.Exit(1)
		}
	}
}
