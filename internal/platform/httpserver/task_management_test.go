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

type taskChangeFake struct {
	items                      []storage.Task
	prior                      *storage.Task
	current                    storage.Task
	changes                    int
	listErr, changeErr, getErr error
}

func (f *taskChangeFake) ListTasks(context.Context, string, string, int) ([]storage.Task, error) {
	return f.items, f.listErr
}
func (f *taskChangeFake) GetTask(context.Context, string, string) (storage.Task, error) {
	return f.current, f.getErr
}
func (f *taskChangeFake) TaskChangeResult(context.Context, string, string, string) (*storage.Task, error) {
	return f.prior, nil
}
func (f *taskChangeFake) ChangeOpenTask(_ context.Context, task storage.Task, at *time.Time, action storage.AgentAction) (storage.Task, error) {
	if f.changeErr != nil {
		return storage.Task{}, f.changeErr
	}
	f.changes++
	switch action.ActionName {
	case core.ActionTaskComplete:
		task.Status = "done"
	case core.ActionTaskCancel:
		task.Status = "cancelled"
	case core.ActionTaskReschedule:
		task.DueAt = at
	}
	f.prior = &task
	f.current = task
	return task, nil
}

func TestTaskManagementValidation(t *testing.T) {
	now := time.Now().UTC()
	base := storage.Task{ID: "task", UserID: "user", Title: "Оплатити рахунок", Status: "open", UpdatedAt: now}
	for _, tc := range []struct {
		name               string
		action             core.NovaAction
		items              []storage.Task
		want               string
		changes            int
		listErr, changeErr error
	}{
		{"complete", core.NovaAction{Type: core.ActionTaskComplete, Title: "рахунок"}, []storage.Task{base}, "виконаною", 1, nil, nil},
		{"cancel", core.NovaAction{Type: core.ActionTaskCancel, Title: "рахунок"}, []storage.Task{base}, "Скасувала", 1, nil, nil},
		{"reschedule", core.NovaAction{Type: core.ActionTaskReschedule, Title: "рахунок", DueAt: now.Add(time.Hour).Format(time.RFC3339)}, []storage.Task{base}, "Нагадування не змінювала", 1, nil, nil},
		{"missing title", core.NovaAction{Type: core.ActionTaskComplete}, []storage.Task{base}, "Яку саме", 0, nil, nil},
		{"missing new date", core.NovaAction{Type: core.ActionTaskReschedule, Title: "рахунок"}, []storage.Task{base}, "На яку дату", 0, nil, nil},
		{"past", core.NovaAction{Type: core.ActionTaskReschedule, Title: "рахунок", DueAt: now.Add(-time.Hour).Format(time.RFC3339)}, []storage.Task{base}, "минув", 0, nil, nil},
		{"bad target time", core.NovaAction{Type: core.ActionTaskComplete, Title: "рахунок", TargetTime: "tomorrow"}, []storage.Task{base}, "початкову", 0, nil, nil},
		{"ambiguous", core.NovaAction{Type: core.ActionTaskComplete, Title: "рахунок"}, []storage.Task{base, base}, "кілька задач", 0, nil, nil},
		{"not found", core.NovaAction{Type: core.ActionTaskComplete, Title: "паспорт"}, []storage.Task{base}, "Не знайшла", 0, nil, nil},
		{"truncated search", core.NovaAction{Type: core.ActionTaskCancel, Title: "рахунок"}, make([]storage.Task, 100), "забагато", 0, nil, nil},
		{"stale", core.NovaAction{Type: core.ActionTaskCancel, Title: "рахунок"}, []storage.Task{base}, "щойно змінилася", 0, nil, storage.ErrNotFound},
		{"database failure", core.NovaAction{Type: core.ActionTaskComplete, Title: "рахунок"}, nil, "", 0, errors.New("offline"), nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := &taskChangeFake{items: tc.items, listErr: tc.listErr, changeErr: tc.changeErr}
			reply, err := runTaskChange(context.Background(), f, "user", "trace", tc.action, "hash", "key", "Europe/Kyiv")
			if (err != nil) != (tc.listErr != nil) || !strings.Contains(reply, tc.want) || f.changes != tc.changes {
				t.Fatalf("reply=%q err=%v changes=%d", reply, err, f.changes)
			}
			if tc.changes > 0 {
				_, err = runTaskChange(context.Background(), f, "user", "trace", tc.action, "hash", "key", "Europe/Kyiv")
				if err != nil || f.changes != 1 {
					t.Fatal("request replay changed task twice")
				}
				f.current.UpdatedAt = now.Add(time.Minute)
				reply, err = runTaskChange(context.Background(), f, "user", "trace", tc.action, "hash", "key", "Europe/Kyiv")
				if err != nil || !strings.Contains(reply, "Після цього") {
					t.Fatalf("stale receipt=%s %v", reply, err)
				}
			}
		})
	}
}

func TestTaskMatchingIsConservative(t *testing.T) {
	at := time.Date(2026, 10, 1, 8, 30, 45, 0, time.UTC)
	later := at.Add(time.Hour)
	items := []storage.Task{
		{ID: "one", UserID: "u", Title: "Паспорт", Status: "open", DueAt: &at},
		{ID: "two", UserID: "u", Title: "Паспорт", Status: "open", DueAt: &later},
		{ID: "undated", UserID: "u", Title: "Паспорт", Status: "open"},
		{ID: "foreign", UserID: "other", Title: "Паспорт", Status: "open", DueAt: &at},
		{ID: "done", UserID: "u", Title: "Паспорт", Status: "done", DueAt: &at},
		{ID: "substring", UserID: "u", Title: "Паспортний стіл", Status: "open", DueAt: &at},
	}
	if got := matchOpenTasks(items, "u", "ПАСПОРТ!", nil); len(got) != 3 {
		t.Fatalf("matches=%v", got)
	}
	minute := at.Truncate(time.Minute)
	if got := matchOpenTasks(items, "u", "паспорт", &minute); len(got) != 1 || got[0].ID != "one" {
		t.Fatalf("timed matches=%v", got)
	}
	if len(matchOpenTasks(items, "u", "", nil)) != 0 {
		t.Fatal("empty selector matched")
	}
}

func TestPlannerTaskPreviewIsBounded(t *testing.T) {
	items := make([]storage.Task, 21)
	for i := range items {
		items[i] = storage.Task{Status: "open", Title: "Рахунок"}
	}
	items[0].Title = strings.Repeat("ї", 161)
	preview, truncated := plannerTaskPreview(items)
	if !truncated || len(preview) != 19 {
		t.Fatalf("preview=%v truncated=%v", preview, truncated)
	}
	if result, truncated := plannerTaskPreview(nil); len(result) != 0 || truncated {
		t.Fatal("empty preview marked truncated")
	}
}

func TestTaskIntentHandlingAndFallback(t *testing.T) {
	s := &Server{}
	for _, intent := range []string{core.IntentTaskComplete, core.IntentTaskCancel, core.IntentTaskReschedule} {
		if !isMutationIntent(intent) || !planNeedsHandling(core.ActionPlan{Intent: intent}) || !strings.Contains(clarificationForIntent(intent), "задач") {
			t.Fatalf("missing task capability: %s", intent)
		}
		result := s.reminderManagementFallback(context.Background(), "u", "", intent)
		if !result.Handled || !strings.Contains(result.AssistantText, "Нічого не змінювала") {
			t.Fatalf("unsafe fallback: %+v", result)
		}
	}
}
