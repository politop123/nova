package httpserver

import (
	"context"
	"fmt"
	"strings"
	"time"

	"nova.local/core/internal/storage"
)

type agendaStore interface {
	ListAgendaTasks(context.Context, string, *time.Time, *time.Time) ([]storage.Task, error)
	ListAgendaReminders(context.Context, string, *time.Time, *time.Time) ([]storage.Reminder, error)
}

func (s *Server) agendaAssistantText(ctx context.Context, userID, date string) string {
	return readAgenda(ctx, s.store, userID, date, s.requestTimezone(s.cfg.Timezone))
}

func agendaWindow(date, timezone string) (*time.Time, *time.Time, error) {
	location, err := time.LoadLocation(timezone)
	if err != nil {
		return nil, nil, err
	}
	if date == "" {
		return nil, nil, nil
	}
	start, err := time.ParseInLocation("2006-01-02", date, location)
	if err != nil {
		return nil, nil, err
	}
	// Calendar days can be 23/25 hours when the local clock changes.
	end := start.AddDate(0, 0, 1)
	return &start, &end, nil
}

func readAgenda(ctx context.Context, store agendaStore, userID, date, timezone string) string {
	start, end, err := agendaWindow(date, timezone)
	if err != nil {
		return "На який день показати плани? Наприклад, на сьогодні чи завтра."
	}
	tasks, err := store.ListAgendaTasks(ctx, userID, start, end)
	if err != nil {
		return "Не вдалося прочитати задачі. Спробуй запитати про плани ще раз."
	}
	reminders, err := store.ListAgendaReminders(ctx, userID, start, end)
	if err != nil {
		return "Не вдалося прочитати нагадування. Спробуй запитати про плани ще раз."
	}
	return formatAgenda(tasks, reminders, date, timezone)
}

func formatAgenda(tasks []storage.Task, reminders []storage.Reminder, date, timezone string) string {
	location, err := time.LoadLocation(timezone)
	if err != nil {
		location = time.UTC
	}
	label := "Активні плани"
	if date != "" {
		label = "Плани на " + date
		now := time.Now().In(location)
		if date == now.Format("2006-01-02") {
			label = "Плани на сьогодні"
		} else if date == now.AddDate(0, 0, 1).Format("2006-01-02") {
			label = "Плани на завтра"
		}
	}
	if len(tasks)+len(reminders) == 0 {
		return label + ": активних задач і нагадувань немає."
	}
	lines := []string{label + ":"}
	if len(reminders) > 0 {
		lines = append(lines, "Нагадування:")
		for _, reminder := range reminders[:min(8, len(reminders))] {
			lines = append(lines, fmt.Sprintf("• %s — %s", reminder.TriggerAt.In(location).Format("02.01 15:04"), compactAgendaTitle(reminder.Title)))
		}
		if len(reminders) > 8 {
			lines = append(lines, "Є ще нагадування — повний список у вебпанелі.")
		}
	}
	if len(tasks) > 0 {
		lines = append(lines, "Задачі:")
		for _, task := range tasks[:min(8, len(tasks))] {
			when := "без дедлайну"
			if task.DueAt != nil {
				when = task.DueAt.In(location).Format("02.01 15:04")
			}
			lines = append(lines, fmt.Sprintf("• %s — %s", when, compactAgendaTitle(task.Title)))
		}
		if len(tasks) > 8 {
			lines = append(lines, "Є ще задачі — повний список у вебпанелі.")
		}
	}
	if date != "" {
		lines = append(lines, "Задачі без дедлайну — у загальному списку планів.")
	}
	return strings.Join(lines, "\n")
}

func compactAgendaTitle(title string) string {
	text := []rune(strings.Join(strings.Fields(title), " "))
	if len(text) > 120 {
		return string(text[:117]) + "…"
	}
	return string(text)
}
