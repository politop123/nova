package config

import "testing"

func TestTelegramNotificationChatIDPrefersChatID(t *testing.T) {
	cfg := Config{TelegramAllowedUserID: "100", TelegramAllowedChatID: "200"}
	got, ok := cfg.TelegramNotificationChatID()
	if !ok || got != 200 {
		t.Fatalf("TelegramNotificationChatID() = %d, %t; want 200, true", got, ok)
	}
}

func TestTelegramNotificationChatIDFallsBackToUserID(t *testing.T) {
	cfg := Config{TelegramAllowedUserID: "100"}
	got, ok := cfg.TelegramNotificationChatID()
	if !ok || got != 100 {
		t.Fatalf("TelegramNotificationChatID() = %d, %t; want 100, true", got, ok)
	}
}

func TestTelegramNotificationChatIDRejectsInvalidValues(t *testing.T) {
	cfg := Config{TelegramAllowedUserID: "not-a-number", TelegramAllowedChatID: "also-bad"}
	got, ok := cfg.TelegramNotificationChatID()
	if ok || got != 0 {
		t.Fatalf("TelegramNotificationChatID() = %d, %t; want 0, false", got, ok)
	}
}
