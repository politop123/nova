package httpserver

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"

	"nova.local/core/internal/storage"
)

type reminderCommand struct {
	Title     string
	TriggerAt time.Time
}

func (s *Server) createReminderFromChatCommand(ctx context.Context, userID, channel, text, traceID string) (string, *storage.Reminder, *serviceError) {
	command, err := parseReminderCommand(text, s.cfg.Timezone, time.Now())
	if err != nil {
		return "Я бачу, що ти просиш нагадати, але поки найнадійніше розумію формат: «нагадай через 10 хвилин: перевірити NOVA».", nil, nil
	}
	deliveryMethod := "web"
	if channel == "telegram" {
		deliveryMethod = "telegram"
	} else if s.telegramReadyForDelivery() {
		deliveryMethod = "telegram"
	}
	reminder := storage.Reminder{
		UserID:         userID,
		Title:          command.Title,
		TriggerAt:      command.TriggerAt,
		Timezone:       requestedTimezone(s.cfg.Timezone),
		Priority:       "normal",
		DeliveryMethod: deliveryMethod,
		Status:         "scheduled",
	}
	created, err := s.store.CreateReminder(ctx, reminder, "chat:"+traceID+":reminder")
	if err != nil {
		return "", nil, newServiceError(500, "reminder could not be created", err)
	}
	if err := s.scheduleReminder(ctx, created); err != nil {
		s.logger.Error("chat reminder scheduling failed", "reminder_id", created.ID, "error", err)
		return "", nil, newServiceError(503, "reminder could not be scheduled", err)
	}
	return reminderCreatedText(created, s.cfg.Timezone, s.reminderDeliveryLabel(created)), &created, nil
}

func (s *Server) reminderDeliveryLabel(reminder storage.Reminder) string {
	switch reminder.DeliveryMethod {
	case "telegram":
		return "в Telegram"
	default:
		return "у веб-дашборді NOVA"
	}
}

func reminderCreatedText(reminder storage.Reminder, timezone, deliveryLabel string) string {
	return fmt.Sprintf("Готово — нагадаю %s %s: %s.", formatReminderTrigger(reminder.TriggerAt, timezone), deliveryLabel, reminder.Title)
}

func formatReminderTrigger(triggerAt time.Time, timezone string) string {
	location, err := time.LoadLocation(requestedTimezone(timezone))
	if err != nil {
		location = time.UTC
	}
	local := triggerAt.In(location)
	now := time.Now().In(location)
	day := "02.01.2006"
	switch {
	case sameLocalDate(local, now):
		day = "сьогодні"
	case sameLocalDate(local, now.Add(24*time.Hour)):
		day = "завтра"
	default:
		day = local.Format("02.01.2006")
	}
	return fmt.Sprintf("%s о %s", day, local.Format("15:04"))
}

func sameLocalDate(a, b time.Time) bool {
	yearA, monthA, dayA := a.Date()
	yearB, monthB, dayB := b.Date()
	return yearA == yearB && monthA == monthB && dayA == dayB
}

var (
	relativeReminderPattern = regexp.MustCompile(`(?i)(?:^|[\s,;:!?—–-])(?:через|за)\s+([0-9]+|один|одна|одну|два|дві|пару|три|чотири|п'ять|п’ять|пять|шість|сім|вісім|дев'ять|дев’ять|девять|десять)\s+(секунд(?:у|и)?|сек\.?|хвилин(?:у|и)?|хв\.?|годин(?:у|и)?|год\.?|день|дні|дня|днів)(?:$|[\s,;:!?—–-])`)
	reminderVerbPattern     = regexp.MustCompile(`(?i)(?:^|[\s,;:!?—–-])(?:нагадай|нагадати)(?:$|[\s,;:!?—–-])`)
	fillerPhrasePattern     = regexp.MustCompile(`(?i)(?:^|[\s,;:!?—–-])(?:можеш|будь ласка|будь-ласка|потрібно|треба)(?:$|[\s,;:!?—–-])`)
	spacePattern            = regexp.MustCompile(`\s+`)
)

func parseReminderCommand(text, timezone string, now time.Time) (reminderCommand, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return reminderCommand{}, errors.New("reminder command is empty")
	}
	location, err := time.LoadLocation(requestedTimezone(timezone))
	if err != nil {
		return reminderCommand{}, err
	}
	match := relativeReminderPattern.FindStringSubmatch(text)
	if len(match) != 3 {
		return reminderCommand{}, errors.New("relative reminder time is required")
	}
	amount, err := parseUkrainianNumber(match[1])
	if err != nil {
		return reminderCommand{}, err
	}
	duration, err := reminderDuration(amount, match[2])
	if err != nil {
		return reminderCommand{}, err
	}
	triggerAt := now.In(location).Add(duration).UTC()
	title := extractReminderTitle(text)
	if title == "" {
		title = "Нагадування"
	}
	return reminderCommand{Title: title, TriggerAt: triggerAt}, nil
}

func requestedTimezone(timezone string) string {
	timezone = strings.TrimSpace(timezone)
	if timezone == "" {
		return "Europe/Kyiv"
	}
	return timezone
}

func parseUkrainianNumber(value string) (int, error) {
	value = strings.ToLower(strings.TrimSpace(value))
	if number, err := strconv.Atoi(value); err == nil && number > 0 {
		return number, nil
	}
	numbers := map[string]int{
		"один":    1,
		"одна":    1,
		"одну":    1,
		"два":     2,
		"дві":     2,
		"пару":    2,
		"три":     3,
		"чотири":  4,
		"п'ять":   5,
		"п’ять":   5,
		"пять":    5,
		"шість":   6,
		"сім":     7,
		"вісім":   8,
		"дев'ять": 9,
		"дев’ять": 9,
		"девять":  9,
		"десять":  10,
	}
	if number, ok := numbers[value]; ok {
		return number, nil
	}
	return 0, errors.New("unsupported reminder amount")
}

func reminderDuration(amount int, unit string) (time.Duration, error) {
	if amount <= 0 {
		return 0, errors.New("reminder amount must be positive")
	}
	unit = strings.ToLower(strings.Trim(unit, ". "))
	switch {
	case strings.HasPrefix(unit, "сек"):
		return time.Duration(amount) * time.Second, nil
	case strings.HasPrefix(unit, "хв"):
		return time.Duration(amount) * time.Minute, nil
	case strings.HasPrefix(unit, "год"):
		return time.Duration(amount) * time.Hour, nil
	case strings.HasPrefix(unit, "д"):
		return time.Duration(amount) * 24 * time.Hour, nil
	default:
		return 0, errors.New("unsupported reminder unit")
	}
}

func extractReminderTitle(text string) string {
	if tail, ok := textAfterMarker(text, "щоб"); ok {
		return cleanReminderTitle(tail)
	}
	if tail, ok := textAfterMarker(text, "про"); ok {
		return cleanReminderTitle(tail)
	}
	withoutTime := relativeReminderPattern.ReplaceAllString(text, " ")
	withoutVerb := reminderVerbPattern.ReplaceAllString(withoutTime, " ")
	return cleanReminderTitle(withoutVerb)
}

func textAfterMarker(text, marker string) (string, bool) {
	pattern := regexp.MustCompile(`(?i)(^|[\s,;:!?—–-])` + regexp.QuoteMeta(marker) + `([\s,;:!?—–-]+)`)
	match := pattern.FindStringIndex(text)
	if len(match) != 2 {
		return "", false
	}
	return text[match[1]:], true
}

func cleanReminderTitle(text string) string {
	text = strings.TrimFunc(text, func(r rune) bool {
		return unicode.IsSpace(r) || strings.ContainsRune(".,;:!?—-–", r)
	})
	text = fillerPhrasePattern.ReplaceAllString(text, " ")
	text = spacePattern.ReplaceAllString(text, " ")
	text = strings.TrimFunc(text, func(r rune) bool {
		return unicode.IsSpace(r) || strings.ContainsRune(".,;:!?—-–", r)
	})
	if text == "" {
		return ""
	}
	runes := []rune(text)
	runes[0] = unicode.ToUpper(runes[0])
	return string(runes)
}
