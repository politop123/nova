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
	"nova.local/core/internal/agent"
	"nova.local/core/internal/core"
	"nova.local/core/internal/integrations/telegram"
	"nova.local/core/internal/platform/config"
	"nova.local/core/internal/storage"
)

type taskPlannerFake struct {
	plan  core.ActionPlan
	input agent.PlannerInput
}

func (f *taskPlannerFake) PlanActions(_ context.Context, _, _, input string) (core.ActionPlanResponse, error) {
	if err := json.Unmarshal([]byte(input), &f.input); err != nil {
		return core.ActionPlanResponse{}, err
	}
	return core.ActionPlanResponse{Plan: f.plan}, nil
}

type taskTelegramFake struct{ messages []string }

func (f *taskTelegramFake) SendMessage(_ context.Context, _ int64, text string) error {
	f.messages = append(f.messages, text)
	return nil
}
func (f *taskTelegramFake) DownloadVoice(context.Context, string) (telegram.VoiceDownload, error) {
	return telegram.VoiceDownload{}, errors.New("voice not used")
}

// Tests the full channel -> planner contract -> validated write -> shared history
// path with a fake model/provider. It does not claim to evaluate live model NLP.
func TestTaskChatIntegration(t *testing.T) {
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
	sql, err := os.ReadFile("../../../infrastructure/postgres/init.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(ctx, string(sql)); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, channel, action, status string
		confidence                    float64
		noActions                     bool
	}{
		{"web completion", "web", core.ActionTaskComplete, "done", 1, false},
		{"SSE deadline change", "stream", core.ActionTaskReschedule, "open", 1, false},
		{"Telegram cancellation", "telegram", core.ActionTaskCancel, "cancelled", 1, false},
		{"low confidence", "web", core.ActionTaskComplete, "open", 0.2, false},
		{"clarification cannot claim success", "web", core.ActionTaskComplete, "open", 1, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var userID string
			if err := db.QueryRow(ctx, "INSERT INTO users DEFAULT VALUES RETURNING id::text").Scan(&userID); err != nil {
				t.Fatal(err)
			}
			defer db.Exec(ctx, "DELETE FROM users WHERE id=$1::uuid", userID)
			store := storage.New(db)
			conversation, err := store.CreateConversation(ctx, userID, "Shared tasks")
			if err != nil {
				t.Fatal(err)
			}
			task, err := store.CreateTask(ctx, storage.Task{UserID: userID, Title: "Оплатити рахунок", Status: "open"}, "")
			if err != nil {
				t.Fatal(err)
			}
			deadline := time.Now().Add(2 * time.Hour).UTC().Truncate(time.Second)
			plan := core.ActionPlan{Intent: tc.action, Confidence: tc.confidence, Reply: "Готово, усе виконано!", Actions: []core.NovaAction{{Type: tc.action, Title: task.Title, DueAt: deadline.Format(time.RFC3339), Timezone: "Europe/Kyiv"}}}
			if tc.noActions {
				plan.Actions = nil
			}
			planner := &taskPlannerFake{plan: plan}
			transport := &taskTelegramFake{}
			s := New(config.Config{Timezone: "Europe/Kyiv", OpenAIPlannerModel: "gpt-5.6-luna", TelegramEnabled: true, TelegramWebhookSecret: "test-secret", TelegramAllowedUserID: "42"}, db, nil,
				Dependencies{Store: store, Planner: planner, Telegram: transport, UserID: userID})
			path := "/api/v1/conversations/" + conversation.ID + "/messages"
			payload := []byte(`{"text":"Зміни мою задачу про рахунок"}`)
			if tc.channel == "stream" {
				path += "/stream"
			}
			if tc.channel == "telegram" {
				path = "/api/v1/telegram/webhook"
				payload = []byte(`{"update_id":1,"message":{"message_id":7,"from":{"id":42},"chat":{"id":42,"type":"private"},"text":"Скасуй задачу про рахунок"}}`)
			}
			req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(payload))
			req.Header.Set("X-Telegram-Bot-Api-Secret-Token", "test-secret")
			rec := httptest.NewRecorder()
			s.Handler().ServeHTTP(rec, req)
			if rec.Code < 200 || rec.Code >= 300 {
				t.Fatalf("response=%d %s", rec.Code, rec.Body.String())
			}
			current, err := store.GetTask(ctx, userID, task.ID)
			if err != nil || current.Status != tc.status {
				t.Fatalf("state=%+v %v", current, err)
			}
			mutated := tc.confidence >= plannerMinimumActionConfidence && !tc.noActions
			if mutated && tc.action == core.ActionTaskReschedule && (current.DueAt == nil || !current.DueAt.Equal(deadline)) {
				t.Fatal("new deadline not saved")
			}
			var audits int
			if err := db.QueryRow(ctx, "SELECT count(*) FROM agent_actions WHERE user_id=$1::uuid AND status='EXECUTED'", userID).Scan(&audits); err != nil || (audits == 1) != mutated {
				t.Fatalf("audit=%d %v", audits, err)
			}
			messages, err := store.ListMessages(ctx, userID, conversation.ID, 100)
			if err != nil || len(messages) != 2 || strings.Contains(messages[1].Content, "усе виконано") {
				t.Fatalf("unverified model success leaked: %v %v", messages, err)
			}
			if tc.channel == "telegram" && (len(transport.messages) != 1 || transport.messages[0] != messages[1].Content || messages[1].Channel != "telegram") {
				t.Fatal("Telegram and shared history differ")
			}
			if tc.channel != "telegram" && mutated && !strings.Contains(rec.Body.String(), `"refreshTasks":true`) {
				t.Fatal("Web was not instructed to refresh tasks")
			}
			if len(planner.input.OpenTasks) != 1 || planner.input.OpenTasks[0].Title != task.Title {
				t.Fatal("planner missing real task preview")
			}
		})
	}
}
