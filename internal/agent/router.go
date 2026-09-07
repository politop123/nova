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
	case strings.HasPrefix(normalized, "нагадай"), strings.HasPrefix(normalized, "нагадати"):
		return "reminder.create"
	default:
		return ""
	}
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
