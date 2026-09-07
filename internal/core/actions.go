package core

const (
	IntentReply          = "reply"
	IntentUnknown        = "unknown"
	IntentMemorySave     = "memory.save"
	IntentTaskCreate     = "task.create"
	IntentReminderCreate = "reminder.create"
	IntentGitStatus      = "git.status"
)

const (
	ActionMemorySave     = "memory.save"
	ActionTaskCreate     = "task.create"
	ActionReminderCreate = "reminder.create"
)

// ActionPlan is the model's structured proposal. It is never executed directly:
// the server validates every field, applies policy, records an audit row, and
// then calls the typed storage/scheduler functions.
type ActionPlan struct {
	Intent     string       `json:"intent"`
	Confidence float64      `json:"confidence"`
	Reply      string       `json:"reply"`
	Actions    []NovaAction `json:"actions"`
}

type NovaAction struct {
	Type           string  `json:"type"`
	Title          string  `json:"title"`
	Details        string  `json:"details"`
	Content        string  `json:"content"`
	Kind           string  `json:"kind"`
	DueAt          string  `json:"dueAt"`
	TriggerAt      string  `json:"triggerAt"`
	Timezone       string  `json:"timezone"`
	DeliveryMethod string  `json:"deliveryMethod"`
	Priority       string  `json:"priority"`
	ProjectKey     string  `json:"projectKey"`
	ExpiresAt      string  `json:"expiresAt"`
	Confidence     float64 `json:"confidence"`
}

type ActionPlanResponse struct {
	Plan  ActionPlan `json:"plan"`
	Usage NovaUsage  `json:"usage"`
}
