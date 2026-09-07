package policy

import "testing"

func TestEvaluateRequiresConfirmation(t *testing.T) {
	got := Evaluate(Context{Authenticated: true, ToolEnabled: true, Permission: Confirm})
	if got.Outcome != NeedConfirm {
		t.Fatalf("expected confirmation, got %+v", got)
	}
}
