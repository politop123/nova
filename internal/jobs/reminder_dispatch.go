package jobs

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/hibiken/asynq"
	"nova.local/core/internal/storage"
)

const MaxReminderLateness = 24 * time.Hour

type ReminderDispatchStore interface {
	ClaimReminderDispatches(context.Context, int) ([]storage.ReminderDispatch, error)
	FinishReminderDispatch(context.Context, storage.ReminderDispatch, string, string, time.Time) (bool, error)
	ReminderDeliveryAttempted(context.Context, storage.Reminder) (bool, error)
}

type ReminderDispatchQueue interface {
	State(context.Context, storage.Reminder) (asynq.TaskState, bool, error)
	Enqueue(context.Context, storage.Reminder) error
}

type AsynqReminderQueue struct {
	Client    *asynq.Client
	Inspector *asynq.Inspector
}

func (q AsynqReminderQueue) State(ctx context.Context, r storage.Reminder) (asynq.TaskState, bool, error) {
	if err := ctx.Err(); err != nil {
		return 0, false, err
	}
	info, err := q.Inspector.GetTaskInfo("default", ReminderTaskID(r))
	if errors.Is(err, asynq.ErrTaskNotFound) || errors.Is(err, asynq.ErrQueueNotFound) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, err
	}
	return info.State, true, nil
}

func (q AsynqReminderQueue) Enqueue(ctx context.Context, r storage.Reminder) error {
	return EnqueueReminder(ctx, q.Client, r)
}

func EnqueueReminder(ctx context.Context, client *asynq.Client, r storage.Reminder) error {
	if client == nil {
		return errors.New("reminder queue is not configured")
	}
	task, err := NewReminderDeliveryTask(r)
	if err != nil {
		return err
	}
	_, err = client.EnqueueContext(ctx, task, asynq.ProcessAt(r.TriggerAt), asynq.TaskID(ReminderTaskID(r)), asynq.MaxRetry(5))
	if errors.Is(err, asynq.ErrDuplicateTask) || errors.Is(err, asynq.ErrTaskIDConflict) {
		return nil
	}
	return err
}

type DispatchResult struct{ Checked, Enqueued, Failed int }

func DispatchReminders(ctx context.Context, store ReminderDispatchStore, queue ReminderDispatchQueue, now time.Time) (DispatchResult, error) {
	var result DispatchResult
	items, err := store.ClaimReminderDispatches(ctx, 25)
	if err != nil {
		return result, err
	}
	for _, item := range items {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		status, detail, enqueued := reconcileReminder(ctx, store, queue, item.Reminder, now)
		next := now.Add(time.Minute)
		if status == "pending" {
			next = now.Add(dispatchBackoff(item.Failures))
		}
		applied, err := store.FinishReminderDispatch(ctx, item, status, detail, next)
		if err != nil {
			return result, err
		}
		if !applied {
			continue
		}
		result.Checked++
		if enqueued {
			result.Enqueued++
		}
		if status == "failed" {
			result.Failed++
		}
	}
	return result, nil
}

func reconcileReminder(ctx context.Context, store ReminderDispatchStore, queue ReminderDispatchQueue, r storage.Reminder, now time.Time) (status, detail string, enqueued bool) {
	if r.TriggerAt.Before(now.Add(-MaxReminderLateness)) {
		return "failed", "Час нагадування минув понад 24 години тому. Автоматичну доставку зупинено; перенеси його на новий час.", false
	}
	state, exists, err := queue.State(ctx, r)
	if err != nil {
		return "pending", "Черга тимчасово недоступна; постановку буде повторено автоматично.", false
	}
	if exists {
		if state == asynq.TaskStateArchived {
			return "failed", "Черга вичерпала спроби доставки. Перевір канал і перенеси нагадування на новий час.", false
		}
		if state == asynq.TaskStateCompleted {
			return "failed", "Завдання завершилось, але доставку не підтверджено в базі. Потрібна перевірка історії.", false
		}
		return "queued", "", false
	}
	attempted, err := store.ReminderDeliveryAttempted(ctx, r)
	if err != nil {
		return "pending", "Не вдалося перевірити попередні спроби доставки.", false
	}
	if attempted {
		return "failed", "Завдання зникло з черги після початку доставки. Щоб не повторити можливе повідомлення, перевір історію перед новим нагадуванням.", false
	}
	if err := queue.Enqueue(ctx, r); err != nil {
		return "pending", "Не вдалося поставити нагадування в чергу; спробу буде повторено автоматично.", false
	}
	return "queued", "", true
}

func dispatchBackoff(failures int) time.Duration {
	failures = max(0, min(failures, 6))
	return min(5*time.Second*time.Duration(1<<failures), 5*time.Minute)
}

func StartReminderDispatch(parent context.Context, logger *slog.Logger, store ReminderDispatchStore, queue ReminderDispatchQueue) func() {
	ctx, cancel := context.WithCancel(parent)
	done := make(chan struct{})
	go func() {
		defer close(done)
		run := func() {
			batchCtx, batchCancel := context.WithTimeout(ctx, 20*time.Second)
			defer batchCancel()
			result, err := DispatchReminders(batchCtx, store, queue, time.Now().UTC())
			if err != nil && ctx.Err() == nil {
				logger.Warn("reminder recovery check failed", "error", err)
			}
			if result.Enqueued > 0 || result.Failed > 0 {
				logger.Info("reminder recovery", "enqueued", result.Enqueued, "needs_attention", result.Failed)
			}
		}
		run()
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				run()
			}
		}
	}()
	return func() { cancel(); <-done }
}
