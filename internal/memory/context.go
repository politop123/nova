package memory

import "strings"

type ContextMessage struct {
	Role    string
	Content string
}

// CompactSummary creates a bounded, deterministic rolling transcript. It is a
// safe fallback until asynchronous model-based fact extraction is introduced:
// old turns remain available without sending the full conversation every time.
func CompactSummary(messages []ContextMessage, maxTokens int) string {
	if maxTokens < 1 {
		return ""
	}
	maxChars := maxTokens * 4
	var builder strings.Builder
	for _, message := range messages {
		content := strings.Join(strings.Fields(message.Content), " ")
		if content == "" {
			continue
		}
		line := message.Role + ": " + content
		if builder.Len() > 0 {
			line = "\n" + line
		}
		if builder.Len()+len(line) > maxChars {
			break
		}
		builder.WriteString(line)
	}
	return builder.String()
}

func AssemblePrompt(summary string, memories []string, recent []ContextMessage, budget ContextBudget) string {
	var sections []string
	if summary = trimToTokens(summary, budget.SummaryTokens); summary != "" {
		sections = append(sections, "Conversation summary:\n"+summary)
	}
	if len(memories) > 0 && budget.MemoriesTokens > 0 {
		memoryText := trimToTokens(strings.Join(memories, "\n"), budget.MemoriesTokens)
		if memoryText != "" {
			sections = append(sections, "Relevant memories:\n"+memoryText)
		}
	}
	if recentText := recentMessagesText(recent, budget.RecentMessagesTokens); recentText != "" {
		sections = append(sections, "Recent messages:\n"+recentText)
	}
	return strings.Join(sections, "\n\n")
}

func recentMessagesText(messages []ContextMessage, maxTokens int) string {
	if maxTokens < 1 {
		return ""
	}
	maxChars := maxTokens * 4
	lines := make([]string, 0, len(messages))
	characters := 0
	for index := len(messages) - 1; index >= 0; index-- {
		content := strings.Join(strings.Fields(messages[index].Content), " ")
		if content == "" {
			continue
		}
		line := messages[index].Role + ": " + content
		separator := 0
		if len(lines) > 0 {
			separator = 1
		}
		if characters+separator+len(line) > maxChars {
			if len(lines) > 0 {
				break
			}
			line = trimToTokens(line, maxTokens)
		}
		lines = append(lines, line)
		characters += separator + len(line)
	}
	for left, right := 0, len(lines)-1; left < right; left, right = left+1, right-1 {
		lines[left], lines[right] = lines[right], lines[left]
	}
	return strings.Join(lines, "\n")
}

func trimToTokens(value string, maxTokens int) string {
	if maxTokens < 1 {
		return ""
	}
	value = strings.TrimSpace(value)
	maxChars := maxTokens * 4
	runes := []rune(value)
	if len(runes) <= maxChars {
		return value
	}
	return strings.TrimSpace(string(runes[:maxChars])) + "…"
}
