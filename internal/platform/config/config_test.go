package config

import "testing"

func TestLoadGitDefaults(t *testing.T) {
	t.Setenv("NOVA_GIT_REPOSITORY", "")
	t.Setenv("NOVA_GIT_BRANCH", "")
	t.Setenv("NOVA_GITHUB_TOKEN", "")
	t.Setenv("GITHUB_TOKEN", "")
	t.Setenv("NOVA_GITHUB_API_BASE", "")
	t.Setenv("NOVA_DEPLOYED_COMMIT_SHA", "")
	t.Setenv("IMAGE_TAG", "")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.GitRepository != "politop123/nova" || cfg.GitBranch != "dev" {
		t.Fatalf("unexpected git defaults: repository=%q branch=%q", cfg.GitRepository, cfg.GitBranch)
	}
	if cfg.GitHubAPIBase != "https://api.github.com" {
		t.Fatalf("GitHubAPIBase = %q, want default", cfg.GitHubAPIBase)
	}
	if cfg.GitDeployedCommitSHA != "" {
		t.Fatalf("GitDeployedCommitSHA = %q, want empty", cfg.GitDeployedCommitSHA)
	}
}

func TestLoadGitDeployedCommitPrefersExplicitValue(t *testing.T) {
	t.Setenv("NOVA_DEPLOYED_COMMIT_SHA", "explicit-sha")
	t.Setenv("IMAGE_TAG", "image-tag-sha")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.GitDeployedCommitSHA != "explicit-sha" {
		t.Fatalf("GitDeployedCommitSHA = %q, want explicit-sha", cfg.GitDeployedCommitSHA)
	}
}

func TestLoadGitDeployedCommitFallsBackToImageTag(t *testing.T) {
	t.Setenv("NOVA_DEPLOYED_COMMIT_SHA", "")
	t.Setenv("IMAGE_TAG", "image-tag-sha")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.GitDeployedCommitSHA != "image-tag-sha" {
		t.Fatalf("GitDeployedCommitSHA = %q, want image-tag-sha", cfg.GitDeployedCommitSHA)
	}
}

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
