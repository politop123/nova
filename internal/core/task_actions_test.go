package core

import "testing"

func TestTaskChangeActionContract(t *testing.T) {
	for _, pair := range []struct{ intent, action, want string }{
		{IntentTaskComplete, ActionTaskComplete, "task.complete"},
		{IntentTaskCancel, ActionTaskCancel, "task.cancel"},
		{IntentTaskReschedule, ActionTaskReschedule, "task.reschedule"},
	} {
		if pair.intent != pair.want || pair.action != pair.want {
			t.Fatalf("task contract drift: %+v", pair)
		}
	}
}
