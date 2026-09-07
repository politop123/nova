package usage

import (
	"math"
	"strings"
	"unicode/utf8"

	"nova.local/core/internal/core"
)

// ModelPricing stores USD prices per one million tokens. Cached input is
// priced separately because prompt caching is a first-class cost signal.
type ModelPricing struct {
	InputPerMillion       float64
	CachedInputPerMillion float64
	OutputPerMillion      float64
}

var modelPrices = map[string]ModelPricing{
	"gpt-5.6-luna":  {InputPerMillion: 0.20, CachedInputPerMillion: 0.02, OutputPerMillion: 1.20},
	"gpt-5.6-terra": {InputPerMillion: 2.00, CachedInputPerMillion: 0.20, OutputPerMillion: 12.00},
	"gpt-5.6-sol":   {InputPerMillion: 4.00, CachedInputPerMillion: 0.40, OutputPerMillion: 20.00},
}

// PricingForModel returns the configured price for a model. Unknown models
// use the strongest configured price so a new model cannot bypass guardrails.
func PricingForModel(model string) ModelPricing {
	key := strings.ToLower(strings.TrimSpace(model))
	if pricing, ok := modelPrices[key]; ok {
		return pricing
	}
	return modelPrices["gpt-5.6-sol"]
}

func EstimateCost(model string, inputTokens, cachedInputTokens, outputTokens int) float64 {
	inputTokens = maxInt(inputTokens, 0)
	cachedInputTokens = minInt(maxInt(cachedInputTokens, 0), inputTokens)
	outputTokens = maxInt(outputTokens, 0)
	pricing := PricingForModel(model)
	input := float64(inputTokens-cachedInputTokens) / 1_000_000 * pricing.InputPerMillion
	cached := float64(cachedInputTokens) / 1_000_000 * pricing.CachedInputPerMillion
	output := float64(outputTokens) / 1_000_000 * pricing.OutputPerMillion
	return input + cached + output
}

// EstimateTokens intentionally errs high enough for preflight checks. The
// provider's usage is still recorded after the response and is authoritative.
func EstimateTokens(text string) int {
	if strings.TrimSpace(text) == "" {
		return 0
	}
	byteCount := len(text)
	if !utf8.ValidString(text) {
		byteCount = len([]rune(text)) * 4
	}
	return maxInt(1, int(math.Ceil(float64(byteCount)/4)))
}

func EstimateResponse(model, input, output string) core.NovaUsage {
	inputTokens := EstimateTokens(input)
	outputTokens := EstimateTokens(output)
	return core.NovaUsage{
		Model:            model,
		InputTokens:      inputTokens,
		OutputTokens:     outputTokens,
		EstimatedCostUSD: EstimateCost(model, inputTokens, 0, outputTokens),
	}
}

func EstimateRequest(model, input string, maxOutputTokens int) core.NovaUsage {
	inputTokens := EstimateTokens(input)
	maxOutputTokens = maxInt(maxOutputTokens, 0)
	return core.NovaUsage{
		Model:            model,
		InputTokens:      inputTokens,
		OutputTokens:     maxOutputTokens,
		EstimatedCostUSD: EstimateCost(model, inputTokens, 0, maxOutputTokens),
	}
}

type BudgetLimits struct {
	DailyUSD   float64
	MonthlyUSD float64
}

type BudgetSnapshot struct {
	DailyUsedUSD   float64
	MonthlyUsedUSD float64
	EstimatedUSD   float64
}

type BudgetDecision struct {
	Exceeded     bool
	Period       string
	LimitUSD     float64
	UsedUSD      float64
	EstimatedUSD float64
	RemainingUSD float64
}

func CheckBudget(limits BudgetLimits, snapshot BudgetSnapshot) BudgetDecision {
	if limits.DailyUSD > 0 && snapshot.DailyUsedUSD+snapshot.EstimatedUSD > limits.DailyUSD {
		return BudgetDecision{
			Exceeded:     true,
			Period:       "daily",
			LimitUSD:     limits.DailyUSD,
			UsedUSD:      snapshot.DailyUsedUSD,
			EstimatedUSD: snapshot.EstimatedUSD,
			RemainingUSD: maxFloat(limits.DailyUSD-snapshot.DailyUsedUSD, 0),
		}
	}
	if limits.MonthlyUSD > 0 && snapshot.MonthlyUsedUSD+snapshot.EstimatedUSD > limits.MonthlyUSD {
		return BudgetDecision{
			Exceeded:     true,
			Period:       "monthly",
			LimitUSD:     limits.MonthlyUSD,
			UsedUSD:      snapshot.MonthlyUsedUSD,
			EstimatedUSD: snapshot.EstimatedUSD,
			RemainingUSD: maxFloat(limits.MonthlyUSD-snapshot.MonthlyUsedUSD, 0),
		}
	}
	return BudgetDecision{Exceeded: false, RemainingUSD: maxFloat(minPositive(limits.DailyUSD-snapshot.DailyUsedUSD, limits.MonthlyUSD-snapshot.MonthlyUsedUSD), 0)}
}

func maxInt(left, right int) int {
	if left > right {
		return left
	}
	return right
}

func minInt(left, right int) int {
	if left < right {
		return left
	}
	return right
}

func maxFloat(left, right float64) float64 {
	if left > right {
		return left
	}
	return right
}

func minPositive(left, right float64) float64 {
	if left <= 0 {
		return right
	}
	if right <= 0 {
		return left
	}
	if left < right {
		return left
	}
	return right
}
