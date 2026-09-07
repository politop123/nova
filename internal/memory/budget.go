package memory

type ContextBudget struct {
	ProfileTokens        int `json:"profileTokens"`
	SummaryTokens        int `json:"summaryTokens"`
	MemoriesTokens       int `json:"memoriesTokens"`
	RecentMessagesTokens int `json:"recentMessagesTokens"`
	MaxMemories          int `json:"maxMemories"`
}

func DefaultContextBudget() ContextBudget {
	return ContextBudget{
		ProfileTokens:        500,
		SummaryTokens:        700,
		MemoriesTokens:       1000,
		RecentMessagesTokens: 1000,
		MaxMemories:          8,
	}
}

func (b ContextBudget) TotalTokens() int {
	return b.ProfileTokens + b.SummaryTokens + b.MemoriesTokens + b.RecentMessagesTokens
}

type Record struct {
	ID              string  `json:"id"`
	UserID          string  `json:"userId"`
	Kind            string  `json:"kind"`
	Content         string  `json:"content"`
	Confidence      float64 `json:"confidence"`
	SourceMessageID string  `json:"sourceMessageId"`
	ExpiresAt       string  `json:"expiresAt,omitempty"`
}
