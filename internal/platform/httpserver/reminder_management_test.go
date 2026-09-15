package httpserver

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"nova.local/core/internal/core"
	"nova.local/core/internal/storage"
)

type changeStoreFake struct {
	items     []storage.Reminder
	previous  *storage.Reminder
	changes   int
	changeErr error
}

func (f *changeStoreFake) ListReminders(context.Context, string, string, int) ([]storage.Reminder, error) {
	return f.items, nil
}
func (f *changeStoreFake) ReminderChangeResult(context.Context, string, string) (*storage.Reminder, error) {
	return f.previous, nil
}
func (f *changeStoreFake) GetReminder(_ context.Context, userID, id string) (storage.Reminder, error) {
	for _, item := range f.items {
		if item.UserID == userID && item.ID == id {
			return item, nil
		}
	}
	return storage.Reminder{}, storage.ErrNotFound
}
func (f *changeStoreFake) ChangeScheduledReminder(_ context.Context, r storage.Reminder, at *time.Time, timezone string, a storage.AgentAction) (storage.Reminder, error) {
	if f.changeErr != nil {
		return storage.Reminder{}, f.changeErr
	}
	f.changes++
	if a.ActionName == core.ActionReminderCancel {
		r.Status = "cancelled"
	} else {
		r.TriggerAt, r.Timezone = *at, timezone
	}
	for i := range f.items {
		if f.items[i].ID == r.ID {
			f.items[i] = r
		}
	}
	f.previous = &r
	return r, nil
}

func TestReminderChanges(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Minute)
	base := storage.Reminder{ID: "one", UserID: "user", Title: "Забрати паспорт", TriggerAt: now.Add(time.Hour), Status: "scheduled", Timezone: "Europe/Kyiv"}
	for _, tc := range []struct {
		name                string
		action              core.NovaAction
		items               []storage.Reminder
		want                string
		changes, queued     int
		queueErr, changeErr error
	}{
		{"cancel", core.NovaAction{Type: core.ActionReminderCancel, Title: "паспорт"}, []storage.Reminder{base}, "Скасувала", 1, 0, nil, nil},
		{"reschedule", core.NovaAction{Type: core.ActionReminderReschedule, Title: "паспорт", TriggerAt: now.Add(2 * time.Hour).Format(time.RFC3339)}, []storage.Reminder{base}, "Перенесла", 1, 1, nil, nil},
		{"missing subject", core.NovaAction{Type: core.ActionReminderCancel}, []storage.Reminder{base}, "Яке саме", 0, 0, nil, nil},
		{"past", core.NovaAction{Type: core.ActionReminderReschedule, Title: "паспорт", TriggerAt: now.Add(-time.Hour).Format(time.RFC3339)}, []storage.Reminder{base}, "минув", 0, 0, nil, nil},
		{"missing time", core.NovaAction{Type: core.ActionReminderReschedule, Title: "паспорт"}, []storage.Reminder{base}, "яку дату", 0, 0, nil, nil},
		{"ambiguous", core.NovaAction{Type: core.ActionReminderCancel, Title: "паспорт"}, []storage.Reminder{base, base}, "кілька", 0, 0, nil, nil},
		{"unknown", core.NovaAction{Type: core.ActionReminderCancel, Title: "ліки"}, []storage.Reminder{base}, "Не знайшла", 0, 0, nil, nil},
		{"stale", core.NovaAction{Type: core.ActionReminderCancel, Title: "паспорт"}, []storage.Reminder{base}, "щойно змінилося", 0, 0, nil, storage.ErrNotFound},
		{"queue failed", core.NovaAction{Type: core.ActionReminderReschedule, Title: "паспорт", TriggerAt: now.Add(2 * time.Hour).Format(time.RFC3339)}, []storage.Reminder{base}, "Доставка ще не підтверджена", 1, 1, errors.New("offline"), nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := &changeStoreFake{items: append([]storage.Reminder{}, tc.items...), changeErr: tc.changeErr}
			queued := 0
			schedule := func(context.Context, storage.Reminder) error { queued++; return tc.queueErr }
			reply, err := runReminderChange(context.Background(), store, schedule, "user", "trace", tc.action, "hash", "key", "Europe/Kyiv")
			if err != nil || !strings.Contains(reply, tc.want) || store.changes != tc.changes || queued != tc.queued {
				t.Fatalf("reply=%q err=%v changes=%d queued=%d", reply, err, store.changes, queued)
			}
			if tc.changes == 1 {
				_, err = runReminderChange(context.Background(), store, schedule, "user", "trace", tc.action, "hash", "key", "Europe/Kyiv")
				if err != nil || store.changes != 1 {
					t.Fatalf("replayed mutation: changes=%d err=%v", store.changes, err)
				}
			}
		})
	}
}

func TestReminderMatchingDoesNotGuess(t *testing.T) {
	at := time.Date(2026, 9, 15, 10, 0, 32, 0, time.UTC)
	items := []storage.Reminder{
		{ID: "one", UserID: "user", Title: "Паспорт", TriggerAt: at, Status: "scheduled"},
		{ID: "two", UserID: "user", Title: "Паспорт", TriggerAt: at.Add(time.Hour), Status: "scheduled"},
		{ID: "foreign", UserID: "other", Title: "Паспорт", TriggerAt: at, Status: "scheduled"},
		{ID: "cancelled", UserID: "user", Title: "Паспорт", TriggerAt: at, Status: "cancelled"},
		{ID: "substring", UserID: "user", Title: "Паспортний стіл", TriggerAt: at, Status: "scheduled"},
	}
	if got := matchScheduledReminders(items, "user", "паспорт", nil); len(got) != 2 {
		t.Fatalf("matches=%v", got)
	}
	minute := at.Truncate(time.Minute)
	if got := matchScheduledReminders(items, "user", "ПАСПОРТ!", &minute); len(got) != 1 || got[0].ID != "one" {
		t.Fatalf("timed matches=%v", got)
	}
	if got := matchScheduledReminders(items, "user", "", nil); len(got) != 0 {
		t.Fatal("empty selector matched")
	}
}

type agendaStoreFake struct {
	start, end *time.Time
	fail       bool
}

func (f *agendaStoreFake) ListAgendaTasks(_ context.Context, _ string, start, end *time.Time) ([]storage.Task, error) {
	f.start, f.end = start, end
	if f.fail {
		return nil, errors.New("offline")
	}
	return []storage.Task{{Title: "Зустріч", DueAt: start}}, nil
}
func (f *agendaStoreFake) ListAgendaReminders(context.Context, string, *time.Time, *time.Time) ([]storage.Reminder, error) {
	return nil, nil
}

func TestAgendaLocalCalendarAndReadFailure(t *testing.T) {
	for _, tc := range []struct {
		date  string
		hours int
	}{{"2026-03-29", 23}, {"2026-10-25", 25}, {"2026-09-15", 24}} {
		start, end, err := agendaWindow(tc.date, "Europe/Kyiv")
		if err != nil || int(end.Sub(*start).Hours()) != tc.hours || start.Hour() != 0 || end.Hour() != 0 {
			t.Fatalf("window %s: %v %v %v", tc.date, start, end, err)
		}
	}
	f := &agendaStoreFake{}
	text := readAgenda(context.Background(), f, "user", "2026-09-15", "Europe/Kyiv")
	if f.start == nil || f.end == nil || !strings.Contains(text, "Зустріч") {
		t.Fatalf("agenda=%q", text)
	}
	f.fail = true
	text = readAgenda(context.Background(), f, "user", "2026-09-15", "Europe/Kyiv")
	if !strings.Contains(text, "Не вдалося") || strings.Contains(text, "немає") {
		t.Fatalf("failure looked empty: %q", text)
	}
	if _, _, err := agendaWindow("tomorrow", "Europe/Kyiv"); err == nil {
		t.Fatal("invalid date accepted")
	}
}
