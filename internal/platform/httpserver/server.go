package httpserver

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
	"nova.local/core/internal/agent"
	"nova.local/core/internal/core"
	"nova.local/core/internal/memory"
	"nova.local/core/internal/platform/config"
	"nova.local/core/internal/storage"
	"nova.local/core/internal/usage"
)

type Responder interface {
	RespondWithModel(ctx context.Context, model, instructions, input string) (string, error)
}

type UsageResponder interface {
	RespondWithModelUsage(ctx context.Context, model, instructions, input string) (core.ModelResponse, error)
}

type Dependencies struct {
	Store     *storage.Store
	Responder Responder
	Models    agent.ModelCatalog
	UserID    string
	Logger    *slog.Logger
}

type Server struct {
	cfg       config.Config
	db        *pgxpool.Pool
	redis     *redis.Client
	store     *storage.Store
	responder Responder
	models    agent.ModelCatalog
	userID    string
	logger    *slog.Logger
	mux       *http.ServeMux
}

func New(cfg config.Config, db *pgxpool.Pool, redisClient *redis.Client, deps Dependencies) *Server {
	logger := deps.Logger
	if logger == nil {
		logger = slog.Default()
	}
	s := &Server{
		cfg:       cfg,
		db:        db,
		redis:     redisClient,
		store:     deps.Store,
		responder: deps.Responder,
		models:    deps.Models,
		userID:    deps.UserID,
		logger:    logger,
		mux:       http.NewServeMux(),
	}
	if s.models.Simple == "" {
		s.models = agent.DefaultModels()
	}
	s.mux.HandleFunc("GET /health", s.health)
	s.mux.HandleFunc("GET /api/health", s.health)
	s.mux.HandleFunc("GET /api/v1/conversations", s.listConversations)
	s.mux.HandleFunc("POST /api/v1/conversations", s.createConversation)
	s.mux.HandleFunc("GET /api/v1/conversations/{id}/messages", s.listMessages)
	s.mux.HandleFunc("POST /api/v1/conversations/{id}/messages", s.createMessage)
	s.mux.HandleFunc("POST /api/v1/conversations/{id}/messages/stream", s.streamMessage)
	s.mux.HandleFunc("GET /api/v1/memories", s.listMemories)
	s.mux.HandleFunc("POST /api/v1/memories", s.createMemory)
	s.mux.HandleFunc("PATCH /api/v1/memories/{id}", s.updateMemory)
	s.mux.HandleFunc("DELETE /api/v1/memories/{id}", s.deleteMemory)
	return s
}

func (s *Server) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", s.cfg.WebOrigin)
		w.Header().Set("Access-Control-Allow-Credentials", "true")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, X-NOVA-User-ID")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PATCH, DELETE, OPTIONS")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		s.mux.ServeHTTP(w, r)
	})
}

type createConversationRequest struct {
	Title string `json:"title"`
}

type createMessageRequest struct {
	Text string `json:"text"`
}

type createMemoryRequest struct {
	Kind            string   `json:"kind"`
	Content         string   `json:"content"`
	Confidence      *float64 `json:"confidence"`
	SourceMessageID string   `json:"sourceMessageId"`
	ProjectKey      string   `json:"projectKey"`
	ExpiresAt       string   `json:"expiresAt"`
}

type updateMemoryRequest struct {
	Kind       *string  `json:"kind"`
	Content    *string  `json:"content"`
	Confidence *float64 `json:"confidence"`
	ProjectKey *string  `json:"projectKey"`
	ExpiresAt  *string  `json:"expiresAt"`
}

type routeResponse struct {
	Mode   string `json:"mode"`
	Intent string `json:"intent,omitempty"`
	Tier   string `json:"tier,omitempty"`
	Model  string `json:"model,omitempty"`
}

type createMessageResponse struct {
	Conversation storage.Conversation `json:"conversation"`
	UserMessage  storage.Message      `json:"userMessage"`
	Assistant    storage.Message      `json:"assistantMessage"`
	Route        routeResponse        `json:"route"`
	Usage        *core.NovaUsage      `json:"usage,omitempty"`
}

func (s *Server) listConversations(w http.ResponseWriter, r *http.Request) {
	if s.store == nil {
		writeError(w, http.StatusServiceUnavailable, "storage is not configured")
		return
	}
	userID := s.requestUserID(r)
	if err := s.store.EnsureUser(r.Context(), userID); err != nil {
		s.writeStorageError(w, err)
		return
	}
	conversations, err := s.store.ListConversations(r.Context(), userID)
	if err != nil {
		s.writeStorageError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"conversations": conversations})
}

func (s *Server) createConversation(w http.ResponseWriter, r *http.Request) {
	if s.store == nil {
		writeError(w, http.StatusServiceUnavailable, "storage is not configured")
		return
	}
	userID := s.requestUserID(r)
	if err := s.store.EnsureUser(r.Context(), userID); err != nil {
		s.writeStorageError(w, err)
		return
	}
	var request createConversationRequest
	if r.ContentLength != 0 {
		if err := decodeJSON(w, r, &request); err != nil {
			return
		}
	}
	conversation, err := s.store.CreateConversation(r.Context(), userID, strings.TrimSpace(request.Title))
	if err != nil {
		s.writeStorageError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, conversation)
}

func (s *Server) listMemories(w http.ResponseWriter, r *http.Request) {
	if s.store == nil {
		writeError(w, http.StatusServiceUnavailable, "storage is not configured")
		return
	}
	userID := s.requestUserID(r)
	if err := s.store.EnsureUser(r.Context(), userID); err != nil {
		s.writeStorageError(w, err)
		return
	}
	limit := parseLimit(r.URL.Query().Get("limit"), 50)
	query := strings.TrimSpace(r.URL.Query().Get("q"))
	var memories []storage.Memory
	var err error
	if query == "" {
		memories, err = s.store.ListMemories(r.Context(), userID, limit)
	} else {
		memories, err = s.store.SearchMemories(r.Context(), userID, query, limit)
	}
	if err != nil {
		s.writeStorageError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"memories": memories})
}

func (s *Server) createMemory(w http.ResponseWriter, r *http.Request) {
	if s.store == nil {
		writeError(w, http.StatusServiceUnavailable, "storage is not configured")
		return
	}
	userID := s.requestUserID(r)
	if err := s.store.EnsureUser(r.Context(), userID); err != nil {
		s.writeStorageError(w, err)
		return
	}
	var request createMemoryRequest
	if err := decodeJSON(w, r, &request); err != nil {
		return
	}
	memoryRecord, err := buildMemoryRecord(userID, request)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	created, err := s.store.CreateMemory(r.Context(), memoryRecord)
	if err != nil {
		s.writeStorageError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, created)
}

func (s *Server) updateMemory(w http.ResponseWriter, r *http.Request) {
	if s.store == nil {
		writeError(w, http.StatusServiceUnavailable, "storage is not configured")
		return
	}
	var request updateMemoryRequest
	if err := decodeJSON(w, r, &request); err != nil {
		return
	}
	if request.Kind == nil && request.Content == nil && request.Confidence == nil && request.ProjectKey == nil && request.ExpiresAt == nil {
		writeError(w, http.StatusBadRequest, "at least one memory field is required")
		return
	}
	if request.Kind != nil && strings.TrimSpace(*request.Kind) == "" {
		writeError(w, http.StatusBadRequest, "kind cannot be empty")
		return
	}
	if request.Content != nil && strings.TrimSpace(*request.Content) == "" {
		writeError(w, http.StatusBadRequest, "content cannot be empty")
		return
	}
	if request.Confidence != nil && (*request.Confidence < 0 || *request.Confidence > 1) {
		writeError(w, http.StatusBadRequest, "confidence must be between 0 and 1")
		return
	}
	var expiresAt *time.Time
	if request.ExpiresAt != nil {
		parsed, err := parseMemoryExpiry(*request.ExpiresAt)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		expiresAt = parsed
	}
	updated, err := s.store.UpdateMemory(r.Context(), s.requestUserID(r), r.PathValue("id"), storage.MemoryUpdate{
		Kind: request.Kind, Content: request.Content, Confidence: request.Confidence,
		ProjectKey: request.ProjectKey, ExpiresAt: expiresAt,
	})
	if err != nil {
		s.writeMemoryError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, updated)
}

func (s *Server) deleteMemory(w http.ResponseWriter, r *http.Request) {
	if s.store == nil {
		writeError(w, http.StatusServiceUnavailable, "storage is not configured")
		return
	}
	if err := s.store.DeleteMemory(r.Context(), s.requestUserID(r), r.PathValue("id")); err != nil {
		s.writeMemoryError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func buildMemoryRecord(userID string, request createMemoryRequest) (storage.Memory, error) {
	kind := strings.TrimSpace(request.Kind)
	content := strings.TrimSpace(request.Content)
	if kind == "" {
		return storage.Memory{}, errors.New("kind is required")
	}
	if content == "" {
		return storage.Memory{}, errors.New("content is required")
	}
	confidence := 0.8
	if request.Confidence != nil {
		confidence = *request.Confidence
	}
	if confidence < 0 || confidence > 1 {
		return storage.Memory{}, errors.New("confidence must be between 0 and 1")
	}
	expiresAt, err := parseMemoryExpiry(request.ExpiresAt)
	if err != nil {
		return storage.Memory{}, err
	}
	return storage.Memory{
		UserID: userID, Kind: kind, Content: content, Confidence: confidence,
		SourceMessageID: strings.TrimSpace(request.SourceMessageID),
		ProjectKey:      strings.TrimSpace(request.ProjectKey), ExpiresAt: expiresAt,
	}, nil
}

func parseMemoryExpiry(value string) (*time.Time, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil, nil
	}
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return nil, errors.New("expiresAt must be an RFC3339 timestamp")
	}
	return &parsed, nil
}

func parseLimit(value string, fallback int) int {
	limit, err := strconv.Atoi(value)
	if err != nil || limit < 1 {
		return fallback
	}
	if limit > 100 {
		return 100
	}
	return limit
}

func (s *Server) listMessages(w http.ResponseWriter, r *http.Request) {
	if s.store == nil {
		writeError(w, http.StatusServiceUnavailable, "storage is not configured")
		return
	}
	userID := s.requestUserID(r)
	conversationID := r.PathValue("id")
	if _, err := s.store.GetConversation(r.Context(), userID, conversationID); err != nil {
		s.writeConversationError(w, err)
		return
	}
	messages, err := s.store.ListMessages(r.Context(), userID, conversationID, 100)
	if err != nil {
		s.writeStorageError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"messages": messages})
}

func (s *Server) createMessage(w http.ResponseWriter, r *http.Request) {
	if s.store == nil {
		writeError(w, http.StatusServiceUnavailable, "storage is not configured")
		return
	}
	userID := s.requestUserID(r)
	conversationID := r.PathValue("id")
	conversation, err := s.store.GetConversation(r.Context(), userID, conversationID)
	if err != nil {
		s.writeConversationError(w, err)
		return
	}

	var request createMessageRequest
	if err := decodeJSON(w, r, &request); err != nil {
		return
	}
	request.Text = strings.TrimSpace(request.Text)
	if request.Text == "" {
		writeError(w, http.StatusBadRequest, "text is required")
		return
	}

	traceID := newTraceID()
	userMessage, err := s.store.AddMessage(r.Context(), storage.Message{
		ConversationID: conversationID,
		UserID:         userID,
		Role:           "user",
		Channel:        "web",
		Modality:       "text",
		Content:        request.Text,
		TraceID:        traceID,
	})
	if err != nil {
		s.writeStorageError(w, err)
		return
	}

	route := agent.RouteRequest(agent.Request{Text: request.Text}, s.models)
	assistantText := deterministicResponse(route.Intent)
	var modelUsage *core.NovaUsage
	modelStartedAt := time.Now()
	if route.Mode == "model" {
		if s.responder == nil {
			writeError(w, http.StatusServiceUnavailable, "OPENAI_API_KEY is not configured")
			return
		}
		messages, listErr := s.store.ListMessages(r.Context(), userID, conversationID, 100)
		if listErr != nil {
			s.writeStorageError(w, listErr)
			return
		}
		modelInput, listErr := s.buildModelInput(r.Context(), userID, conversationID, messages)
		if listErr != nil {
			s.writeStorageError(w, listErr)
			return
		}
		estimatedUsage := usage.EstimateRequest(route.Model, agent.SystemProfile+"\n"+modelInput, 512)
		if err := s.checkBudget(r.Context(), userID, estimatedUsage.EstimatedCostUSD); err != nil {
			if errors.Is(err, errBudgetExceeded) {
				writeError(w, http.StatusTooManyRequests, "NOVA budget limit reached; try again after the budget period resets")
				return
			}
			s.logger.Error("budget check failed", "trace_id", traceID, "error", err)
			writeError(w, http.StatusServiceUnavailable, "budget check is temporarily unavailable")
			return
		}
		if usageResponder, ok := s.responder.(UsageResponder); ok {
			var result core.ModelResponse
			result, err = usageResponder.RespondWithModelUsage(r.Context(), route.Model, agent.SystemProfile, modelInput)
			assistantText = result.Text
			modelUsage = &result.Usage
		} else {
			assistantText, err = s.responder.RespondWithModel(r.Context(), route.Model, agent.SystemProfile, modelInput)
			estimatedUsage = usage.EstimateResponse(route.Model, modelInput, assistantText)
			modelUsage = &estimatedUsage
		}
		if err != nil {
			s.logger.Error("model response failed", "trace_id", traceID, "error", err)
			writeError(w, http.StatusBadGateway, "model response failed")
			return
		}
		if modelUsage == nil {
			modelUsage = &estimatedUsage
		}
		if modelUsage.InputTokens == 0 {
			modelUsage.InputTokens = estimatedUsage.InputTokens
		}
		if modelUsage.OutputTokens == 0 {
			modelUsage.OutputTokens = usage.EstimateTokens(assistantText)
		}
		modelUsage.Model = route.Model
		modelUsage.Feature = "chat"
		modelUsage.EstimatedCostUSD = usage.EstimateCost(
			modelUsage.Model,
			modelUsage.InputTokens,
			modelUsage.CachedInputTokens,
			modelUsage.OutputTokens,
		)
		if err := s.store.RecordUsageEvent(r.Context(), storage.UsageEvent{
			UserID: userID, Feature: modelUsage.Feature, Model: modelUsage.Model,
			InputTokens: modelUsage.InputTokens, CachedInputTokens: modelUsage.CachedInputTokens,
			OutputTokens: modelUsage.OutputTokens, EstimatedCostUSD: modelUsage.EstimatedCostUSD,
			LatencyMS: int(time.Since(modelStartedAt) / time.Millisecond),
			TraceID:   traceID,
		}); err != nil {
			s.logger.Error("usage event failed", "trace_id", traceID, "error", err)
			writeError(w, http.StatusServiceUnavailable, "usage accounting is temporarily unavailable")
			return
		}
	}

	assistantMessage, err := s.store.AddMessage(r.Context(), storage.Message{
		ConversationID: conversationID,
		UserID:         userID,
		Role:           "assistant",
		Channel:        "web",
		Modality:       "text",
		Content:        assistantText,
		TraceID:        traceID,
	})
	if err != nil {
		s.writeStorageError(w, err)
		return
	}
	s.refreshConversationSummary(r.Context(), userID, conversationID)
	writeJSON(w, http.StatusCreated, createMessageResponse{
		Conversation: conversation,
		UserMessage:  userMessage,
		Assistant:    assistantMessage,
		Route: routeResponse{
			Mode: stringOrEmpty(route.Mode), Intent: stringOrEmpty(route.Intent),
			Tier: string(route.Tier), Model: stringOrEmpty(route.Model),
		},
		Usage: modelUsage,
	})
}

var errBudgetExceeded = errors.New("budget exceeded")

func (s *Server) checkBudget(ctx context.Context, userID string, estimatedCost float64) error {
	if s.store == nil {
		return errors.New("storage is not configured")
	}
	now := time.Now()
	location, err := time.LoadLocation(s.cfg.Timezone)
	if err != nil {
		location = time.UTC
	}
	localNow := now.In(location)
	dailyStart := time.Date(localNow.Year(), localNow.Month(), localNow.Day(), 0, 0, 0, 0, location).UTC()
	monthlyStart := time.Date(localNow.Year(), localNow.Month(), 1, 0, 0, 0, 0, location).UTC()
	dailyCost, err := s.store.CostSince(ctx, userID, dailyStart)
	if err != nil {
		return err
	}
	monthlyCost, err := s.store.CostSince(ctx, userID, monthlyStart)
	if err != nil {
		return err
	}
	decision := usage.CheckBudget(usage.BudgetLimits{
		DailyUSD: s.cfg.DailyBudgetUSD, MonthlyUSD: s.cfg.MonthlyBudgetUSD,
	}, usage.BudgetSnapshot{
		DailyUsedUSD: dailyCost, MonthlyUsedUSD: monthlyCost, EstimatedUSD: estimatedCost,
	})
	if decision.Exceeded {
		return fmt.Errorf("%w: %s limit %.6f, used %.6f, request %.6f", errBudgetExceeded,
			decision.Period, decision.LimitUSD, decision.UsedUSD, decision.EstimatedUSD)
	}
	return nil
}

type StreamResponder interface {
	RespondWithModelStream(ctx context.Context, model, instructions, input string, onDelta func(string)) (core.ModelResponse, error)
}

func (s *Server) streamMessage(w http.ResponseWriter, r *http.Request) {
	if s.store == nil {
		writeError(w, http.StatusServiceUnavailable, "storage is not configured")
		return
	}
	userID := s.requestUserID(r)
	conversationID := r.PathValue("id")
	conversation, err := s.store.GetConversation(r.Context(), userID, conversationID)
	if err != nil {
		s.writeConversationError(w, err)
		return
	}
	var request createMessageRequest
	if err := decodeJSON(w, r, &request); err != nil {
		return
	}
	request.Text = strings.TrimSpace(request.Text)
	if request.Text == "" {
		writeError(w, http.StatusBadRequest, "text is required")
		return
	}

	traceID := newTraceID()
	userMessage, err := s.store.AddMessage(r.Context(), storage.Message{
		ConversationID: conversationID,
		UserID:         userID,
		Role:           "user",
		Channel:        "web",
		Modality:       "text",
		Content:        request.Text,
		TraceID:        traceID,
	})
	if err != nil {
		s.writeStorageError(w, err)
		return
	}

	route := agent.RouteRequest(agent.Request{Text: request.Text}, s.models)
	var messages []storage.Message
	var modelInput string
	var estimatedUsage core.NovaUsage
	modelStartedAt := time.Now()
	if route.Mode == "model" {
		if s.responder == nil {
			writeError(w, http.StatusServiceUnavailable, "OPENAI_API_KEY is not configured")
			return
		}
		messages, err = s.store.ListMessages(r.Context(), userID, conversationID, 100)
		if err != nil {
			s.writeStorageError(w, err)
			return
		}
		modelInput, err = s.buildModelInput(r.Context(), userID, conversationID, messages)
		if err != nil {
			s.writeStorageError(w, err)
			return
		}
		estimatedUsage = usage.EstimateRequest(route.Model, agent.SystemProfile+"\n"+modelInput, 512)
		if err := s.checkBudget(r.Context(), userID, estimatedUsage.EstimatedCostUSD); err != nil {
			if errors.Is(err, errBudgetExceeded) {
				writeError(w, http.StatusTooManyRequests, "NOVA budget limit reached; try again after the budget period resets")
				return
			}
			s.logger.Error("budget check failed", "trace_id", traceID, "error", err)
			writeError(w, http.StatusServiceUnavailable, "budget check is temporarily unavailable")
			return
		}
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, "streaming is not supported by this server")
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	if err := writeSSE(w, flusher, "ready", map[string]any{"userMessage": userMessage}); err != nil {
		return
	}

	assistantText := deterministicResponse(route.Intent)
	var modelUsage *core.NovaUsage
	if route.Mode == "model" {
		if streamResponder, ok := s.responder.(StreamResponder); ok {
			var result core.ModelResponse
			result, err = streamResponder.RespondWithModelStream(r.Context(), route.Model, agent.SystemProfile, modelInput, func(delta string) {
				if writeErr := writeSSE(w, flusher, "delta", map[string]string{"text": delta}); writeErr != nil {
					s.logger.Debug("stream client disconnected", "trace_id", traceID, "error", writeErr)
				}
			})
			assistantText = result.Text
			modelUsage = &result.Usage
		} else if usageResponder, ok := s.responder.(UsageResponder); ok {
			var result core.ModelResponse
			result, err = usageResponder.RespondWithModelUsage(r.Context(), route.Model, agent.SystemProfile, modelInput)
			assistantText = result.Text
			modelUsage = &result.Usage
			if assistantText != "" {
				_ = writeSSE(w, flusher, "delta", map[string]string{"text": assistantText})
			}
		} else {
			assistantText, err = s.responder.RespondWithModel(r.Context(), route.Model, agent.SystemProfile, modelInput)
			modelUsage = &estimatedUsage
			if assistantText != "" {
				_ = writeSSE(w, flusher, "delta", map[string]string{"text": assistantText})
			}
		}
		if err != nil {
			s.logger.Error("model stream failed", "trace_id", traceID, "error", err)
			_ = writeSSE(w, flusher, "error", map[string]string{"message": "model response failed"})
			return
		}
		if modelUsage == nil {
			modelUsage = &estimatedUsage
		}
		if modelUsage.InputTokens == 0 {
			modelUsage.InputTokens = estimatedUsage.InputTokens
		}
		if modelUsage.OutputTokens == 0 {
			modelUsage.OutputTokens = usage.EstimateTokens(assistantText)
		}
		modelUsage.Model = route.Model
		modelUsage.Feature = "chat"
		modelUsage.EstimatedCostUSD = usage.EstimateCost(modelUsage.Model, modelUsage.InputTokens, modelUsage.CachedInputTokens, modelUsage.OutputTokens)
		if err := s.store.RecordUsageEvent(r.Context(), storage.UsageEvent{
			UserID: userID, Feature: modelUsage.Feature, Model: modelUsage.Model,
			InputTokens: modelUsage.InputTokens, CachedInputTokens: modelUsage.CachedInputTokens,
			OutputTokens: modelUsage.OutputTokens, EstimatedCostUSD: modelUsage.EstimatedCostUSD,
			LatencyMS: int(time.Since(modelStartedAt) / time.Millisecond),
			TraceID:   traceID,
		}); err != nil {
			s.logger.Error("usage event failed", "trace_id", traceID, "error", err)
			_ = writeSSE(w, flusher, "error", map[string]string{"message": "usage accounting is temporarily unavailable"})
			return
		}
	}

	assistantMessage, err := s.store.AddMessage(r.Context(), storage.Message{
		ConversationID: conversationID,
		UserID:         userID,
		Role:           "assistant",
		Channel:        "web",
		Modality:       "text",
		Content:        assistantText,
		TraceID:        traceID,
	})
	if err != nil {
		s.logger.Error("assistant message persistence failed", "trace_id", traceID, "error", err)
		_ = writeSSE(w, flusher, "error", map[string]string{"message": "assistant message could not be saved"})
		return
	}
	s.refreshConversationSummary(r.Context(), userID, conversationID)
	_ = writeSSE(w, flusher, "done", createMessageResponse{
		Conversation: conversation,
		UserMessage:  userMessage,
		Assistant:    assistantMessage,
		Route: routeResponse{
			Mode: stringOrEmpty(route.Mode), Intent: stringOrEmpty(route.Intent),
			Tier: string(route.Tier), Model: stringOrEmpty(route.Model),
		},
		Usage: modelUsage,
	})
}

func writeSSE(w http.ResponseWriter, flusher http.Flusher, event string, value any) error {
	payload, err := json.Marshal(value)
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, payload); err != nil {
		return err
	}
	flusher.Flush()
	return nil
}

func (s *Server) requestUserID(r *http.Request) string {
	if userID := strings.TrimSpace(r.Header.Get("X-NOVA-User-ID")); userID != "" {
		return userID
	}
	return s.userID
}

func (s *Server) writeStorageError(w http.ResponseWriter, err error) {
	s.logger.Error("storage request failed", "error", err)
	writeError(w, http.StatusInternalServerError, "storage request failed")
}

func (s *Server) writeConversationError(w http.ResponseWriter, err error) {
	if errors.Is(err, storage.ErrNotFound) {
		writeError(w, http.StatusNotFound, "conversation not found")
		return
	}
	s.writeStorageError(w, err)
}

func (s *Server) writeMemoryError(w http.ResponseWriter, err error) {
	if errors.Is(err, storage.ErrNotFound) {
		writeError(w, http.StatusNotFound, "memory not found")
		return
	}
	s.writeStorageError(w, err)
}

func decodeJSON(w http.ResponseWriter, r *http.Request, destination any) error {
	r.Body = http.MaxBytesReader(w, r.Body, 64*1024)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return err
	}
	return nil
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}

func deterministicResponse(intent string) string {
	switch intent {
	case "reminder.create":
		return "Команду нагадування розпізнано. Планувальник нагадувань буде підключено наступним етапом."
	case "interaction.stop":
		return "Поточну дію зупинено."
	case "confirmation.approve":
		return "Підтвердження отримано."
	case "confirmation.reject":
		return "Дію скасовано."
	default:
		return "Команду оброблено без виклику моделі."
	}
}

func (s *Server) buildModelInput(ctx context.Context, userID, conversationID string, messages []storage.Message) (string, error) {
	budget := memory.DefaultContextBudget()
	summary := ""
	conversationSummary, err := s.store.GetConversationSummary(ctx, userID, conversationID)
	if err != nil && !errors.Is(err, storage.ErrNotFound) {
		return "", err
	}
	if err == nil {
		summary = conversationSummary.Summary
	}
	recent := make([]memory.ContextMessage, 0, len(messages))
	latestUserText := ""
	for _, message := range messages {
		recent = append(recent, memory.ContextMessage{Role: message.Role, Content: message.Content})
		if message.Role == "user" {
			latestUserText = message.Content
		}
	}
	relevantMemories, err := s.store.SearchMemories(ctx, userID, latestUserText, budget.MaxMemories)
	if err != nil {
		return "", err
	}
	memoryLines := make([]string, 0, len(relevantMemories))
	for _, relevant := range relevantMemories {
		provenance := ""
		if relevant.SourceMessageID != "" {
			provenance = ", source=" + relevant.SourceMessageID
		}
		memoryLines = append(memoryLines, fmt.Sprintf("[%s, confidence=%.2f%s] %s", relevant.Kind, relevant.Confidence, provenance, relevant.Content))
	}
	return memory.AssemblePrompt(summary, memoryLines, recent, budget), nil
}

func (s *Server) refreshConversationSummary(ctx context.Context, userID, conversationID string) {
	messages, err := s.store.ListMessages(ctx, userID, conversationID, 100)
	if err != nil {
		s.logger.Warn("conversation summary refresh skipped", "conversation_id", conversationID, "error", err)
		return
	}
	// Keep the recent window in the prompt and summarize only older turns.
	cutoff := len(messages) - 8
	if cutoff < 1 {
		return
	}
	items := make([]memory.ContextMessage, 0, cutoff)
	for _, message := range messages[:cutoff] {
		items = append(items, memory.ContextMessage{Role: message.Role, Content: message.Content})
	}
	summary := memory.CompactSummary(items, memory.DefaultContextBudget().SummaryTokens)
	if summary == "" {
		return
	}
	if err := s.store.UpsertConversationSummary(ctx, userID, conversationID, summary); err != nil {
		s.logger.Warn("conversation summary refresh failed", "conversation_id", conversationID, "error", err)
	}
}

func newTraceID() string {
	bytes := make([]byte, 16)
	if _, err := rand.Read(bytes); err != nil {
		return fmt.Sprintf("trace-%d", time.Now().UnixNano())
	}
	return fmt.Sprintf("%x", bytes)
}

func stringOrEmpty(value string) string { return value }

type healthResponse struct {
	Service   string            `json:"service"`
	Status    string            `json:"status"`
	Version   string            `json:"version"`
	Timestamp string            `json:"timestamp"`
	Checks    map[string]string `json:"checks"`
}

func (s *Server) health(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 750*time.Millisecond)
	defer cancel()
	checks := map[string]string{"api": "ok"}
	status := "ok"

	if s.db != nil {
		if err := s.db.Ping(ctx); err != nil {
			checks["postgres"] = "degraded"
			status = "degraded"
		} else {
			checks["postgres"] = "ok"
		}
	}
	if s.redis != nil {
		if err := s.redis.Ping(ctx).Err(); err != nil {
			checks["redis"] = "degraded"
			status = "degraded"
		} else {
			checks["redis"] = "ok"
		}
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(healthResponse{
		Service:   "nova-api",
		Status:    status,
		Version:   "0.1.0",
		Timestamp: time.Now().UTC().Format(time.RFC3339),
		Checks:    checks,
	})
}
