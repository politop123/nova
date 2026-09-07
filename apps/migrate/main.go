package main

import (
	"context"
	"flag"
	"log/slog"
	"os"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"nova.local/core/internal/platform/config"
)

func main() {
	schemaPath := flag.String("schema", "infrastructure/postgres/init.sql", "path to the initial schema")
	flag.Parse()

	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	cfg, err := config.Load()
	if err != nil {
		logger.Error("invalid configuration", "error", err)
		os.Exit(1)
	}
	schema, err := os.ReadFile(*schemaPath)
	if err != nil {
		logger.Error("read schema failed", "path", *schemaPath, "error", err)
		os.Exit(1)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
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
	if _, err := db.Exec(ctx, string(schema)); err != nil {
		logger.Error("schema migration failed", "error", err)
		os.Exit(1)
	}
	logger.Info("NOVA initial schema applied")
}
