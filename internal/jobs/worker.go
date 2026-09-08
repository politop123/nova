package jobs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
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

type ReminderDeliveryStore interface {
	GetReminder(ctx context.Context, userID, reminderID string) (storage.Reminder, error)
	DeliverReminder(ctx context.Context, userID, reminderID string) (storage.Reminder, bool, error)
	RecordReminderDeliveryEvent(ctx context.Context, event storage.ReminderDeliveryEvent) (storage.ReminderDeliveryEvent, error)
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

func Handler(logger *slog.Logger, store ReminderDeliveryStore, configs ...HandlerConfig) *asynq.ServeMux {
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
		attempt := reminderDeliveryAttempt(ctx)
		reminder, err := store.GetReminder(ctx, payload.UserID, payload.ReminderID)
		if err != nil {
			return err
		}
		provider := reminderDeliveryProvider(reminder)
		if err := recordReminderDeliveryEvent(ctx, store, payload, reminder, storage.ReminderDeliveryAttempted, attempt, provider, "Worker почав спробу доставки.", nil); err != nil {
			return err
		}
		if reminder.Status != "scheduled" || reminder.TriggerAt.After(time.Now().UTC().Add(30*time.Second)) {
			detail := skippedReminderDeliveryDetail(reminder)
			if err := recordReminderDeliveryEvent(ctx, store, payload, reminder, storage.ReminderDeliverySkipped, attempt, provider, detail, nil); err != nil {
				return err
			}
			logger.InfoContext(ctx, "skipped reminder delivery",
				"reminder_id", reminder.ID,
				"status", reminder.Status,
				"attempt", attempt,
			)
			return nil
		}
		switch reminder.DeliveryMethod {
		case "telegram":
			if cfg.Telegram == nil || cfg.TelegramChatID == 0 {
				deliveryErr := errors.New("telegram reminder delivery is not configured")
				if err := recordReminderDeliveryEvent(ctx, store, payload, reminder, storage.ReminderDeliveryFailed, attempt, provider, "Telegram доставка не налаштована.", deliveryErr); err != nil {
					return errors.Join(deliveryErr, err)
				}
				return deliveryErr
			}
			if err := cfg.Telegram.SendMessage(ctx, cfg.TelegramChatID, formatTelegramReminder(reminder)); err != nil {
				deliveryErr := fmt.Errorf("send telegram reminder: %w", err)
				if eventErr := recordReminderDeliveryEvent(ctx, store, payload, reminder, storage.ReminderDeliveryFailed, attempt, provider, "Telegram не прийняв повідомлення.", deliveryErr); eventErr != nil {
					return errors.Join(deliveryErr, eventErr)
				}
				return deliveryErr
			}
		case "web":
			// The web dashboard reads reminder state directly; marking the reminder delivered is enough.
		default:
			deliveryErr := fmt.Errorf("unsupported reminder delivery method %q", reminder.DeliveryMethod)
			if eventErr := recordReminderDeliveryEvent(ctx, store, payload, reminder, storage.ReminderDeliveryFailed, attempt, provider, "Канал доставки не підтримується.", deliveryErr); eventErr != nil {
				return errors.Join(deliveryErr, eventErr)
			}
			return deliveryErr
		}
		updatedReminder, delivered, err := store.DeliverReminder(ctx, payload.UserID, payload.ReminderID)
		if err != nil {
			deliveryErr := fmt.Errorf("mark reminder delivered: %w", err)
			if eventErr := recordReminderDeliveryEvent(ctx, store, payload, reminder, storage.ReminderDeliveryFailed, attempt, provider, "Повідомлення могло бути відправлене, але статус нагадування не збережено.", deliveryErr); eventErr != nil {
				return errors.Join(deliveryErr, eventErr)
			}
			return deliveryErr
		}
		reminder = updatedReminder
		if !delivered {
			if err := recordReminderDeliveryEvent(ctx, store, payload, reminder, storage.ReminderDeliverySkipped, attempt, provider, "Нагадування вже не було актуальним під час фіналізації.", nil); err != nil {
				return err
			}
		} else if err := recordReminderDeliveryEvent(ctx, store, payload, reminder, storage.ReminderDeliverySent, attempt, provider, sentReminderDeliveryDetail(reminder), nil); err != nil {
			return err
		}
		logger.InfoContext(ctx, "processed reminder delivery",
			"reminder_id", reminder.ID,
			"status", reminder.Status,
			"delivered", delivered,
			"attempt", attempt,
		)
		return nil
	})
	return mux
}

func formatTelegramReminder(reminder storage.Reminder) string {
	return "🔔 Нагадування: " + reminder.Title
}

func reminderDeliveryAttempt(ctx context.Context) int {
	retryCount, ok := asynq.GetRetryCount(ctx)
	if !ok || retryCount < 0 {
		return 1
	}
	return retryCount + 1
}

func reminderDeliveryProvider(reminder storage.Reminder) string {
	switch reminder.DeliveryMethod {
	case "telegram":
		return "telegram_bot"
	case "web":
		return "nova_web"
	default:
		return reminder.DeliveryMethod
	}
}

func recordReminderDeliveryEvent(ctx context.Context, store ReminderDeliveryStore, payload ReminderDeliveryPayload, reminder storage.Reminder, status string, attempt int, provider, detail string, deliveryErr error) error {
	if store == nil {
		return errors.New("storage is not configured")
	}
	errorText := ""
	if deliveryErr != nil {
		errorText = deliveryErr.Error()
	}
	_, err := store.RecordReminderDeliveryEvent(ctx, storage.ReminderDeliveryEvent{
		ReminderID:     payload.ReminderID,
		UserID:         payload.UserID,
		Channel:        reminder.DeliveryMethod,
		Provider:       provider,
		Status:         status,
		Attempt:        attempt,
		Detail:         detail,
		ErrorText:      errorText,
		IdempotencyKey: reminderDeliveryEventID(payload, status, attempt),
	})
	if err != nil {
		return fmt.Errorf("record reminder delivery event: %w", err)
	}
	return nil
}

func reminderDeliveryEventID(payload ReminderDeliveryPayload, status string, attempt int) string {
	triggerAt := strings.TrimSpace(payload.TriggerAt)
	if triggerAt == "" {
		triggerAt = "unknown-trigger"
	}
	return fmt.Sprintf("reminder:%s:%s:attempt:%d:%s", payload.ReminderID, triggerAt, attempt, status)
}

func skippedReminderDeliveryDetail(reminder storage.Reminder) string {
	if reminder.Status != "scheduled" {
		return "Пропущено, бо статус нагадування `" + reminder.Status + "`."
	}
	return "Пропущено, бо час нагадування ще не настав."
}

func sentReminderDeliveryDetail(reminder storage.Reminder) string {
	switch reminder.DeliveryMethod {
	case "telegram":
		return "Telegram повідомлення відправлено, нагадування позначено доставленим."
	case "web":
		return "Web-нагадування позначено доставленим."
	default:
		return "Нагадування позначено доставленим."
	}
}
