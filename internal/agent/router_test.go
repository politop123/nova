package agent

import "testing"

func TestReminderManagementRoutes(t *testing.T) {
	for text, want := range map[string]string{
		"Скасуй нагадування про деплой":                         "reminder.cancel",
		"Перенеси нагадування перевірити статус NOVA на завтра": "reminder.reschedule",
		"Що в мене сьогодні?":                                   "agenda.list", "Що у мене завтра?": "agenda.list",
		"Мої нагадування": "agenda.list", "Які плани на завтра?": "agenda.list",
		"Скасуй нагадування про паспорт":             "reminder.cancel",
		"Перенеси нагадування про паспорт на завтра": "reminder.reschedule",
		"Нагадай завтра купити хліб":                 "reminder.create",
		"Познач задачу про рахунок виконаною":        "task.complete",
		"Скасуй задачу про паспорт":                  "task.cancel",
		"Перенеси дедлайн задачі на завтра":          "task.reschedule",
	} {
		if got := DetectDeterministicIntent(text); got != want {
			t.Errorf("%q: %q want %q", text, got, want)
		}
	}
}

func TestRouteRequest(t *testing.T) {
	models := DefaultModels()
	got := RouteRequest(Request{Text: "Нагадай завтра подзвонити Сергію"}, models)
	if got.Mode != "deterministic" || got.Intent != "reminder.create" {
		t.Fatalf("unexpected deterministic route: %+v", got)
	}

	got = RouteRequest(Request{Text: "Ти можеш мені нагадати через 2 хвилини, щоб я перевірив NOVA?"}, models)
	if got.Mode != "deterministic" || got.Intent != "reminder.create" {
		t.Fatalf("unexpected polite reminder route: %+v", got)
	}

	got = RouteRequest(Request{Text: "Який останній коміт у NOVA?"}, models)
	if got.Mode != "deterministic" || got.Intent != "git.status" {
		t.Fatalf("unexpected git route: %+v", got)
	}

	got = RouteRequest(Request{Text: "Чи задеплоївся вже commit 9905719?"}, models)
	if got.Mode != "deterministic" || got.Intent != "git.status" {
		t.Fatalf("unexpected deploy route: %+v", got)
	}

	got = RouteRequest(Request{Text: "NOVA, що з тобою? Чи все працює?"}, models)
	if got.Mode != "deterministic" || got.Intent != "system.status" {
		t.Fatalf("unexpected system status route: %+v", got)
	}

	got = RouteRequest(Request{Text: "/status"}, models)
	if got.Mode != "deterministic" || got.Intent != "system.status" {
		t.Fatalf("unexpected slash status route: %+v", got)
	}

	got = RouteRequest(Request{Text: "Що там з продуктом NOVA?"}, models)
	if got.Mode == "deterministic" && got.Intent == "git.status" {
		t.Fatalf("product question should not be treated as prod deploy: %+v", got)
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
