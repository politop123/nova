package httpserver

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"nova.local/core/internal/agent"
	"nova.local/core/internal/core"
	"nova.local/core/internal/policy"
	"nova.local/core/internal/storage"
)

func isMutationIntent(intent string) bool {
	switch intent {
	case core.IntentTaskComplete, core.IntentTaskCancel, core.IntentTaskReschedule, core.IntentReminderCancel, core.IntentReminderReschedule:
		return true
	default:
		return false
	}
}

// Never pass an unbounded task list to the model. Long titles are omitted, not
// truncated into selectors that could identify a different task.
func plannerTaskPreview(tasks []storage.Task) ([]agent.PlannerTask, bool) {
	truncated := len(tasks) > 20
	preview := make([]agent.PlannerTask, 0, min(len(tasks), 20))
	for _, task := range tasks[:min(len(tasks), 20)] {
		if task.Status != "open" || len([]rune(task.Title)) > 160 {
			truncated = true
			continue
		}
		item := agent.PlannerTask{Title: task.Title}
		if task.DueAt != nil {
			item.DueAt = task.DueAt.UTC().Format(time.RFC3339)
		}
		preview = append(preview, item)
	}
	return preview, truncated
}

type taskChangeStore interface {
	ListTasks(context.Context, string, string, int) ([]storage.Task, error)
	GetTask(context.Context, string, string) (storage.Task, error)
	TaskChangeResult(context.Context, string, string, string) (*storage.Task, error)
	ChangeOpenTask(context.Context, storage.Task, *time.Time, storage.AgentAction) (storage.Task, error)
}

func (s *Server) executeTaskChange(ctx context.Context, userID, traceID string, action core.NovaAction, hash, key string) (string, *serviceError) {
	decision := policy.Evaluate(policy.Context{UserID: userID, ToolName: action.Type, Permission: policy.Write,
		Authenticated: userID != "", ToolEnabled: true})
	if decision.Outcome != policy.Allow {
		return "Цю дію зараз заборонено.", nil
	}
	reply, err := runTaskChange(ctx, s.store, userID, traceID, action, hash, key, s.requestTimezone(action.Timezone))
	if err != nil {
		return "", newServiceError(http.StatusServiceUnavailable, "task change is temporarily unavailable", err)
	}
	return reply, nil
}

func runTaskChange(ctx context.Context, store taskChangeStore, userID, traceID string, action core.NovaAction, hash, key, timezone string) (string, error) {
	if action.Type != core.ActionTaskComplete && action.Type != core.ActionTaskCancel && action.Type != core.ActionTaskReschedule {
		return "", errors.New("unsupported task change")
	}
	if _, err := time.LoadLocation(timezone); err != nil {
		return "Уточни часовий пояс задачі.", nil
	}
	previous, err := store.TaskChangeResult(ctx, userID, key, hash)
	if err != nil {
		return "", err
	}
	if previous != nil {
		current, err := store.GetTask(ctx, userID, previous.ID)
		if errors.Is(err, storage.ErrNotFound) {
			return "Цей запит уже виконано. Задачі більше немає в актуальному списку.", nil
		}
		if err != nil {
			return "", err
		}
		if !current.UpdatedAt.Equal(previous.UpdatedAt) {
			return "Цей запит уже виконано. Після цього задача змінилася; перевір актуальні плани.", nil
		}
		return taskChangedText(*previous, timezone), nil
	}
	query := strings.TrimSpace(action.Title)
	if normalizeReminderSelector(query) == "" {
		return clarificationForIntent(action.Type), nil
	}
	var targetTime, dueAt *time.Time
	if action.TargetTime != "" {
		targetTime, err = parseRequiredZonedTime(action.TargetTime, timezone, "targetTime")
		if err != nil {
			return "Уточни початкову дату й час дедлайну задачі.", nil
		}
	}
	if action.Type == core.ActionTaskReschedule {
		dueAt, err = parseRequiredZonedTime(action.DueAt, timezone, "dueAt")
		if err != nil {
			return "На яку дату й час перенести дедлайн задачі?", nil
		}
		if !dueAt.After(time.Now().Add(2 * time.Second)) {
			return "Цей час уже минув або надто близько. Вкажи майбутню дату й час дедлайну.", nil
		}
	}
	tasks, err := store.ListTasks(ctx, userID, "open", 100)
	if err != nil {
		return "", err
	}
	if len(tasks) >= 100 {
		return "Активних задач забагато для надійного пошуку в чаті. Зміни потрібну задачу у вебпанелі.", nil
	}
	matches := matchOpenTasks(tasks, userID, query, targetTime)
	if len(matches) == 0 {
		return "Не знайшла відкриту задачу «" + compactAgendaTitle(query) + "». Напиши «Мої задачі», щоб побачити список.", nil
	}
	if len(matches) > 1 {
		lines := []string{"Знайшла кілька задач. Уточни назву й початковий дедлайн:"}
		for _, task := range matches[:min(3, len(matches))] {
			when := "без дедлайну"
			if task.DueAt != nil {
				when = formatReminderTrigger(*task.DueAt, timezone)
			}
			lines = append(lines, fmt.Sprintf("• %s — %s", compactAgendaTitle(task.Title), when))
		}
		lines = append(lines, "Якщо назви й дедлайни однакові, обери потрібну задачу у вебпанелі.")
		return strings.Join(lines, "\n"), nil
	}
	updated, err := store.ChangeOpenTask(ctx, matches[0], dueAt, storage.AgentAction{
		UserID: userID, TraceID: traceID, ActionName: action.Type, ArgumentsHash: hash, IdempotencyKey: key})
	if errors.Is(err, storage.ErrNotFound) {
		return "Задача щойно змінилася або вже завершена. Перевір список і повтори запит.", nil
	}
	if err != nil {
		return "", err
	}
	return taskChangedText(updated, timezone), nil
}

func taskChangedText(task storage.Task, timezone string) string {
	switch task.Status {
	case "done":
		return "✅ Позначила задачу виконаною: " + task.Title + "."
	case "cancelled":
		return "Скасувала задачу: " + task.Title + "."
	default:
		if task.DueAt == nil {
			return "Дедлайн задачі не задано."
		}
		return fmt.Sprintf("Перенесла дедлайн задачі «%s» на %s. Нагадування не змінювала.", task.Title, formatReminderTrigger(*task.DueAt, timezone))
	}
}

func matchOpenTasks(tasks []storage.Task, userID, query string, targetTime *time.Time) []storage.Task {
	words := strings.Fields(normalizeReminderSelector(query))
	var matches []storage.Task
	if len(words) == 0 {
		return matches
	}
	for _, task := range tasks {
		if task.UserID != userID || task.Status != "open" {
			continue
		}
		if targetTime != nil && (task.DueAt == nil || !task.DueAt.Truncate(time.Minute).Equal(targetTime.Truncate(time.Minute))) {
			continue
		}
		title := " " + normalizeReminderSelector(task.Title) + " "
		matched := true
		for _, word := range words {
			if !strings.Contains(title, " "+word+" ") {
				matched = false
				break
			}
		}
		if matched {
			matches = append(matches, task)
		}
	}
	return matches
}
