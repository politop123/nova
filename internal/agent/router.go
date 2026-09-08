package agent

import "strings"

type ModelTier string

const (
	TierSimple ModelTier = "simple"
	TierMedium ModelTier = "medium"
	TierStrong ModelTier = "strong"
)

type ModelCatalog struct {
	Simple string
	Medium string
	Strong string
}

type Request struct {
	Text                      string
	Complexity                ModelTier
	RequiresCriticalReasoning bool
}

type Route struct {
	Mode   string
	Intent string
	Tier   ModelTier
	Model  string
}

func DefaultModels() ModelCatalog {
	return ModelCatalog{
		Simple: "gpt-5.6-luna",
		Medium: "gpt-5.6-terra",
		Strong: "gpt-5.6-sol",
	}
}

func DetectDeterministicIntent(text string) string {
	normalized := strings.ToLower(strings.TrimSpace(text))
	switch {
	case normalized == "стоп" || normalized == "зупинись" || normalized == "stop":
		return "interaction.stop"
	case normalized == "так" || normalized == "підтверджую" || normalized == "виконуй" || normalized == "yes":
		return "confirmation.approve"
	case normalized == "ні" || normalized == "скасувати" || normalized == "не роби" || normalized == "no":
		return "confirmation.reject"
	case hasGitStatusRequest(normalized):
		return "git.status"
	case hasSystemStatusRequest(normalized):
		return "system.status"
	case hasReminderRequest(normalized):
		return "reminder.create"
	default:
		return ""
	}
}

func hasReminderRequest(normalized string) bool {
	if strings.HasPrefix(normalized, "нагадай") || strings.HasPrefix(normalized, "нагадати") {
		return true
	}
	return strings.Contains(normalized, " нагадай") || strings.Contains(normalized, " нагадати")
}

func hasGitStatusRequest(normalized string) bool {
	hasOperationalTarget := containsAny(normalized, []string{
		"git", "github", "гіт", "ґіт", "репозитор", "repo",
		"коміт", "комит", "commit",
		"деплой", "депло", "задепло", "deploy", "deployment",
		"workflow", "actions", "production",
	}) || hasProdToken(normalized)
	if !hasOperationalTarget {
		return false
	}
	return containsAny(normalized, []string{
		"остан", "який", "яка", "що там", "статус", "стан",
		"задепло", "депло", "deploy", "workflow", "actions",
	}) || hasProdToken(normalized)
}

func hasSystemStatusRequest(normalized string) bool {
	if normalized == "/status" || normalized == "status" || normalized == "health" {
		return true
	}
	if strings.Contains(normalized, "що з тобою") ||
		strings.Contains(normalized, "як ти працюєш") ||
		strings.Contains(normalized, "чи все працює") ||
		strings.Contains(normalized, "ти жива") ||
		strings.Contains(normalized, "ти живий") {
		return true
	}
	hasStatusWord := containsAny(normalized, []string{"статус", "стан", "health", "здоров"})
	hasNOVAContext := containsAny(normalized, []string{"nova", "нова", "core", "кор", "систем", "сервер", "бек", "backend"})
	return hasStatusWord && hasNOVAContext
}

func containsAny(value string, needles []string) bool {
	for _, needle := range needles {
		if strings.Contains(value, needle) {
			return true
		}
	}
	return false
}

func hasProdToken(normalized string) bool {
	fields := strings.FieldsFunc(normalized, func(r rune) bool {
		return r == ' ' || r == '\t' || r == '\n' || r == '\r' ||
			r == ',' || r == '.' || r == '?' || r == '!' || r == ':' || r == ';' ||
			r == '(' || r == ')' || r == '[' || r == ']'
	})
	for _, field := range fields {
		if field == "прод" || field == "prod" || strings.HasPrefix(field, "продакш") {
			return true
		}
	}
	return false
}

func RouteRequest(request Request, models ModelCatalog) Route {
	if intent := DetectDeterministicIntent(request.Text); intent != "" {
		return Route{Mode: "deterministic", Intent: intent}
	}

	tier := request.Complexity
	if tier == "" {
		tier = TierSimple
	}
	if request.RequiresCriticalReasoning {
		tier = TierStrong
	}

	model := models.Simple
	switch tier {
	case TierMedium:
		model = models.Medium
	case TierStrong:
		model = models.Strong
	}
	return Route{Mode: "model", Tier: tier, Model: model}
}
