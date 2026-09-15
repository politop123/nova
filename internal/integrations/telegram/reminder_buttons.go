package telegram

import (
	"context"
	"errors"
	"regexp"
	"strings"

	"nova.local/core/internal/core"
)

type InlineButton struct {
	Text string `json:"text"`
	Data string `json:"callback_data"`
}

type InlineKeyboard struct {
	Rows [][]InlineButton `json:"inline_keyboard"`
}

var grantIDPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

func ReminderKeyboard(grantID string) (InlineKeyboard, error) {
	if !grantIDPattern.MatchString(grantID) {
		return InlineKeyboard{}, errors.New("invalid reminder grant")
	}
	return InlineKeyboard{Rows: [][]InlineButton{
		{{Text: "✅ Виконано", Data: "r:" + grantID + ":done"}},
		{{Text: "⏰ Через 10 хв", Data: "r:" + grantID + ":s10"}, {Text: "⏰ Через годину", Data: "r:" + grantID + ":s60"}},
	}}, nil
}

func ParseReminderCallback(data string) (string, core.ReminderQuickAction, error) {
	parts := strings.Split(data, ":")
	if len(data) > 64 || len(parts) != 3 || parts[0] != "r" || !grantIDPattern.MatchString(parts[1]) {
		return "", "", errors.New("invalid reminder callback")
	}
	action := core.ReminderQuickAction(parts[2])
	if !action.Valid() {
		return "", "", errors.New("unsupported reminder action")
	}
	return parts[1], action, nil
}

func (c *Client) SendReminder(ctx context.Context, chatID int64, text, grantID string) error {
	keyboard, err := ReminderKeyboard(grantID)
	if err != nil {
		return err
	}
	return c.call(ctx, "sendMessage", map[string]any{"chat_id": chatID, "text": text, "reply_markup": keyboard}, nil)
}

func (c *Client) AnswerCallback(ctx context.Context, id, text string) error {
	if len([]rune(text)) > 200 {
		text = string([]rune(text)[:197]) + "…"
	}
	return c.call(ctx, "answerCallbackQuery", map[string]any{"callback_query_id": id, "text": text}, nil)
}

func (c *Client) FinishReminderMessage(ctx context.Context, chatID, messageID int64, text string) error {
	return c.call(ctx, "editMessageText", map[string]any{"chat_id": chatID, "message_id": messageID, "text": text,
		"reply_markup": InlineKeyboard{Rows: [][]InlineButton{}}}, nil)
}
