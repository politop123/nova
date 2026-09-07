package memory

import "testing"

func TestDefaultContextBudget(t *testing.T) {
	if got := DefaultContextBudget().TotalTokens(); got > 4000 {
		t.Fatalf("default context is too large: %d", got)
	}
}
