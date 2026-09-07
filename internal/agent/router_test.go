package agent

import "testing"

func TestRouteRequest(t *testing.T) {
	models := DefaultModels()
	got := RouteRequest(Request{Text: "Нагадай завтра подзвонити Сергію"}, models)
	if got.Mode != "deterministic" || got.Intent != "reminder.create" {
		t.Fatalf("unexpected deterministic route: %+v", got)
	}

	got = RouteRequest(Request{Text: "Що ми вирішили по NOVA?"}, models)
	if got.Mode != "model" || got.Tier != TierSimple || got.Model != "gpt-5.6-luna" {
		t.Fatalf("unexpected default route: %+v", got)
	}

	got = RouteRequest(Request{Text: "critical", RequiresCriticalReasoning: true}, models)
	if got.Tier != TierStrong || got.Model != "gpt-5.6-sol" {
		t.Fatalf("unexpected escalation route: %+v", got)
	}
}
