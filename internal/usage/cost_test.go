package usage

import "testing"

func TestEstimateCostUsesCachedInputPrice(t *testing.T) {
	cost := EstimateCost("gpt-5.6-luna", 1_000_000, 500_000, 1_000_000)
	want := 0.5*0.20 + 0.5*0.02 + 1.20
	if cost != want {
		t.Fatalf("cost = %f, want %f", cost, want)
	}
}

func TestCheckBudgetStopsWhenDailyLimitWouldBeExceeded(t *testing.T) {
	decision := CheckBudget(BudgetLimits{DailyUSD: 1, MonthlyUSD: 10}, BudgetSnapshot{
		DailyUsedUSD: 0.9, MonthlyUsedUSD: 0.9, EstimatedUSD: 0.2,
	})
	if !decision.Exceeded || decision.Period != "daily" {
		t.Fatalf("decision = %+v, want daily denial", decision)
	}
}

func TestCheckBudgetStopsWhenMonthlyLimitWouldBeExceeded(t *testing.T) {
	decision := CheckBudget(BudgetLimits{DailyUSD: 0, MonthlyUSD: 1}, BudgetSnapshot{
		MonthlyUsedUSD: 0.9, EstimatedUSD: 0.2,
	})
	if !decision.Exceeded || decision.Period != "monthly" {
		t.Fatalf("decision = %+v, want monthly denial", decision)
	}
}

func TestCheckBudgetAllowsWithinLimits(t *testing.T) {
	decision := CheckBudget(BudgetLimits{DailyUSD: 1, MonthlyUSD: 10}, BudgetSnapshot{
		DailyUsedUSD: 0.5, MonthlyUsedUSD: 0.5, EstimatedUSD: 0.2,
	})
	if decision.Exceeded {
		t.Fatalf("decision = %+v, want allowed", decision)
	}
}
