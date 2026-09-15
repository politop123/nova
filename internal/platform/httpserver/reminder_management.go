package httpserver

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"

	"nova.local/core/internal/agent"
	"nova.local/core/internal/core"
	"nova.local/core/internal/storage"
)

func (s *Server) reminderManagementFallback(ctx context.Context, userID, text, intent string) plannedActionResult {
	if intent == "" {
		intent = agent.DetectDeterministicIntent(text)
	}
	result := plannedActionResult{Handled: true, Intent: intent}
	switch intent {
	case core.IntentAgendaList:
		location, err := time.LoadLocation(s.requestTimezone(s.cfg.Timezone))
		if err != nil {
			result.AssistantText = "Не вдалося визначити часовий пояс для планів."
			return result
		}
		date := ""
		normalized := strings.ToLower(text)
		now := time.Now().In(location)
		switch {
		case strings.Contains(normalized, "післязавтра"):
			date = now.AddDate(0, 0, 2).Format("2006-01-02")
		case strings.Contains(normalized, "завтра") && !strings.Contains(normalized, "сьогодні"):
			date = now.AddDate(0, 0, 1).Format("2006-01-02")
		case strings.Contains(normalized, "сьогодні") && !strings.Contains(normalized, "завтра"):
			date = now.Format("2006-01-02")
		case normalized != "мої плани" && normalized != "мої нагадування" && normalized != "мої задачі":
			result.AssistantText = "Зараз можу показати плани на один день. Напиши «Що в мене сьогодні?» або «Що в мене завтра?»."
			return result
		}
		result.AssistantText = s.agendaAssistantText(ctx, userID, date)
	case core.IntentReminderCancel, core.IntentReminderReschedule:
		result.AssistantText = "Зараз не вдалося розібрати запит на зміну нагадування. Спробуй ще раз або зміни його у вебпанелі."
	default:
		return plannedActionResult{}
	}
	return result
}

type reminderChangeStore interface {
	ListReminders(context.Context, string, string, int) ([]storage.Reminder, error)
	GetReminder(context.Context, string, string) (storage.Reminder, error)
	ReminderChangeResult(context.Context, string, string) (*storage.Reminder, error)
	ChangeScheduledReminder(context.Context, storage.Reminder, *time.Time, string, storage.AgentAction) (storage.Reminder, error)
}

func (s *Server) executeReminderChange(ctx context.Context, userID, traceID string, action core.NovaAction, hash, key string) (string, *serviceError) {
	reply, err := runReminderChange(ctx, s.store, s.scheduleReminder, userID, traceID, action, hash, key, s.requestTimezone(action.Timezone))
	if err != nil {
		return "", newServiceError(503, "reminder change is temporarily unavailable", err)
	}
	return reply, nil
}

func runReminderChange(ctx context.Context, store reminderChangeStore, schedule func(context.Context, storage.Reminder) error, userID, traceID string, action core.NovaAction, hash, key, timezone string) (string, error) {
	if action.Type != core.ActionReminderCancel && action.Type != core.ActionReminderReschedule {
		return "", errors.New("unsupported reminder change")
	}
	if _, err := time.LoadLocation(timezone); err != nil {
		return "Уточни часовий пояс нагадування.", nil
	}
	previous, err := store.ReminderChangeResult(ctx, userID, key)
	if err != nil {
		return "", err
	}
	if previous != nil {
		return finishReminderChange(ctx, store, schedule, *previous)
	}
	query := firstNonEmpty(action.Title, action.Content)
	if normalizeReminderSelector(query) == "" {
		return "Яке саме нагадування змінити? Напиши його тему.", nil
	}
	var triggerAt, targetTime *time.Time
	if action.TargetTime != "" {
		targetTime, err = parseRequiredZonedTime(action.TargetTime, timezone, "targetTime")
		if err != nil {
			return "Уточни початкову дату й час нагадування, яке потрібно змінити.", nil
		}
	}
	if action.Type == core.ActionReminderReschedule {
		triggerAt, err = parseRequiredZonedTime(action.TriggerAt, timezone, "triggerAt")
		if err != nil {
			return "На яку дату й час перенести нагадування?", nil
		}
		if !triggerAt.After(time.Now().Add(2 * time.Second)) {
			return "Цей час уже минув або надто близько. Вкажи майбутню дату й час.", nil
		}
	}
	reminders, err := store.ListReminders(ctx, userID, "scheduled", 100)
	if err != nil {
		return "", err
	}
	// A truncated search cannot prove that a match is unique.
	if len(reminders) >= 100 {
		return "Активних нагадувань забагато для надійного пошуку в чаті. Зміни потрібне нагадування у вебпанелі.", nil
	}
	matches := matchScheduledReminders(reminders, userID, query, targetTime)
	if len(matches) == 0 {
		return "Не знайшла активне нагадування про «" + compactAgendaTitle(query) + "». Напиши «Мої нагадування», щоб побачити список.", nil
	}
	if len(matches) > 1 {
		lines := []string{"Знайшла кілька нагадувань. Уточни назву й початкову дату та час:"}
		for _, reminder := range matches[:min(3, len(matches))] {
			lines = append(lines, fmt.Sprintf("• %s — %s", compactAgendaTitle(reminder.Title), formatReminderTrigger(reminder.TriggerAt, timezone)))
		}
		if len(matches) > 3 {
			lines = append(lines, "Є й інші збіги; повний список у вебпанелі.")
		}
		return strings.Join(lines, "\n"), nil
	}
	updated, err := store.ChangeScheduledReminder(ctx, matches[0], triggerAt, timezone, storage.AgentAction{
		UserID: userID, TraceID: traceID, ActionName: action.Type, ArgumentsHash: hash, IdempotencyKey: key,
	})
	if errors.Is(err, storage.ErrNotFound) {
		return "Нагадування щойно змінилося або вже було доставлене. Перевір список і повтори запит.", nil
	}
	if err != nil {
		return "", err
	}
	return finishReminderChange(ctx, store, schedule, updated)
}

func finishReminderChange(ctx context.Context, store reminderChangeStore, schedule func(context.Context, storage.Reminder) error, reminder storage.Reminder) (string, error) {
	if reminder.Status == "cancelled" {
		return "Скасувала нагадування: " + reminder.Title + ".", nil
	}
	// Replaying an old request must not re-enqueue a subsequently cancelled or moved reminder.
	current, err := store.GetReminder(ctx, reminder.UserID, reminder.ID)
	if err != nil {
		return "", err
	}
	if current.Status != "scheduled" || !current.TriggerAt.Equal(reminder.TriggerAt) {
		return "Цей запит уже виконано. Після цього стан нагадування змінився; перевір актуальні плани.", nil
	}
	if err := schedule(ctx, current); err != nil {
		return "Новий час збережено, але не вдалося поставити сповіщення в чергу. Доставка ще не підтверджена; спробуй перенести його ще раз.", nil
	}
	return fmt.Sprintf("Перенесла нагадування «%s» на %s.", reminder.Title, formatReminderTrigger(reminder.TriggerAt, reminder.Timezone)), nil
}

// Use complete words and require every query word; never pick a fuzzy best match
// for a mutation. An optional original timestamp disambiguates repeated titles.
func matchScheduledReminders(reminders []storage.Reminder, userID, query string, targetTime *time.Time) []storage.Reminder {
	words := strings.Fields(normalizeReminderSelector(query))
	var matches []storage.Reminder
	if len(words) == 0 {
		return matches
	}
	for _, reminder := range reminders {
		if reminder.UserID != userID || reminder.Status != "scheduled" {
			continue
		}
		if targetTime != nil && !reminder.TriggerAt.Truncate(time.Minute).Equal(targetTime.Truncate(time.Minute)) {
			continue
		}
		title := " " + normalizeReminderSelector(reminder.Title) + " "
		matched := true
		for _, word := range words {
			if !strings.Contains(title, " "+word+" ") {
				matched = false
				break
			}
		}
		if matched {
			matches = append(matches, reminder)
		}
	}
	return matches
}

func normalizeReminderSelector(text string) string {
	return strings.Join(strings.FieldsFunc(strings.ToLower(text), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsNumber(r)
	}), " ")
}
