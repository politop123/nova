package jobs

import (
	"context"
	"log/slog"
	"time"

	"nova.local/core/internal/storage"
)

const ServiceWorker = "worker"

type HeartbeatStore interface {
	UpsertServiceHeartbeat(ctx context.Context, heartbeat storage.ServiceHeartbeat) error
}

type HeartbeatConfig struct {
	ServiceName  string
	InstanceID   string
	Status       string
	Metadata     map[string]string
	StartedAt    time.Time
	Interval     time.Duration
	WriteTimeout time.Duration
}

func StartHeartbeat(parent context.Context, logger *slog.Logger, store HeartbeatStore, cfg HeartbeatConfig) func() {
	if logger == nil {
		logger = slog.Default()
	}
	if store == nil {
		return func() {}
	}
	if cfg.ServiceName == "" {
		cfg.ServiceName = ServiceWorker
	}
	if cfg.Status == "" {
		cfg.Status = "ok"
	}
	if cfg.StartedAt.IsZero() {
		cfg.StartedAt = time.Now().UTC()
	}
	if cfg.Interval <= 0 {
		cfg.Interval = 15 * time.Second
	}
	if cfg.WriteTimeout <= 0 {
		cfg.WriteTimeout = 5 * time.Second
	}

	ctx, cancel := context.WithCancel(parent)
	done := make(chan struct{})
	go func() {
		defer close(done)
		writeHeartbeat(ctx, logger, store, cfg)
		ticker := time.NewTicker(cfg.Interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				writeHeartbeat(ctx, logger, store, cfg)
			}
		}
	}()

	return func() {
		cancel()
		<-done
	}
}

func writeHeartbeat(ctx context.Context, logger *slog.Logger, store HeartbeatStore, cfg HeartbeatConfig) {
	writeCtx, cancel := context.WithTimeout(ctx, cfg.WriteTimeout)
	defer cancel()
	if err := store.UpsertServiceHeartbeat(writeCtx, storage.ServiceHeartbeat{
		ServiceName: cfg.ServiceName,
		InstanceID:  cfg.InstanceID,
		Status:      cfg.Status,
		Metadata:    cfg.Metadata,
		StartedAt:   cfg.StartedAt,
	}); err != nil {
		logger.WarnContext(ctx, "worker heartbeat failed", "service", cfg.ServiceName, "error", err)
	}
}
