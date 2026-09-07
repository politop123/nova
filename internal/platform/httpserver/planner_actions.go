package httpserver

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"nova.local/core/internal/agent"
	"nova.local/core/internal/core"
	"nova.local/core/internal/storage"
	"nova.local/core/internal/usage"
)

const plannerMinimumActionConfidence = 0.55

type plannedActionResult struct {
	Handled         bool
	Intent          string
	AssistantText   string
	Usage           *core.NovaUsage
	CreatedMemory   *storage.Memory
	CreatedTask     *storage.Task
	CreatedReminder *storage.Reminder
}

type executedActionSet struct {
	replies         []string
	createdMemory   *storage.Memory
	createdTask     *storage.Task
	createdReminder *storage.Reminder
}

func (s *Server) planAndMaybeExecuteActions(
	ctx context.Context,
	userID string,
	conversation storage.Conversation,
	channel string,
	modality string,
	text string,
	traceID string,
	userMessage storage.Message,
) (plannedActionResult, *serviceError) {
	deterministicIntent := agent.DetectDeterministicIntent(text)
	if deterministicIntent == core.IntentGitStatus {
		return plannedActionResult{}, nil
	}
	if action, ok := deterministicPersonalFactMemoryAction(text); ok && deterministicIntent == "" {
		plan := core.ActionPlan{
			Intent:     core.IntentMemorySave,
			Confidence: 1,
			Actions:    []core.NovaAction{action},
		}
		executed, actionErr := s.executeActionPlan(ctx, userID, channel, traceID, userMessage, plan)
		if actionErr != nil {
			return plannedActionResult{}, actionErr
		}
		reply := strings.Join(executed.replies, "\n")
		if reply == "" {
			reply = "Запамʼятала."
		}
		return plannedActionResult{
			Handled:       true,
			Intent:        core.IntentMemorySave,
			AssistantText: reply,
			CreatedMemory: executed.createdMemory,
		}, nil
	}
	if s.planner == nil {
		return plannedActionResult{}, nil
	}
	plannerInput, err := s.buildActionPlannerInput(ctx, userID, conversation.ID, channel, modality, text)
	if err != nil {
		return plannedActionResult{}, newServiceError(http.StatusInternalServerError, "storage request failed", err)
	}
	model := strings.TrimSpace(s.cfg.OpenAIPlannerModel)
	if model == "" {
		model = s.models.Simple
	}
	if model == "" {
		return plannedActionResult{}, nil
	}
	estimatedUsage := usage.EstimateRequest(model, agent.ActionPlannerProfile+"\n"+plannerInput, 700)
	if err := s.checkBudget(ctx, userID, estimatedUsage.EstimatedCostUSD); err != nil {
		if errors.Is(err, errBudgetExceeded) {
			return plannedActionResult{}, newServiceError(http.StatusTooManyRequests, "NOVA budget limit reached; try again after the budget period resets", err)
		}
		s.logger.Error("planner budget check failed", "trace_id", traceID, "error", err)
		return plannedActionResult{}, newServiceError(http.StatusServiceUnavailable, "budget check is temporarily unavailable", err)
	}
	startedAt := time.Now()
	result, err := s.planner.PlanActions(ctx, model, agent.ActionPlannerProfile, plannerInput)
	if err != nil {
		s.logger.Warn("action planner failed; falling back to chat route", "trace_id", traceID, "error", err)
		return plannedActionResult{}, nil
	}
	plannerUsage := result.Usage
	if plannerUsage.Model == "" {
		plannerUsage.Model = model
	}
	plannerUsage.Feature = "action_planner"
	if plannerUsage.InputTokens == 0 {
		plannerUsage.InputTokens = estimatedUsage.InputTokens
	}
	if plannerUsage.OutputTokens == 0 {
		plannerUsage.OutputTokens = usage.EstimateTokens(result.Plan.Reply) + usage.EstimateTokens(actionsText(result.Plan.Actions))
	}
	plannerUsage.EstimatedCostUSD = usage.EstimateCost(
		plannerUsage.Model,
		plannerUsage.InputTokens,
		plannerUsage.CachedInputTokens,
		plannerUsage.OutputTokens,
	)
	if err := s.store.RecordUsageEvent(ctx, storage.UsageEvent{
		UserID:            userID,
		Feature:           plannerUsage.Feature,
		Model:             plannerUsage.Model,
		InputTokens:       plannerUsage.InputTokens,
		CachedInputTokens: plannerUsage.CachedInputTokens,
		OutputTokens:      plannerUsage.OutputTokens,
		EstimatedCostUSD:  plannerUsage.EstimatedCostUSD,
		LatencyMS:         int(time.Since(startedAt) / time.Millisecond),
		TraceID:           traceID,
	}); err != nil {
		s.logger.Error("planner usage event failed", "trace_id", traceID, "error", err)
		return plannedActionResult{}, newServiceError(http.StatusServiceUnavailable, "usage accounting is temporarily unavailable", err)
	}

	plan := normalizeServerActionPlan(result.Plan)
	if plan.Intent == core.IntentGitStatus {
		return plannedActionResult{
			Handled:       true,
			Intent:        core.IntentGitStatus,
			AssistantText: s.gitStatusAssistantText(ctx, text),
			Usage:         &plannerUsage,
		}, nil
	}
	if !planNeedsHandling(plan) {
		return plannedActionResult{Usage: &plannerUsage}, nil
	}
	if plan.Confidence > 0 && plan.Confidence < plannerMinimumActionConfidence {
		reply := strings.TrimSpace(plan.Reply)
		if reply == "" {
			reply = "Я не до кінця впевнена, яку дію треба виконати. Уточни, будь ласка."
		}
		return plannedActionResult{
			Handled:       true,
			Intent:        plan.Intent,
			AssistantText: reply,
			Usage:         &plannerUsage,
		}, nil
	}
	if len(plan.Actions) == 0 {
		reply := strings.TrimSpace(plan.Reply)
		if reply == "" {
			reply = clarificationForIntent(plan.Intent)
		}
		return plannedActionResult{
			Handled:       true,
			Intent:        plan.Intent,
			AssistantText: reply,
			Usage:         &plannerUsage,
		}, nil
	}
	executed, actionErr := s.executeActionPlan(ctx, userID, channel, traceID, userMessage, plan)
	if actionErr != nil {
		return plannedActionResult{}, actionErr
	}
	reply := strings.TrimSpace(plan.Reply)
	if len(executed.replies) > 0 {
		reply = strings.Join(executed.replies, "\n")
	}
	if reply == "" {
		reply = "Готово."
	}
	return plannedActionResult{
		Handled:         true,
		Intent:          plan.Intent,
		AssistantText:   reply,
		Usage:           &plannerUsage,
		CreatedMemory:   executed.createdMemory,
		CreatedTask:     executed.createdTask,
		CreatedReminder: executed.createdReminder,
	}, nil
}

func (s *Server) buildActionPlannerInput(ctx context.Context, userID, conversationID, channel, modality, text string) (string, error) {
	messages, err := s.store.ListMessages(ctx, userID, conversationID, 12)
	if err != nil {
		return "", err
	}
	recent := make([]agent.PlannerMessage, 0, len(messages))
	for _, message := range messages {
		content := strings.TrimSpace(message.Content)
		if content == "" {
			continue
		}
		recent = append(recent, agent.PlannerMessage{Role: message.Role, Content: content})
	}
	return agent.BuildPlannerInput(agent.PlannerInput{
		CurrentTime:    time.Now().In(time.UTC).Format(time.RFC3339),
		Timezone:       s.requestTimezone(s.cfg.Timezone),
		Channel:        channel,
		Modality:       modality,
		UserText:       text,
		TelegramReady:  s.telegramReadyForDelivery(),
		Capabilities:   agent.PlannerCapabilities(),
		RecentMessages: recent,
		Notes: map[string]string{
			"execution": "The backend executes only validated typed actions. Missing or vague action fields should become a clarification reply, not an action.",
		},
	}), nil
}

func normalizeServerActionPlan(plan core.ActionPlan) core.ActionPlan {
	plan.Intent = strings.TrimSpace(plan.Intent)
	plan.Reply = strings.TrimSpace(plan.Reply)
	if plan.Intent == "" {
		plan.Intent = core.IntentUnknown
	}
	if plan.Confidence < 0 {
		plan.Confidence = 0
	}
	if plan.Confidence > 1 {
		plan.Confidence = 1
	}
	if len(plan.Actions) > 3 {
		plan.Actions = plan.Actions[:3]
	}
	for i := range plan.Actions {
		action := &plan.Actions[i]
		action.Type = strings.TrimSpace(action.Type)
		action.Title = strings.TrimSpace(action.Title)
		action.Details = strings.TrimSpace(action.Details)
		action.Content = strings.TrimSpace(action.Content)
		action.Kind = strings.TrimSpace(action.Kind)
		action.DueAt = strings.TrimSpace(action.DueAt)
		action.TriggerAt = strings.TrimSpace(action.TriggerAt)
		action.Timezone = strings.TrimSpace(action.Timezone)
		action.DeliveryMethod = strings.TrimSpace(action.DeliveryMethod)
		action.Priority = strings.TrimSpace(action.Priority)
		action.ProjectKey = strings.TrimSpace(action.ProjectKey)
		action.ExpiresAt = strings.TrimSpace(action.ExpiresAt)
		if action.Confidence < 0 {
			action.Confidence = 0
		}
		if action.Confidence > 1 {
			action.Confidence = 1
		}
	}
	return plan
}

func planNeedsHandling(plan core.ActionPlan) bool {
	if len(plan.Actions) > 0 {
		return true
	}
	switch plan.Intent {
	case core.IntentMemorySave, core.IntentTaskCreate, core.IntentReminderCreate, core.IntentGitStatus, core.IntentUnknown:
		return true
	default:
		return false
	}
}

func clarificationForIntent(intent string) string {
	switch intent {
	case core.IntentReminderCreate:
		return "Коли саме нагадати?"
	case core.IntentTaskCreate:
		return "Що саме записати в задачі?"
	case core.IntentMemorySave:
		return "Що саме запамʼятати?"
	default:
		return "Уточни, будь ласка, що саме потрібно зробити."
	}
}

func (s *Server) executeActionPlan(
	ctx context.Context,
	userID string,
	channel string,
	traceID string,
	userMessage storage.Message,
	plan core.ActionPlan,
) (executedActionSet, *serviceError) {
	if len(plan.Actions) == 0 {
		return executedActionSet{}, nil
	}
	var executed executedActionSet
	for index, action := range plan.Actions {
		actionHash := hashPlannedAction(action)
		idempotencyKey := fmt.Sprintf("planner:%s:%d:%s", userMessage.ID, index, actionHash[:16])
		switch action.Type {
		case core.ActionMemorySave:
			memory, reply, err := s.executeMemorySave(ctx, userID, traceID, userMessage, action, actionHash, idempotencyKey)
			if err != nil {
				return executedActionSet{}, err
			}
			executed.createdMemory = memory
			executed.replies = append(executed.replies, reply)
		case core.ActionTaskCreate:
			task, reply, err := s.executeTaskCreate(ctx, userID, traceID, action, actionHash, idempotencyKey)
			if err != nil {
				return executedActionSet{}, err
			}
			executed.createdTask = task
			executed.replies = append(executed.replies, reply)
		case core.ActionReminderCreate:
			reminder, reply, err := s.executeReminderCreate(ctx, userID, channel, traceID, action, actionHash, idempotencyKey)
			if err != nil {
				return executedActionSet{}, err
			}
			executed.createdReminder = reminder
			executed.replies = append(executed.replies, reply)
		default:
			if err := s.recordPlannerAction(ctx, userID, traceID, action.Type, "FAILED", actionHash, idempotencyKey, "unknown action type"); err != nil {
				return executedActionSet{}, err
			}
			executed.replies = append(executed.replies, "Я зрозуміла намір, але ця дія ще не підтримується.")
		}
	}
	return executed, nil
}

func (s *Server) executeMemorySave(
	ctx context.Context,
	userID string,
	traceID string,
	userMessage storage.Message,
	action core.NovaAction,
	actionHash string,
	idempotencyKey string,
) (*storage.Memory, string, *serviceError) {
	content := firstNonEmpty(action.Content, action.Title, action.Details)
	if content == "" {
		if err := s.recordPlannerAction(ctx, userID, traceID, action.Type, "FAILED", actionHash, idempotencyKey, "memory content is required"); err != nil {
			return nil, "", err
		}
		return nil, "Що саме запамʼятати?", nil
	}
	kind := strings.TrimSpace(action.Kind)
	if kind == "" {
		kind = "fact"
	}
	confidence := action.Confidence
	if confidence <= 0 {
		confidence = 0.8
	}
	if confidence > 1 {
		confidence = 1
	}
	expiresAt, err := parseMemoryExpiry(action.ExpiresAt)
	if err != nil {
		if auditErr := s.recordPlannerAction(ctx, userID, traceID, action.Type, "FAILED", actionHash, idempotencyKey, err.Error()); auditErr != nil {
			return nil, "", auditErr
		}
		return nil, "Я зрозуміла, що це треба запамʼятати, але не змогла розібрати термін дії памʼяті.", nil
	}
	memory, err := s.store.CreateMemory(ctx, storage.Memory{
		UserID:          userID,
		Kind:            kind,
		Content:         content,
		Confidence:      confidence,
		SourceMessageID: userMessage.ID,
		ProjectKey:      action.ProjectKey,
		ExpiresAt:       expiresAt,
	})
	if err != nil {
		if auditErr := s.recordPlannerAction(ctx, userID, traceID, action.Type, "FAILED", actionHash, idempotencyKey, err.Error()); auditErr != nil {
			return nil, "", auditErr
		}
		return nil, "", newServiceError(http.StatusInternalServerError, "memory could not be saved", err)
	}
	if err := s.recordPlannerAction(ctx, userID, traceID, action.Type, "EXECUTED", actionHash, idempotencyKey, "memory saved"); err != nil {
		return nil, "", err
	}
	return &memory, "Запамʼятала: " + memory.Content, nil
}

func (s *Server) executeTaskCreate(
	ctx context.Context,
	userID string,
	traceID string,
	action core.NovaAction,
	actionHash string,
	idempotencyKey string,
) (*storage.Task, string, *serviceError) {
	title := firstNonEmpty(action.Title, action.Content)
	if title == "" {
		if err := s.recordPlannerAction(ctx, userID, traceID, action.Type, "FAILED", actionHash, idempotencyKey, "task title is required"); err != nil {
			return nil, "", err
		}
		return nil, "Що саме записати в задачі?", nil
	}
	timezone := firstNonEmpty(action.Timezone, s.cfg.Timezone)
	dueAt, err := parseOptionalZonedTime(action.DueAt, s.requestTimezone(timezone), "dueAt")
	if err != nil {
		if auditErr := s.recordPlannerAction(ctx, userID, traceID, action.Type, "FAILED", actionHash, idempotencyKey, err.Error()); auditErr != nil {
			return nil, "", auditErr
		}
		return nil, "Я зрозуміла задачу, але не змогла розібрати дедлайн. Уточни дату або час.", nil
	}
	task, err := s.store.CreateTask(ctx, storage.Task{
		UserID:  userID,
		Title:   title,
		Details: action.Details,
		Status:  "open",
		DueAt:   dueAt,
	}, idempotencyKey)
	if err != nil {
		if auditErr := s.recordPlannerAction(ctx, userID, traceID, action.Type, "FAILED", actionHash, idempotencyKey, err.Error()); auditErr != nil {
			return nil, "", auditErr
		}
		return nil, "", newServiceError(http.StatusInternalServerError, "task could not be created", err)
	}
	if err := s.recordPlannerAction(ctx, userID, traceID, action.Type, "EXECUTED", actionHash, idempotencyKey, "task created"); err != nil {
		return nil, "", err
	}
	return &task, "Записала задачу: " + task.Title, nil
}

func (s *Server) executeReminderCreate(
	ctx context.Context,
	userID string,
	channel string,
	traceID string,
	action core.NovaAction,
	actionHash string,
	idempotencyKey string,
) (*storage.Reminder, string, *serviceError) {
	title := firstNonEmpty(action.Title, action.Content)
	if title == "" {
		if err := s.recordPlannerAction(ctx, userID, traceID, action.Type, "FAILED", actionHash, idempotencyKey, "reminder title is required"); err != nil {
			return nil, "", err
		}
		return nil, "Про що саме нагадати?", nil
	}
	timezone := firstNonEmpty(action.Timezone, s.cfg.Timezone)
	triggerAt, err := parseRequiredZonedTime(action.TriggerAt, s.requestTimezone(timezone), "triggerAt")
	if err != nil {
		if auditErr := s.recordPlannerAction(ctx, userID, traceID, action.Type, "FAILED", actionHash, idempotencyKey, err.Error()); auditErr != nil {
			return nil, "", auditErr
		}
		return nil, "Коли саме нагадати?", nil
	}
	if !triggerAt.After(time.Now().UTC().Add(2 * time.Second)) {
		if auditErr := s.recordPlannerAction(ctx, userID, traceID, action.Type, "FAILED", actionHash, idempotencyKey, "reminder triggerAt is not in the future"); auditErr != nil {
			return nil, "", auditErr
		}
		return nil, "Цей час уже минув. Напиши, будь ласка, коли нагадати.", nil
	}
	priority := strings.TrimSpace(action.Priority)
	if priority == "" {
		priority = "normal"
	}
	if !validPriority(priority) {
		if auditErr := s.recordPlannerAction(ctx, userID, traceID, action.Type, "FAILED", actionHash, idempotencyKey, "invalid priority"); auditErr != nil {
			return nil, "", auditErr
		}
		return nil, "Я зрозуміла нагадування, але пріоритет має бути low, normal або high.", nil
	}
	deliveryMethod := strings.TrimSpace(action.DeliveryMethod)
	if deliveryMethod == "" {
		deliveryMethod = s.defaultReminderDeliveryMethod(channel)
	}
	if !validDeliveryMethod(deliveryMethod) {
		if auditErr := s.recordPlannerAction(ctx, userID, traceID, action.Type, "FAILED", actionHash, idempotencyKey, "invalid delivery method"); auditErr != nil {
			return nil, "", auditErr
		}
		return nil, "Я зрозуміла нагадування, але канал доставки має бути web або telegram.", nil
	}
	if deliveryMethod == "telegram" {
		if !s.telegramReadyForDelivery() {
			deliveryMethod = "web"
		}
	}
	reminder, err := s.store.CreateReminder(ctx, storage.Reminder{
		UserID:         userID,
		Title:          title,
		TriggerAt:      *triggerAt,
		Timezone:       s.requestTimezone(timezone),
		Priority:       priority,
		DeliveryMethod: deliveryMethod,
		Status:         "scheduled",
	}, idempotencyKey)
	if err != nil {
		if auditErr := s.recordPlannerAction(ctx, userID, traceID, action.Type, "FAILED", actionHash, idempotencyKey, err.Error()); auditErr != nil {
			return nil, "", auditErr
		}
		return nil, "", newServiceError(http.StatusInternalServerError, "reminder could not be created", err)
	}
	if err := s.scheduleReminder(ctx, reminder); err != nil {
		if auditErr := s.recordPlannerAction(ctx, userID, traceID, action.Type, "FAILED", actionHash, idempotencyKey, err.Error()); auditErr != nil {
			return nil, "", auditErr
		}
		return nil, "", newServiceError(http.StatusServiceUnavailable, "reminder could not be scheduled", err)
	}
	if err := s.recordPlannerAction(ctx, userID, traceID, action.Type, "EXECUTED", actionHash, idempotencyKey, "reminder created"); err != nil {
		return nil, "", err
	}
	return &reminder, reminderCreatedText(reminder, s.cfg.Timezone, s.reminderDeliveryLabel(reminder)), nil
}

func (s *Server) defaultReminderDeliveryMethod(channel string) string {
	if channel == core.ChannelTelegram {
		return "telegram"
	}
	if s.telegramReadyForDelivery() {
		return "telegram"
	}
	return "web"
}

func (s *Server) telegramReadyForDelivery() bool {
	if !s.cfg.TelegramEnabled {
		return false
	}
	if s.telegram == nil && strings.TrimSpace(s.cfg.TelegramBotToken) == "" {
		return false
	}
	_, ok := s.cfg.TelegramNotificationChatID()
	return ok
}

func (s *Server) recordPlannerAction(ctx context.Context, userID, traceID, actionName, status, argumentsHash, idempotencyKey, reason string) *serviceError {
	if _, err := s.store.RecordAgentAction(ctx, storage.AgentAction{
		UserID:         userID,
		TraceID:        traceID,
		ActionName:     strings.TrimSpace(actionName),
		Status:         status,
		ArgumentsHash:  argumentsHash,
		IdempotencyKey: idempotencyKey,
		Reason:         reason,
	}); err != nil {
		s.logger.Error("agent action audit failed", "trace_id", traceID, "action", actionName, "error", err)
		return newServiceError(http.StatusServiceUnavailable, "action audit is temporarily unavailable", err)
	}
	return nil
}

func hashPlannedAction(action core.NovaAction) string {
	payload, err := json.Marshal(action)
	if err != nil {
		payload = []byte(action.Type)
	}
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:])
}

func actionsText(actions []core.NovaAction) string {
	payload, err := json.Marshal(actions)
	if err != nil {
		return ""
	}
	return string(payload)
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
