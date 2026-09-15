package telegram

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"nova.local/core/internal/core"
)

const testGrantID = "00000000-0000-0000-0000-000000000987"

func TestReminderKeyboardRoundTrip(t *testing.T) {
	keyboard, err := ReminderKeyboard(testGrantID)
	if err != nil {
		t.Fatal(err)
	}
	var actions []core.ReminderQuickAction
	for _, row := range keyboard.Rows {
		for _, button := range row {
			id, action, err := ParseReminderCallback(button.Data)
			if err != nil || id != testGrantID || len(button.Data) > 64 || button.Text == "" {
				t.Fatalf("bad button: %+v %v", button, err)
			}
			actions = append(actions, action)
		}
	}
	if len(actions) != 3 || actions[0] != core.ReminderComplete || actions[1] != core.ReminderSnooze10 || actions[2] != core.ReminderSnooze60 {
		t.Fatalf("unexpected buttons: %v", actions)
	}
	if _, err = ReminderKeyboard("bad"); err == nil {
		t.Fatal("invalid grant accepted")
	}
	for _, data := range []string{"", "r:bad:done", "other:" + testGrantID + ":done", "r:" + testGrantID + ":delete", "r:" + testGrantID + ":done:extra", strings.Repeat("a", 65)} {
		if _, _, err = ParseReminderCallback(data); err == nil {
			t.Fatalf("accepted %q", data)
		}
	}
}

func TestReminderControlAPIRequests(t *testing.T) {
	var methods []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.Header.Get("Content-Type") != "application/json" {
			t.Error("expected JSON POST")
		}
		var payload struct {
			ChatID     int64          `json:"chat_id"`
			MessageID  int64          `json:"message_id"`
			CallbackID string         `json:"callback_query_id"`
			Text       string         `json:"text"`
			Keyboard   InlineKeyboard `json:"reply_markup"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Error(err)
		}
		method := strings.TrimPrefix(r.URL.Path, "/bottest-token/")
		methods = append(methods, method)
		switch method {
		case "sendMessage":
			if payload.ChatID != 42 || len(payload.Keyboard.Rows) != 2 || payload.Text != "Нагадування" {
				t.Errorf("send payload=%+v", payload)
			}
		case "answerCallbackQuery":
			if payload.CallbackID != "query" || len([]rune(payload.Text)) > 200 || payload.Text == "" {
				t.Errorf("answer payload=%+v", payload)
			}
		case "editMessageText":
			if payload.ChatID != 42 || payload.MessageID != 7 || payload.Keyboard.Rows == nil || len(payload.Keyboard.Rows) != 0 || payload.Text != "Виконано" {
				t.Errorf("edit payload=%+v", payload)
			}
		default:
			t.Errorf("unexpected method=%s", method)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true,"result":true}`))
	}))
	defer server.Close()
	client, err := NewClient("test-token", WithBaseURLs(server.URL, server.URL))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err = client.SendReminder(ctx, 42, "Нагадування", testGrantID); err != nil {
		t.Fatal(err)
	}
	if err = client.AnswerCallback(ctx, "query", strings.Repeat("ї", 220)); err != nil {
		t.Fatal(err)
	}
	if err = client.FinishReminderMessage(ctx, 42, 7, "Виконано"); err != nil {
		t.Fatal(err)
	}
	if len(methods) != 3 {
		t.Fatalf("requests=%v", methods)
	}
}
