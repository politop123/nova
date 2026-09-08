package jobs

import (
	"context"
	"errors"
	"log/slog"
	"slices"
	"strings"
	"testing"
	"time"

	"nova.local/core/internal/storage"
)

type fakeReminderDeliveryStore struct {
	reminder     storage.Reminder
	deliverError error
	events       []storage.ReminderDeliveryEvent
}

func (s *fakeReminderDeliveryStore) GetReminder(ctx context.Context, userID, reminderID string) (storage.Reminder, error) {
	if s.reminder.ID == "" || s.reminder.ID != reminderID || s.reminder.UserID != userID {
		return storage.Reminder{}, storage.ErrNotFound
	}
	return s.reminder, nil
}

func (s *fakeReminderDeliveryStore) DeliverReminder(ctx context.Context, userID, reminderID string) (storage.Reminder, bool, error) {
	if s.deliverError != nil {
		return storage.Reminder{}, false, s.deliverError
	}
	if s.reminder.ID == "" || s.reminder.ID != reminderID || s.reminder.UserID != userID {
		return storage.Reminder{}, false, storage.ErrNotFound
	}
	if s.reminder.Status != "scheduled" {
		return s.reminder, false, nil
	}
	s.reminder.Status = "delivered"
	s.reminder.UpdatedAt = time.Now().UTC()
	return s.reminder, true, nil
}

func (s *fakeReminderDeliveryStore) RecordReminderDeliveryEvent(ctx context.Context, event storage.ReminderDeliveryEvent) (storage.ReminderDeliveryEvent, error) {
	event.ID = "event-" + event.Status
	if event.CreatedAt.IsZero() {
		event.CreatedAt = time.Now().UTC()
	}
	s.events = append(s.events, event)
	return event, nil
}

type fakeTelegramSender struct {
	err      error
	messages []string
}

func (s *fakeTelegramSender) SendMessage(ctx context.Context, chatID int64, text string) error {
	if s.err != nil {
		return s.err
	}
	s.messages = append(s.messages, text)
	return nil
}

func TestReminderDeliveryHandlerRecordsSentEvent(t *testing.T) {
	store := &fakeReminderDeliveryStore{reminder: testReminder("telegram", "scheduled", time.Now().Add(-time.Minute))}
	telegram := &fakeTelegramSender{}
	task, err := NewReminderDeliveryTask(store.reminder)
	if err != nil {
		t.Fatalf("new reminder task: %v", err)
	}

	mux := Handler(slog.Default(), store, HandlerConfig{Telegram: telegram, TelegramChatID: 42})
	if err := mux.ProcessTask(context.Background(), task); err != nil {
		t.Fatalf("process reminder delivery task: %v", err)
	}

	if len(telegram.messages) != 1 || !strings.Contains(telegram.messages[0], store.reminder.Title) {
		t.Fatalf("unexpected telegram messages: %#v", telegram.messages)
	}
	got := eventStatuses(store.events)
	want := []string{storage.ReminderDeliveryAttempted, storage.ReminderDeliverySent}
	if !slices.Equal(got, want) {
		t.Fatalf("event statuses = %#v, want %#v; events=%#v", got, want, store.events)
	}
	if store.events[1].Attempt != 1 || store.events[1].Provider != "telegram_bot" || store.events[1].Channel != "telegram" {
		t.Fatalf("unexpected sent event metadata: %#v", store.events[1])
	}
}

func TestReminderDeliveryHandlerRecordsFailedTelegramEvent(t *testing.T) {
	store := &fakeReminderDeliveryStore{reminder: testReminder("telegram", "scheduled", time.Now().Add(-time.Minute))}
	telegram := &fakeTelegramSender{err: errors.New("telegram offline")}
	task, err := NewReminderDeliveryTask(store.reminder)
	if err != nil {
		t.Fatalf("new reminder task: %v", err)
	}

	mux := Handler(slog.Default(), store, HandlerConfig{Telegram: telegram, TelegramChatID: 42})
	err = mux.ProcessTask(context.Background(), task)
	if err == nil {
		t.Fatal("expected telegram delivery error")
	}

	got := eventStatuses(store.events)
	want := []string{storage.ReminderDeliveryAttempted, storage.ReminderDeliveryFailed}
	if !slices.Equal(got, want) {
		t.Fatalf("event statuses = %#v, want %#v; events=%#v", got, want, store.events)
	}
	if !strings.Contains(store.events[1].ErrorText, "telegram offline") {
		t.Fatalf("expected telegram error text, got %#v", store.events[1])
	}
}

func TestReminderDeliveryHandlerRecordsSkippedEvent(t *testing.T) {
	store := &fakeReminderDeliveryStore{reminder: testReminder("telegram", "cancelled", time.Now().Add(-time.Minute))}
	task, err := NewReminderDeliveryTask(store.reminder)
	if err != nil {
		t.Fatalf("new reminder task: %v", err)
	}

	mux := Handler(slog.Default(), store, HandlerConfig{Telegram: &fakeTelegramSender{}, TelegramChatID: 42})
	if err := mux.ProcessTask(context.Background(), task); err != nil {
		t.Fatalf("process skipped reminder delivery task: %v", err)
	}

	got := eventStatuses(store.events)
	want := []string{storage.ReminderDeliveryAttempted, storage.ReminderDeliverySkipped}
	if !slices.Equal(got, want) {
		t.Fatalf("event statuses = %#v, want %#v; events=%#v", got, want, store.events)
	}
}

func testReminder(deliveryMethod, status string, triggerAt time.Time) storage.Reminder {
	now := time.Now().UTC()
	return storage.Reminder{
		ID:             "00000000-0000-0000-0000-000000000123",
		UserID:         "00000000-0000-0000-0000-000000000001",
		Title:          "Перевірити доставку",
		TriggerAt:      triggerAt.UTC(),
		Timezone:       "Europe/Kyiv",
		Priority:       "normal",
		DeliveryMethod: deliveryMethod,
		Status:         status,
		CreatedAt:      now,
		UpdatedAt:      now,
	}
}

func eventStatuses(events []storage.ReminderDeliveryEvent) []string {
	result := make([]string, 0, len(events))
	for _, event := range events {
		result = append(result, event.Status)
	}
	return result
}
