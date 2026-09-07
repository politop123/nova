package httpserver

import (
	"testing"
	"time"

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
