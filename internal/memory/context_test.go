package memory

import "testing"

func TestCompactSummaryIsBounded(t *testing.T) {
	summary := CompactSummary([]ContextMessage{
		{Role: "user", Content: "one two three four five six seven eight nine ten"},
		{Role: "assistant", Content: "another long response"},
	}, 5)
	if len(summary) > 20 {
		t.Fatalf("summary length = %d, want at most 20 characters", len(summary))
	}
}

func TestAssemblePromptKeepsRecentMessagesInOrder(t *testing.T) {
	prompt := AssemblePrompt("remember this", nil, []ContextMessage{
		{Role: "user", Content: "first"},
		{Role: "assistant", Content: "second"},
	}, DefaultContextBudget())
	if prompt == "" || indexOf(prompt, "first") > indexOf(prompt, "second") {
		t.Fatalf("prompt does not preserve message order: %q", prompt)
	}
}

func TestAssemblePromptAlwaysIncludesLatestMessage(t *testing.T) {
	longMessage := "abcdefghijklmnopqrstuvwxyzabcdefghijklmnopqrstuvwxyzabcdefghijklmnopqrstuvwxyz"
	prompt := AssemblePrompt("", nil, []ContextMessage{{Role: "user", Content: longMessage}}, ContextBudget{RecentMessagesTokens: 4})
	if prompt == "" || !contains(prompt, "user:") {
		t.Fatalf("prompt omitted latest message: %q", prompt)
	}
}

func indexOf(value, needle string) int {
	for index := 0; index+len(needle) <= len(value); index++ {
		if value[index:index+len(needle)] == needle {
			return index
		}
	}
	return -1
}

func contains(value, needle string) bool { return indexOf(value, needle) >= 0 }
