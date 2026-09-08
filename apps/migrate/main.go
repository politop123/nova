package main

import (
	"context"
	"flag"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"nova.local/core/internal/platform/config"
)

func main() {
	schemaPath := flag.String("schema", "infrastructure/postgres/init.sql", "path to the initial schema")
	migrationsPath := flag.String("migrations", "", "optional directory with idempotent SQL migration files")
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

	if err := applyMigrations(ctx, db, *migrationsPath, logger); err != nil {
		logger.Error("schema migrations failed", "error", err)
		os.Exit(1)
	}
}

func applyMigrations(ctx context.Context, db *pgxpool.Pool, migrationsPath string, logger *slog.Logger) error {
	migrationsPath = strings.TrimSpace(migrationsPath)
	if migrationsPath == "" {
		return nil
	}
	entries, err := os.ReadDir(migrationsPath)
	if os.IsNotExist(err) {
		logger.Info("migration directory does not exist; skipping", "path", migrationsPath)
		return nil
	}
	if err != nil {
		return err
	}
	if _, err := db.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS schema_migrations (
			version TEXT PRIMARY KEY,
			applied_at TIMESTAMPTZ NOT NULL DEFAULT now()
		)
	`); err != nil {
		return err
	}
	names := migrationFileNames(entries)
	for _, name := range names {
		applied := false
		if err := db.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM schema_migrations WHERE version = $1)`, name).Scan(&applied); err != nil {
			return err
		}
		if applied {
			logger.Info("migration already applied", "migration", name)
			continue
		}
		migrationSQL, err := os.ReadFile(filepath.Join(migrationsPath, name))
		if err != nil {
			return err
		}
		tx, err := db.Begin(ctx)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, string(migrationSQL)); err != nil {
			_ = tx.Rollback(ctx)
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO schema_migrations (version) VALUES ($1)`, name); err != nil {
			_ = tx.Rollback(ctx)
			return err
		}
		if err := tx.Commit(ctx); err != nil {
			return err
		}
		logger.Info("migration applied", "migration", name)
	}
	return nil
}

func migrationFileNames(entries []os.DirEntry) []string {
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".sql") {
			continue
		}
		names = append(names, entry.Name())
	}
	sort.Strings(names)
	return names
}
