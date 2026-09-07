package config

import (
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
)

type Config struct {
	Environment           string
	APIPort               string
	WebOrigin             string
	NovaUserID            string
	DatabaseURL           string
	RedisURL              string
	OpenAIAPIKey          string
	OpenAITextModel       string
	OpenAIMediumModel     string
	OpenAIStrongModel     string
	OpenAIEmbeddingModel  string
	OpenAITranscribeModel string
	Timezone              string
	DailyBudgetUSD        float64
	MonthlyBudgetUSD      float64
	TelegramEnabled       bool
	TelegramBotToken      string
	TelegramAllowedUserID string
	TelegramAllowedChatID string
	TelegramWebhookSecret string
}

func Load() (Config, error) {
	daily, err := floatEnv("DAILY_COST_BUDGET_USD", 2)
	if err != nil {
		return Config{}, err
	}
	monthly, err := floatEnv("MONTHLY_COST_BUDGET_USD", 30)
	if err != nil {
		return Config{}, err
	}
	telegram, err := boolEnv("TELEGRAM_ENABLED", false)
	if err != nil {
		return Config{}, err
	}
	return Config{
		Environment:           stringEnv("APP_ENV", stringEnv("NODE_ENV", "development")),
		APIPort:               stringEnv("API_PORT", "4000"),
		WebOrigin:             stringEnv("WEB_ORIGIN", "http://localhost:3000"),
		NovaUserID:            stringEnv("NOVA_USER_ID", "00000000-0000-0000-0000-000000000001"),
		DatabaseURL:           stringEnv("DATABASE_URL", "postgresql://nova:nova_local_only@localhost:5432/nova"),
		RedisURL:              stringEnv("REDIS_URL", "redis://localhost:6379"),
		OpenAIAPIKey:          os.Getenv("OPENAI_API_KEY"),
		OpenAITextModel:       stringEnv("OPENAI_TEXT_MODEL", "gpt-5.6-luna"),
		OpenAIMediumModel:     stringEnv("OPENAI_MEDIUM_MODEL", "gpt-5.6-terra"),
		OpenAIStrongModel:     stringEnv("OPENAI_STRONG_MODEL", "gpt-5.6-sol"),
		OpenAIEmbeddingModel:  stringEnv("OPENAI_EMBEDDING_MODEL", "text-embedding-3-small"),
		OpenAITranscribeModel: stringEnv("OPENAI_TRANSCRIBE_MODEL", "gpt-4o-mini-transcribe"),
		Timezone:              stringEnv("NOVA_TIMEZONE", "Europe/Kyiv"),
		DailyBudgetUSD:        daily,
		MonthlyBudgetUSD:      monthly,
		TelegramEnabled:       telegram,
		TelegramBotToken:      os.Getenv("TELEGRAM_BOT_TOKEN"),
		TelegramAllowedUserID: os.Getenv("TELEGRAM_ALLOWED_USER_ID"),
		TelegramAllowedChatID: os.Getenv("TELEGRAM_ALLOWED_CHAT_ID"),
		TelegramWebhookSecret: os.Getenv("TELEGRAM_WEBHOOK_SECRET"),
	}, nil
}

func (c Config) RedisAddress() (string, error) {
	u, err := url.Parse(c.RedisURL)
	if err != nil {
		return "", fmt.Errorf("parse redis URL: %w", err)
	}
	if u.Host == "" {
		return "", fmt.Errorf("redis URL has no host")
	}
	return u.Host, nil
}

func (c Config) TelegramNotificationChatID() (int64, bool) {
	for _, candidate := range []string{c.TelegramAllowedChatID, c.TelegramAllowedUserID} {
		value := strings.TrimSpace(candidate)
		if value == "" {
			continue
		}
		chatID, err := strconv.ParseInt(value, 10, 64)
		if err == nil {
			return chatID, true
		}
	}
	return 0, false
}

func stringEnv(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}

func floatEnv(name string, fallback float64) (float64, error) {
	value := stringEnv(name, strconv.FormatFloat(fallback, 'f', -1, 64))
	result, err := strconv.ParseFloat(value, 64)
	if err != nil {
		return 0, fmt.Errorf("%s must be a number: %w", name, err)
	}
	return result, nil
}

func boolEnv(name string, fallback bool) (bool, error) {
	value := stringEnv(name, strconv.FormatBool(fallback))
	result, err := strconv.ParseBool(value)
	if err != nil {
		return false, fmt.Errorf("%s must be true or false: %w", name, err)
	}
	return result, nil
}
