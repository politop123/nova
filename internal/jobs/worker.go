package jobs

import (
	"context"
	"encoding/json"
	"log/slog"

	"github.com/hibiken/asynq"
)

const TypeHealth = "nova:health"

func NewHealthTask() (*asynq.Task, error) {
	return asynq.NewTask(TypeHealth, json.RawMessage(`{"source":"startup"}`)), nil
}

func Handler(logger *slog.Logger) *asynq.ServeMux {
	mux := asynq.NewServeMux()
	mux.HandleFunc(TypeHealth, func(ctx context.Context, task *asynq.Task) error {
		logger.InfoContext(ctx, "processed worker health task", "payload", string(task.Payload()))
		return nil
	})
	return mux
}
