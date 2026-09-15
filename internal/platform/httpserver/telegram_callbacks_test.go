package httpserver

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"nova.local/core/internal/integrations/telegram"
	"nova.local/core/internal/platform/config"
	"nova.local/core/internal/storage"
)

func testReminderCallback() telegram.CallbackQuery {
	return telegram.CallbackQuery{ID: "query-1", From: &telegram.User{ID: 42},
		Message: &telegram.Message{MessageID: 7, Date: time.Now().Unix(), Chat: telegram.Chat{ID: 42, Type: "private"}}}
}

func TestReminderCallbackAuthorization(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*config.Config, *telegram.CallbackQuery)
		want   bool
	}{
		{"owner", func(*config.Config, *telegram.CallbackQuery) {}, true},
		{"chat only", func(c *config.Config, _ *telegram.CallbackQuery) { c.TelegramAllowedUserID = "" }, true},
		{"user only", func(c *config.Config, _ *telegram.CallbackQuery) { c.TelegramAllowedChatID = "" }, true},
		{"no allowlist", func(c *config.Config, _ *telegram.CallbackQuery) {
			c.TelegramAllowedChatID = ""
			c.TelegramAllowedUserID = ""
		}, false},
		{"no secret", func(c *config.Config, _ *telegram.CallbackQuery) { c.TelegramWebhookSecret = " " }, false},
		{"wrong user", func(c *config.Config, _ *telegram.CallbackQuery) { c.TelegramAllowedUserID = "43" }, false},
		{"wrong chat", func(c *config.Config, _ *telegram.CallbackQuery) { c.TelegramAllowedChatID = "43" }, false},
		{"bot", func(_ *config.Config, q *telegram.CallbackQuery) { q.From.IsBot = true }, false},
		{"no sender", func(_ *config.Config, q *telegram.CallbackQuery) { q.From = nil }, false},
		{"inline", func(_ *config.Config, q *telegram.CallbackQuery) { q.Message = nil }, false},
		{"group", func(_ *config.Config, q *telegram.CallbackQuery) {
			q.Message.Chat.Type = "group"
			q.Message.Chat.ID = -42
		}, false},
		{"foreign chat", func(_ *config.Config, q *telegram.CallbackQuery) { q.Message.Chat.ID = 43 }, false},
		{"inaccessible", func(_ *config.Config, q *telegram.CallbackQuery) { q.Message.Date = 0 }, false},
		{"bad message", func(_ *config.Config, q *telegram.CallbackQuery) { q.Message.MessageID = 0 }, false},
		{"no query id", func(_ *config.Config, q *telegram.CallbackQuery) { q.ID = "" }, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := config.Config{TelegramWebhookSecret: "test-secret", TelegramAllowedUserID: "42", TelegramAllowedChatID: "42"}
			query := testReminderCallback()
			tc.change(&cfg, &query)
			if got := (&Server{cfg: cfg}).telegramReminderCallbackAllowed(query); got != tc.want {
				t.Fatalf("allowed=%v want=%v", got, tc.want)
			}
		})
	}
}

type callbackTelegramFake struct {
	answers, edits []string
	failUI         bool
}

func (f *callbackTelegramFake) SendMessage(context.Context, int64, string) error {
	return errors.New("unexpected message/model path")
}
func (f *callbackTelegramFake) DownloadVoice(context.Context, string) (telegram.VoiceDownload, error) {
	return telegram.VoiceDownload{}, errors.New("unexpected voice path")
}
func (f *callbackTelegramFake) AnswerCallback(_ context.Context, _ string, text string) error {
	f.answers = append(f.answers, text)
	if f.failUI {
		return errors.New("transport failure")
	}
	return nil
}
func (f *callbackTelegramFake) FinishReminderMessage(_ context.Context, chatID, messageID int64, text string) error {
	if chatID != 42 || messageID != 7 {
		return errors.New("wrong message target")
	}
	f.edits = append(f.edits, text)
	if f.failUI {
		return errors.New("transport failure")
	}
	return nil
}

func TestTelegramReminderCallbackWebhookIntegration(t *testing.T) {
	url := os.Getenv("NOVA_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("disposable database is not configured")
	}
	ctx := context.Background()
	db, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, path := range []string{"../../../infrastructure/postgres/init.sql",
		"../../../infrastructure/postgres/migrations/202609150001_reminder_dispatches.sql",
		"../../../infrastructure/postgres/migrations/202609150002_reminder_action_grants.sql"} {
		sql, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = db.Exec(ctx, string(sql)); err != nil {
			t.Fatal(err)
		}
	}
	var userID string
	if err = db.QueryRow(ctx, "INSERT INTO users DEFAULT VALUES RETURNING id::text").Scan(&userID); err != nil {
		t.Fatal(err)
	}
	defer db.Exec(ctx, "DELETE FROM users WHERE id=$1::uuid", userID)
	store := storage.New(db)
	conversation, err := store.CreateConversation(ctx, userID, "Web conversation")
	if err != nil {
		t.Fatal(err)
	}
	transport := &callbackTelegramFake{}
	cfg := config.Config{TelegramEnabled: true, TelegramWebhookSecret: "test-secret", TelegramAllowedUserID: "42", TelegramAllowedChatID: "42"}
	s := New(cfg, db, nil, Dependencies{Store: store, Telegram: transport, UserID: userID})
	post := func(query telegram.CallbackQuery, secret string) int {
		t.Helper()
		// Extra fields are normal Telegram API evolution, not invalid input.
		payload, err := json.Marshal(map[string]any{"update_id": 1, "callback_query": query, "extra_field": "ignored"})
		if err != nil {
			t.Fatal(err)
		}
		req := httptest.NewRequest(http.MethodPost, "/api/v1/telegram/webhook", bytes.NewReader(payload))
		req.Header.Set("X-Telegram-Bot-Api-Secret-Token", secret)
		rec := httptest.NewRecorder()
		s.Handler().ServeHTTP(rec, req)
		return rec.Code
	}
	r, err := store.CreateReminder(ctx, storage.Reminder{UserID: userID, Title: "Перевірити NOVA", TriggerAt: time.Now().Add(-time.Minute), Timezone: "Europe/Kyiv", Status: "scheduled", Priority: "normal", DeliveryMethod: "telegram"}, "")
	if err != nil {
		t.Fatal(err)
	}
	grant, err := store.CreateReminderActionGrant(ctx, r, "telegram", "42")
	if err != nil {
		t.Fatal(err)
	}
	query := testReminderCallback()
	query.Data = "r:" + grant + ":s10"
	if code := post(query, "wrong"); code != http.StatusUnauthorized || len(transport.answers) != 0 {
		t.Fatalf("invalid secret: %d", code)
	}
	query.From.ID = 43
	if code := post(query, "test-secret"); code != http.StatusNoContent || len(transport.edits) != 0 {
		t.Fatalf("foreign user: %d", code)
	}
	query.From.ID = 42
	validData := query.Data
	query.Data = "r:invalid:s10"
	if code := post(query, "test-secret"); code != http.StatusNoContent || len(transport.edits) != 0 {
		t.Fatalf("malformed data: %d", code)
	}
	query.Data = validData
	if code := post(query, "test-secret"); code != http.StatusNoContent {
		t.Fatalf("callback failed: %d", code)
	}
	if len(transport.edits) != 1 || !strings.Contains(transport.edits[0], "Відклала") {
		t.Fatalf("missing inline UI result: %v", transport.edits)
	}
	current, err := store.GetReminder(ctx, userID, r.ID)
	if err != nil || current.Status != "scheduled" || time.Until(current.TriggerAt) < 9*time.Minute {
		t.Fatalf("not snoozed: %+v %v", current, err)
	}
	if durable, err := store.ReminderDispatchDurable(ctx, current); err != nil || !durable {
		t.Fatalf("no durable snooze: %v %v", durable, err)
	}
	messages, err := store.ListMessages(ctx, userID, conversation.ID, 100)
	if err != nil || len(messages) != 2 || messages[1].Channel != "telegram" || messages[1].Content != transport.edits[0] {
		t.Fatalf("not visible in shared chat: %v %v", messages, err)
	}
	// Retry a different button and fail both provider UI calls. Neither may
	// change the committed result or duplicate audit/messages.
	transport.failUI = true
	query.ID = "query-2"
	query.Data = "r:" + grant + ":done"
	if code := post(query, "test-secret"); code != http.StatusNoContent {
		t.Fatalf("committed action retried after UI failure: %d", code)
	}
	messages, err = store.ListMessages(ctx, userID, conversation.ID, 100)
	if err != nil || len(messages) != 2 || !strings.Contains(transport.answers[len(transport.answers)-1], "вже виконано") {
		t.Fatalf("replayed action: %v %v", messages, err)
	}
	current, err = store.GetReminder(ctx, userID, r.ID)
	if err != nil || current.Status != "scheduled" {
		t.Fatalf("duplicate button changed status: %+v %v", current, err)
	}
	if !validReminderStatus("completed") {
		t.Fatal("completed status missing from API validation")
	}
}
