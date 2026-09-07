package httpserver

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"nova.local/core/internal/core"
	"nova.local/core/internal/integrations/telegram"
	"nova.local/core/internal/platform/config"
)

func TestParseOptionalZonedTimeLocalKyiv(t *testing.T) {
	got, err := parseOptionalZonedTime("2026-09-08T09:30", "Europe/Kyiv", "triggerAt")
	if err != nil {
		t.Fatalf("parse local time: %v", err)
	}
	want := time.Date(2026, 9, 8, 6, 30, 0, 0, time.UTC)
	if !got.Equal(want) {
		t.Fatalf("got %s, want %s", got.Format(time.RFC3339), want.Format(time.RFC3339))
	}
}

func TestParseOptionalZonedTimeRFC3339(t *testing.T) {
	got, err := parseOptionalZonedTime("2026-09-08T09:30:00+03:00", "Europe/Kyiv", "triggerAt")
	if err != nil {
		t.Fatalf("parse RFC3339 time: %v", err)
	}
	want := time.Date(2026, 9, 8, 6, 30, 0, 0, time.UTC)
	if !got.Equal(want) {
		t.Fatalf("got %s, want %s", got.Format(time.RFC3339), want.Format(time.RFC3339))
	}
}

func TestBuildTaskRecordRequiresTitle(t *testing.T) {
	server := Server{cfg: config.Config{Timezone: "Europe/Kyiv"}}
	_, err := server.buildTaskRecord("00000000-0000-0000-0000-000000000001", createTaskRequest{})
	if err == nil {
		t.Fatal("expected title validation error")
	}
}

func TestBuildReminderRecordDefaults(t *testing.T) {
	server := Server{cfg: config.Config{Timezone: "Europe/Kyiv"}}
	got, err := server.buildReminderRecord("00000000-0000-0000-0000-000000000001", createReminderRequest{
		Title:     "Підготувати звіт",
		TriggerAt: "2026-09-08T09:30",
	})
	if err != nil {
		t.Fatalf("build reminder record: %v", err)
	}
	if got.Timezone != "Europe/Kyiv" || got.Priority != "normal" || got.DeliveryMethod != "web" || got.Status != "scheduled" {
		t.Fatalf("unexpected defaults: %#v", got)
	}
}

func TestTelegramMessageAllowedChecksUserAndChat(t *testing.T) {
	tests := []struct {
		name    string
		cfg     config.Config
		message telegram.Message
		want    bool
	}{
		{
			name: "open when no allowlist is configured",
			cfg:  config.Config{},
			message: telegram.Message{
				From: &telegram.User{ID: 100},
				Chat: telegram.Chat{ID: 200},
			},
			want: true,
		},
		{
			name: "allows matching user id",
			cfg:  config.Config{TelegramAllowedUserID: "100"},
			message: telegram.Message{
				From: &telegram.User{ID: 100},
				Chat: telegram.Chat{ID: 200},
			},
			want: true,
		},
		{
			name: "allows matching private chat id",
			cfg:  config.Config{TelegramAllowedChatID: "200"},
			message: telegram.Message{
				From: &telegram.User{ID: 100},
				Chat: telegram.Chat{ID: 200},
			},
			want: true,
		},
		{
			name: "rejects unknown user and chat",
			cfg:  config.Config{TelegramAllowedUserID: "101", TelegramAllowedChatID: "201"},
			message: telegram.Message{
				From: &telegram.User{ID: 100},
				Chat: telegram.Chat{ID: 200},
			},
			want: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := Server{cfg: tt.cfg}
			if got := server.telegramMessageAllowed(tt.message); got != tt.want {
				t.Fatalf("telegramMessageAllowed() = %t, want %t", got, tt.want)
			}
		})
	}
}

func TestDecodeTelegramUpdateAllowsUnknownTelegramFields(t *testing.T) {
	request := httptest.NewRequest(http.MethodPost, "/api/v1/telegram/webhook", strings.NewReader(`{
		"update_id": 9001,
		"message": {
			"message_id": 42,
			"message_thread_id": 7,
			"from": {
				"id": 100,
				"is_bot": false,
				"first_name": "Ілля",
				"language_code": "uk",
				"is_premium": true
			},
			"chat": {
				"id": 200,
				"first_name": "Ілля",
				"type": "private"
			},
			"date": 1788810000,
			"text": "Привіт",
			"entities": [
				{ "offset": 0, "length": 6, "type": "bold" }
			]
		}
	}`))
	response := httptest.NewRecorder()
	var update telegram.Update

	if err := decodeTelegramUpdate(response, request, &update); err != nil {
		t.Fatalf("decode telegram update: %v", err)
	}
	if update.UpdateID != 9001 || update.Message == nil || update.Message.Text != "Привіт" {
		t.Fatalf("unexpected update: %#v", update)
	}
	if update.Message.From == nil || update.Message.From.ID != 100 || update.Message.Chat.ID != 200 {
		t.Fatalf("unexpected telegram sender: %#v", update.Message)
	}
}

func TestParseReminderCommandRelativeMinutes(t *testing.T) {
	now := time.Date(2026, 9, 7, 20, 15, 0, 0, time.UTC)
	got, err := parseReminderCommand("Ти можеш мені нагадати через 2 хвилини, щоб я перевірив як ти працюєш?", "Europe/Kyiv", now)
	if err != nil {
		t.Fatalf("parse reminder command: %v", err)
	}
	want := now.Add(2 * time.Minute)
	if !got.TriggerAt.Equal(want) {
		t.Fatalf("triggerAt = %s, want %s", got.TriggerAt, want)
	}
	if !strings.Contains(strings.ToLower(got.Title), "перевірив") {
		t.Fatalf("title does not preserve reminder subject: %q", got.Title)
	}
}

func TestParseReminderCommandDoesNotTreatProjectAsAboutMarker(t *testing.T) {
	got, err := parseReminderCommand("Нагадай через 5 хвилин перевірити проект NOVA", "Europe/Kyiv", time.Now())
	if err != nil {
		t.Fatalf("parse reminder command: %v", err)
	}
	if !strings.Contains(strings.ToLower(got.Title), "проект nova") {
		t.Fatalf("title was cut unexpectedly: %q", got.Title)
	}
}

func TestParseReminderCommandRequiresRelativeTime(t *testing.T) {
	_, err := parseReminderCommand("Нагадай перевірити NOVA", "Europe/Kyiv", time.Now())
	if err == nil {
		t.Fatal("expected a validation error")
	}
}

func TestPlannerActionHandlingDecisions(t *testing.T) {
	if !planNeedsHandling(core.ActionPlan{Intent: core.IntentReminderCreate}) {
		t.Fatal("expected reminder intent to be handled even without actions")
	}
	if planNeedsHandling(core.ActionPlan{Intent: core.IntentReply}) {
		t.Fatal("expected plain reply intent to continue to chat")
	}
	if got := clarificationForIntent(core.IntentReminderCreate); !strings.Contains(got, "Коли") {
		t.Fatalf("unexpected reminder clarification: %q", got)
	}
}

func TestDefaultReminderDeliveryMethodPrefersReadyTelegram(t *testing.T) {
	server := Server{cfg: config.Config{
		TelegramEnabled:       true,
		TelegramBotToken:      "configured",
		TelegramAllowedChatID: "200",
	}}
	if got := server.defaultReminderDeliveryMethod(core.ChannelWeb); got != "telegram" {
		t.Fatalf("delivery method = %q, want telegram", got)
	}
}

func TestDefaultReminderDeliveryMethodFallsBackToWebWithoutTelegramToken(t *testing.T) {
	server := Server{cfg: config.Config{
		TelegramEnabled:       true,
		TelegramAllowedChatID: "200",
	}}
	if got := server.defaultReminderDeliveryMethod(core.ChannelWeb); got != "web" {
		t.Fatalf("delivery method = %q, want web", got)
	}
}
