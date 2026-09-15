package agent

import (
	"encoding/json"
	"time"
)

const ActionPlannerProfile = `You are NOVA's action planner.

Your job is to understand the user's latest Ukrainian or mixed-language message and return one strict JSON action plan.

Rules:
- Return JSON only.
- Do not execute actions yourself. The Go backend validates and executes typed actions.
- Prefer action when the user asks NOVA to remember, write down, create a task, or remind them.
- If the user provides a stable personal fact, contact detail, identity detail, family detail, or the answer to a previous "I do not know yet; share it and I will remember" prompt, create a memory.save action even when the user did not explicitly say "remember this".
- Save personal facts as short, self-contained Ukrainian memory lines, for example "Номер телефону користувача: ..." or "Номер телефону дружини користувача: ...".
- If the user asks about NOVA's own repository, latest commit, GitHub Actions, deploy status, production version, or whether a commit is deployed, use intent "git.status" and no actions. The backend will read the real server-side GitHub/deployment status.
- If the user asks whether NOVA is alive, working, healthy, online, or asks for system/API/Telegram/reminder/worker status, use intent "system.status" and no actions. The backend will read the real operational status.
- If the user asks for their plans, tasks or reminders, use intent "agenda.list", no actions, and agendaDate as a local YYYY-MM-DD date (today/tomorrow resolved using currentTime and timezone), or empty for all upcoming plans. Never invent agenda entries. For an unclear or unsupported date range ask a clarification with intent "unknown".
- For cancellation use intent/action "reminder.cancel"; for moving/postponing an existing reminder use intent/action "reminder.reschedule". Never create a replacement reminder. Put the identifying subject in title, without command words or the NEW time. Use targetTime only for an explicitly identified ORIGINAL reminder time (RFC3339); otherwise leave it empty. The backend resolves the subject against active reminders and asks for clarification if multiple match. For rescheduling put the new future RFC3339 time in triggerAt. If the subject or new time is missing, ask a clarification with no actions.
- Use recent messages to resolve follow-up clarifications, but do not guess which of multiple reminders the user means. Do not interpret negated, hypothetical or informational requests as mutations. Only change reminders on the user's request.
- Keep agendaDate empty for other intents, and targetTime empty for actions other than reminder.cancel/reminder.reschedule.
- If the user only wants a normal conversational answer, use intent "reply" and no actions.
- If the user intent is unclear, use intent "unknown", no actions, and ask one concise Ukrainian clarifying question in reply.
- For reminders and tasks, resolve dates using currentTime and timezone. Output RFC3339 timestamps with an explicit offset or Z.
- If a reminder/task date is vague and cannot be resolved, do not create an action; ask a concise clarification in reply.
- Telegram is the primary delivery channel. When creating a reminder from Web and Telegram is configured, prefer deliveryMethod "telegram"; otherwise "web".
- Keep reply short, natural, and Ukrainian.

Available action types:
- memory.save: remember a useful stable fact/preference about the user or project.
- task.create: create a task/to-do item.
- reminder.create: create a scheduled reminder.
- reminder.cancel: cancel one identified active reminder.
- reminder.reschedule: move one identified active reminder to a new time.

Read-only intents:
- agenda.list: read the user's real open tasks and scheduled reminders for agendaDate or upcoming plans.
- git.status: answer a question about NOVA's GitHub repository, latest commit, deploy workflow, or production commit.
- system.status: answer a question about NOVA Core health, API, database, Redis, worker, reminders, Telegram, OpenAI configuration, and Git/deploy status.`

type PlannerInput struct {
	CurrentTime    string            `json:"currentTime"`
	Timezone       string            `json:"timezone"`
	Channel        string            `json:"channel"`
	Modality       string            `json:"modality"`
	UserText       string            `json:"userText"`
	TelegramReady  bool              `json:"telegramReady"`
	Capabilities   []Capability      `json:"capabilities"`
	RecentMessages []PlannerMessage  `json:"recentMessages,omitempty"`
	Notes          map[string]string `json:"notes,omitempty"`
}

type Capability struct {
	Intent      string   `json:"intent"`
	ActionType  string   `json:"actionType"`
	Description string   `json:"description"`
	Required    []string `json:"required"`
	Optional    []string `json:"optional"`
}

type PlannerMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

func PlannerCapabilities() []Capability {
	return []Capability{
		{
			Intent:      "memory.save",
			ActionType:  "memory.save",
			Description: "Save a stable fact, user preference, project preference, identity detail, or reusable context.",
			Required:    []string{"content"},
			Optional:    []string{"kind", "projectKey", "expiresAt", "confidence"},
		},
		{
			Intent:      "task.create",
			ActionType:  "task.create",
			Description: "Create a task or to-do item that should remain visible until completed or cancelled.",
			Required:    []string{"title"},
			Optional:    []string{"details", "dueAt"},
		},
		{
			Intent:      "reminder.create",
			ActionType:  "reminder.create",
			Description: "Create a reminder for a specific future time.",
			Required:    []string{"title", "triggerAt"},
			Optional:    []string{"deliveryMethod", "priority"},
		},
		{
			Intent: "agenda.list", ActionType: "",
			Description: "Read real tasks and reminders for a local agendaDate (YYYY-MM-DD), or all upcoming plans when empty.",
			Required:    []string{}, Optional: []string{"agendaDate"},
		},
		{
			Intent: "reminder.cancel", ActionType: "reminder.cancel",
			Description: "Cancel one active reminder identified by subject; clarify ambiguous matches.",
			Required:    []string{"title"}, Optional: []string{"targetTime"},
		},
		{
			Intent: "reminder.reschedule", ActionType: "reminder.reschedule",
			Description: "Move one active reminder identified by subject to a new future time.",
			Required:    []string{"title", "triggerAt"}, Optional: []string{"targetTime", "timezone"},
		},
		{
			Intent:      "git.status",
			ActionType:  "",
			Description: "Read NOVA repository, latest commit, GitHub Actions, deployment workflow, or production commit status.",
			Required:    []string{},
			Optional:    []string{},
		},
		{
			Intent:      "system.status",
			ActionType:  "",
			Description: "Read NOVA Core health, API, database, Redis, worker, reminders, Telegram, OpenAI configuration, and Git/deploy status.",
			Required:    []string{},
			Optional:    []string{},
		},
	}
}

func BuildPlannerInput(input PlannerInput) string {
	if input.CurrentTime == "" {
		input.CurrentTime = time.Now().UTC().Format(time.RFC3339)
	}
	if input.Timezone == "" {
		input.Timezone = "Europe/Kyiv"
	}
	if len(input.Capabilities) == 0 {
		input.Capabilities = PlannerCapabilities()
	}
	payload, err := json.MarshalIndent(input, "", "  ")
	if err != nil {
		return "{}"
	}
	return string(payload)
}
