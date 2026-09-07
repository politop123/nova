package jobs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/hibiken/asynq"
	"nova.local/core/internal/storage"
)

const TypeHealth = "nova:health"
const TypeReminderDelivery = "nova:reminder:deliver"

func NewHealthTask() (*asynq.Task, error) {
	return asynq.NewTask(TypeHealth, json.RawMessage(`{"source":"startup"}`)), nil
}

type ReminderDeliveryPayload struct {
	UserID     string `json:"userId"`
	ReminderID string `json:"reminderId"`
	TriggerAt  string `json:"triggerAt"`
}

type TelegramSender interface {
	SendMessage(ctx context.Context, chatID int64, text string) error
}

type HandlerConfig struct {
	Telegram       TelegramSender
	TelegramChatID int64
}

func ReminderTaskID(reminder storage.Reminder) string {
	return fmt.Sprintf("nova-reminder-%s-%d", reminder.ID, reminder.TriggerAt.Unix())
}

func NewReminderDeliveryTask(reminder storage.Reminder) (*asynq.Task, error) {
	payload, err := json.Marshal(ReminderDeliveryPayload{
		UserID:     reminder.UserID,
		ReminderID: reminder.ID,
		TriggerAt:  reminder.TriggerAt.UTC().Format(time.RFC3339),
	})
	if err != nil {
		return nil, fmt.Errorf("encode reminder delivery task: %w", err)
	}
	return asynq.NewTask(TypeReminderDelivery, payload), nil
}

func Handler(logger *slog.Logger, store *storage.Store, configs ...HandlerConfig) *asynq.ServeMux {
	var cfg HandlerConfig
	if len(configs) > 0 {
		cfg = configs[0]
	}
	mux := asynq.NewServeMux()
	mux.HandleFunc(TypeHealth, func(ctx context.Context, task *asynq.Task) error {
		logger.InfoContext(ctx, "processed worker health task", "payload", string(task.Payload()))
		return nil
	})
	mux.HandleFunc(TypeReminderDelivery, func(ctx context.Context, task *asynq.Task) error {
		if store == nil {
			return errors.New("storage is not configured")
		}
		var payload ReminderDeliveryPayload
		if err := json.Unmarshal(task.Payload(), &payload); err != nil {
			return fmt.Errorf("decode reminder delivery task: %w", err)
		}
		reminder, err := store.GetReminder(ctx, payload.UserID, payload.ReminderID)
		if err != nil {
			return err
		}
		if reminder.Status != "scheduled" || reminder.TriggerAt.After(time.Now().UTC().Add(30*time.Second)) {
			logger.InfoContext(ctx, "skipped reminder delivery",
				"reminder_id", reminder.ID,
				"status", reminder.Status,
			)
			return nil
		}
		if reminder.DeliveryMethod == "telegram" {
			if cfg.Telegram == nil || cfg.TelegramChatID == 0 {
				return errors.New("telegram reminder delivery is not configured")
			}
			if err := cfg.Telegram.SendMessage(ctx, cfg.TelegramChatID, formatTelegramReminder(reminder)); err != nil {
				return fmt.Errorf("send telegram reminder: %w", err)
			}
		}
		reminder, delivered, err := store.DeliverReminder(ctx, payload.UserID, payload.ReminderID)
		if err != nil {
			return err
		}
		logger.InfoContext(ctx, "processed reminder delivery",
			"reminder_id", reminder.ID,
			"status", reminder.Status,
			"delivered", delivered,
		)
		return nil
	})
	return mux
}

func formatTelegramReminder(reminder storage.Reminder) string {
	return "🔔 Нагадування: " + reminder.Title
}
