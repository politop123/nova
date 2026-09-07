package httpserver

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/hibiken/asynq"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
	"nova.local/core/internal/agent"
	"nova.local/core/internal/core"
	"nova.local/core/internal/integrations/telegram"
	"nova.local/core/internal/jobs"
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

type AudioTranscriber interface {
	TranscribeAudio(ctx context.Context, audio []byte, filename, contentType, language string) (core.ModelResponse, error)
}

type TelegramClient interface {
	SendMessage(ctx context.Context, chatID int64, text string) error
	DownloadVoice(ctx context.Context, fileID string) (telegram.VoiceDownload, error)
}

type Dependencies struct {
	Store         *storage.Store
	Responder     Responder
	Models        agent.ModelCatalog
	UserID        string
	Logger        *slog.Logger
	ReminderQueue *asynq.Client
	Telegram      TelegramClient
	Transcriber   AudioTranscriber
}

type Server struct {
	cfg           config.Config
	db            *pgxpool.Pool
	redis         *redis.Client
	store         *storage.Store
	responder     Responder
	models        agent.ModelCatalog
	userID        string
	logger        *slog.Logger
	reminderQueue *asynq.Client
	telegram      TelegramClient
	transcriber   AudioTranscriber
	mux           *http.ServeMux
}

func New(cfg config.Config, db *pgxpool.Pool, redisClient *redis.Client, deps Dependencies) *Server {
	logger := deps.Logger
	if logger == nil {
		logger = slog.Default()
	}
	s := &Server{
		cfg:           cfg,
		db:            db,
		redis:         redisClient,
		store:         deps.Store,
		responder:     deps.Responder,
		models:        deps.Models,
		userID:        deps.UserID,
		logger:        logger,
		reminderQueue: deps.ReminderQueue,
		telegram:      deps.Telegram,
		transcriber:   deps.Transcriber,
		mux:           http.NewServeMux(),
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
	s.mux.HandleFunc("GET /api/v1/tasks", s.listTasks)
	s.mux.HandleFunc("POST /api/v1/tasks", s.createTask)
	s.mux.HandleFunc("PATCH /api/v1/tasks/{id}", s.updateTask)
	s.mux.HandleFunc("DELETE /api/v1/tasks/{id}", s.cancelTask)
	s.mux.HandleFunc("GET /api/v1/reminders", s.listReminders)
	s.mux.HandleFunc("POST /api/v1/reminders", s.createReminder)
	s.mux.HandleFunc("PATCH /api/v1/reminders/{id}", s.updateReminder)
	s.mux.HandleFunc("DELETE /api/v1/reminders/{id}", s.cancelReminder)
	s.mux.HandleFunc("POST /api/v1/telegram/webhook", s.telegramWebhook)
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

type createTaskRequest struct {
	Title          string `json:"title"`
	Details        string `json:"details"`
	DueAt          string `json:"dueAt"`
	Timezone       string `json:"timezone"`
	IdempotencyKey string `json:"idempotencyKey"`
}

type updateTaskRequest struct {
	Title    *string `json:"title"`
	Details  *string `json:"details"`
	Status   *string `json:"status"`
	DueAt    *string `json:"dueAt"`
	Timezone *string `json:"timezone"`
}

type createReminderRequest struct {
	Title          string `json:"title"`
	TriggerAt      string `json:"triggerAt"`
	Timezone       string `json:"timezone"`
	RecurrenceRule string `json:"recurrenceRule"`
	Priority       string `json:"priority"`
	DeliveryMethod string `json:"deliveryMethod"`
	IdempotencyKey string `json:"idempotencyKey"`
}

type updateReminderRequest struct {
	Title          *string `json:"title"`
	TriggerAt      *string `json:"triggerAt"`
	Timezone       *string `json:"timezone"`
	RecurrenceRule *string `json:"recurrenceRule"`
	Priority       *string `json:"priority"`
	DeliveryMethod *string `json:"deliveryMethod"`
	Status         *string `json:"status"`
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

func (s *Server) listTasks(w http.ResponseWriter, r *http.Request) {
	if s.store == nil {
		writeError(w, http.StatusServiceUnavailable, "storage is not configured")
		return
	}
	userID := s.requestUserID(r)
	if err := s.store.EnsureUser(r.Context(), userID); err != nil {
		s.writeStorageError(w, err)
		return
	}
	tasks, err := s.store.ListTasks(r.Context(), userID, strings.TrimSpace(r.URL.Query().Get("status")),
		parseLimit(r.URL.Query().Get("limit"), 50))
	if err != nil {
		s.writeStorageError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"tasks": tasks})
}

func (s *Server) createTask(w http.ResponseWriter, r *http.Request) {
	if s.store == nil {
		writeError(w, http.StatusServiceUnavailable, "storage is not configured")
		return
	}
	userID := s.requestUserID(r)
	if err := s.store.EnsureUser(r.Context(), userID); err != nil {
		s.writeStorageError(w, err)
		return
	}
	var request createTaskRequest
	if err := decodeJSON(w, r, &request); err != nil {
		return
	}
	taskRecord, err := s.buildTaskRecord(userID, request)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	created, err := s.store.CreateTask(r.Context(), taskRecord, strings.TrimSpace(request.IdempotencyKey))
	if err != nil {
		s.writeStorageError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, created)
}

func (s *Server) updateTask(w http.ResponseWriter, r *http.Request) {
	if s.store == nil {
		writeError(w, http.StatusServiceUnavailable, "storage is not configured")
		return
	}
	var request updateTaskRequest
	if err := decodeJSON(w, r, &request); err != nil {
		return
	}
	update, err := s.buildTaskUpdate(request)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if update.Title == nil && update.Details == nil && update.Status == nil && !update.DueAtSet {
		writeError(w, http.StatusBadRequest, "at least one task field is required")
		return
	}
	updated, err := s.store.UpdateTask(r.Context(), s.requestUserID(r), r.PathValue("id"), update)
	if err != nil {
		s.writeTaskError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, updated)
}

func (s *Server) cancelTask(w http.ResponseWriter, r *http.Request) {
	if s.store == nil {
		writeError(w, http.StatusServiceUnavailable, "storage is not configured")
		return
	}
	if err := s.store.CancelTask(r.Context(), s.requestUserID(r), r.PathValue("id")); err != nil {
		s.writeTaskError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) listReminders(w http.ResponseWriter, r *http.Request) {
	if s.store == nil {
		writeError(w, http.StatusServiceUnavailable, "storage is not configured")
		return
	}
	userID := s.requestUserID(r)
	if err := s.store.EnsureUser(r.Context(), userID); err != nil {
		s.writeStorageError(w, err)
		return
	}
	reminders, err := s.store.ListReminders(r.Context(), userID, strings.TrimSpace(r.URL.Query().Get("status")),
		parseLimit(r.URL.Query().Get("limit"), 50))
	if err != nil {
		s.writeStorageError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"reminders": reminders})
}

func (s *Server) createReminder(w http.ResponseWriter, r *http.Request) {
	if s.store == nil {
		writeError(w, http.StatusServiceUnavailable, "storage is not configured")
		return
	}
	userID := s.requestUserID(r)
	if err := s.store.EnsureUser(r.Context(), userID); err != nil {
		s.writeStorageError(w, err)
		return
	}
	var request createReminderRequest
	if err := decodeJSON(w, r, &request); err != nil {
		return
	}
	reminderRecord, err := s.buildReminderRecord(userID, request)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	created, err := s.store.CreateReminder(r.Context(), reminderRecord, strings.TrimSpace(request.IdempotencyKey))
	if err != nil {
		s.writeStorageError(w, err)
		return
	}
	if err := s.scheduleReminder(r.Context(), created); err != nil {
		s.logger.Error("reminder scheduling failed", "reminder_id", created.ID, "error", err)
		writeError(w, http.StatusServiceUnavailable, "reminder could not be scheduled")
		return
	}
	writeJSON(w, http.StatusCreated, created)
}

func (s *Server) updateReminder(w http.ResponseWriter, r *http.Request) {
	if s.store == nil {
		writeError(w, http.StatusServiceUnavailable, "storage is not configured")
		return
	}
	var request updateReminderRequest
	if err := decodeJSON(w, r, &request); err != nil {
		return
	}
	update, err := s.buildReminderUpdate(request)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if update.Title == nil && !update.TriggerAtSet && update.Timezone == nil && !update.RecurrenceRuleSet &&
		update.Priority == nil && update.DeliveryMethod == nil && update.Status == nil {
		writeError(w, http.StatusBadRequest, "at least one reminder field is required")
		return
	}
	updated, err := s.store.UpdateReminder(r.Context(), s.requestUserID(r), r.PathValue("id"), update)
	if err != nil {
		s.writeReminderError(w, err)
		return
	}
	if updated.Status == "scheduled" {
		if err := s.scheduleReminder(r.Context(), updated); err != nil {
			s.logger.Error("reminder rescheduling failed", "reminder_id", updated.ID, "error", err)
			writeError(w, http.StatusServiceUnavailable, "reminder could not be scheduled")
			return
		}
	}
	writeJSON(w, http.StatusOK, updated)
}

func (s *Server) cancelReminder(w http.ResponseWriter, r *http.Request) {
	if s.store == nil {
		writeError(w, http.StatusServiceUnavailable, "storage is not configured")
		return
	}
	if err := s.store.CancelReminder(r.Context(), s.requestUserID(r), r.PathValue("id")); err != nil {
		s.writeReminderError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) telegramWebhook(w http.ResponseWriter, r *http.Request) {
	if !s.cfg.TelegramEnabled {
		writeError(w, http.StatusServiceUnavailable, "telegram is not enabled")
		return
	}
	if s.telegram == nil {
		writeError(w, http.StatusServiceUnavailable, "telegram bot token is not configured")
		return
	}
	if !s.telegramSecretValid(r) {
		writeError(w, http.StatusUnauthorized, "telegram webhook secret is invalid")
		return
	}
	if s.store == nil {
		writeError(w, http.StatusServiceUnavailable, "storage is not configured")
		return
	}
	var update telegram.Update
	if err := decodeJSON(w, r, &update); err != nil {
		return
	}
	if update.Message == nil {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	message := *update.Message
	if !s.telegramUserAllowed(message.From) {
		s.logger.Warn("telegram update rejected by allowlist", "telegram_user_id", telegramUserID(message.From))
		w.WriteHeader(http.StatusNoContent)
		return
	}

	userID := s.userID
	if err := s.store.EnsureUser(r.Context(), userID); err != nil {
		s.writeStorageError(w, err)
		return
	}
	conversation, err := s.telegramConversation(r.Context(), userID)
	if err != nil {
		s.writeStorageError(w, err)
		return
	}
	text, modality, err := s.telegramMessageText(r.Context(), message)
	if err != nil {
		s.logger.Warn("telegram input could not be normalized", "message_id", message.MessageID, "error", err)
		_ = s.telegram.SendMessage(r.Context(), message.Chat.ID, "Не змогла розібрати це повідомлення. Спробуй текстом або коротшим голосовим.")
		w.WriteHeader(http.StatusNoContent)
		return
	}
	response, serviceErr := s.completeTextMessage(r.Context(), userID, conversation.ID, core.ChannelTelegram, modality, text)
	if serviceErr != nil {
		s.logger.Warn("telegram turn failed", "status", serviceErr.status, "error", serviceErr.err)
		_ = s.telegram.SendMessage(r.Context(), message.Chat.ID, telegramServiceErrorText(serviceErr))
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if err := s.telegram.SendMessage(r.Context(), message.Chat.ID, truncateTelegramText(response.Assistant.Content)); err != nil {
		s.logger.Error("telegram response delivery failed", "chat_id", message.Chat.ID, "error", err)
		writeError(w, http.StatusBadGateway, "telegram response delivery failed")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) telegramSecretValid(r *http.Request) bool {
	expected := strings.TrimSpace(s.cfg.TelegramWebhookSecret)
	if expected == "" {
		return true
	}
	actual := strings.TrimSpace(r.Header.Get("X-Telegram-Bot-Api-Secret-Token"))
	return subtle.ConstantTimeCompare([]byte(actual), []byte(expected)) == 1
}

func (s *Server) telegramUserAllowed(user *telegram.User) bool {
	allowed := strings.TrimSpace(s.cfg.TelegramAllowedUserID)
	if allowed == "" {
		return true
	}
	if user == nil {
		return false
	}
	return strconv.FormatInt(user.ID, 10) == allowed
}

func telegramUserID(user *telegram.User) string {
	if user == nil {
		return ""
	}
	return strconv.FormatInt(user.ID, 10)
}

func (s *Server) telegramConversation(ctx context.Context, userID string) (storage.Conversation, error) {
	conversations, err := s.store.ListConversations(ctx, userID)
	if err != nil {
		return storage.Conversation{}, err
	}
	if len(conversations) > 0 {
		return conversations[0], nil
	}
	return s.store.CreateConversation(ctx, userID, "Telegram")
}

func (s *Server) telegramMessageText(ctx context.Context, message telegram.Message) (string, string, error) {
	text := strings.TrimSpace(message.Text)
	if text != "" {
		return text, core.ModalityText, nil
	}
	if message.Voice == nil {
		return "", "", errors.New("telegram message has no supported text or voice input")
	}
	if s.transcriber == nil {
		return "", "", errors.New("audio transcriber is not configured")
	}
	estimatedUsage := usage.EstimateRequest(s.cfg.OpenAITranscribeModel, "telegram voice transcription", 256)
	if err := s.checkBudget(ctx, s.userID, estimatedUsage.EstimatedCostUSD); err != nil {
		return "", "", err
	}
	download, err := s.telegram.DownloadVoice(ctx, message.Voice.FileID)
	if err != nil {
		return "", "", err
	}
	result, err := s.transcriber.TranscribeAudio(ctx, download.Data, download.Filename, download.ContentType, "uk")
	if err != nil {
		return "", "", err
	}
	transcript := strings.TrimSpace(result.Text)
	if transcript == "" {
		return "", "", errors.New("voice transcript is empty")
	}
	modelUsage := result.Usage
	if modelUsage.Model == "" {
		modelUsage.Model = s.cfg.OpenAITranscribeModel
	}
	if modelUsage.Feature == "" {
		modelUsage.Feature = "telegram.voice.transcription"
	}
	if modelUsage.InputTokens == 0 {
		modelUsage.InputTokens = estimatedUsage.InputTokens
	}
	if modelUsage.OutputTokens == 0 {
		modelUsage.OutputTokens = usage.EstimateTokens(transcript)
	}
	modelUsage.EstimatedCostUSD = usage.EstimateCost(
		modelUsage.Model,
		modelUsage.InputTokens,
		modelUsage.CachedInputTokens,
		modelUsage.OutputTokens,
	)
	if err := s.store.RecordUsageEvent(ctx, storage.UsageEvent{
		UserID: s.userID, Feature: modelUsage.Feature, Model: modelUsage.Model,
		InputTokens: modelUsage.InputTokens, CachedInputTokens: modelUsage.CachedInputTokens,
		OutputTokens: modelUsage.OutputTokens, EstimatedCostUSD: modelUsage.EstimatedCostUSD,
		TraceID: newTraceID(),
	}); err != nil {
		return "", "", err
	}
	return transcript, core.ModalityVoice, nil
}

func telegramServiceErrorText(err *serviceError) string {
	if err == nil {
		return "Не змогла обробити повідомлення."
	}
	switch err.status {
	case http.StatusTooManyRequests:
		return "Ліміт бюджету NOVA на сьогодні вичерпано. Я зупинила модельні виклики, щоб не створювати неконтрольовані витрати."
	case http.StatusServiceUnavailable:
		return "Сервіс NOVA тимчасово недоступний. Спробуй ще раз трохи пізніше."
	default:
		return "Не змогла отримати відповідь. Спробуй ще раз."
	}
}

func truncateTelegramText(text string) string {
	text = strings.TrimSpace(text)
	if len([]rune(text)) <= 3900 {
		return text
	}
	runes := []rune(text)
	return string(runes[:3900]) + "\n\n…"
}

func (s *Server) buildTaskRecord(userID string, request createTaskRequest) (storage.Task, error) {
	title := strings.TrimSpace(request.Title)
	if title == "" {
		return storage.Task{}, errors.New("title is required")
	}
	timezone := s.requestTimezone(request.Timezone)
	dueAt, err := parseOptionalZonedTime(request.DueAt, timezone, "dueAt")
	if err != nil {
		return storage.Task{}, err
	}
	return storage.Task{
		UserID:  userID,
		Title:   title,
		Details: strings.TrimSpace(request.Details),
		Status:  "open",
		DueAt:   dueAt,
	}, nil
}

func (s *Server) buildTaskUpdate(request updateTaskRequest) (storage.TaskUpdate, error) {
	var update storage.TaskUpdate
	if request.Title != nil {
		title := strings.TrimSpace(*request.Title)
		if title == "" {
			return storage.TaskUpdate{}, errors.New("title cannot be empty")
		}
		update.Title = &title
	}
	if request.Details != nil {
		details := strings.TrimSpace(*request.Details)
		update.Details = &details
	}
	if request.Status != nil {
		status := strings.TrimSpace(*request.Status)
		if !validTaskStatus(status) {
			return storage.TaskUpdate{}, errors.New("status must be open, done, or cancelled")
		}
		update.Status = &status
		if status == "done" || status == "cancelled" {
			now := time.Now().UTC()
			update.CompletedAt = &now
			update.CompletedAtSet = true
		}
		if status == "open" {
			update.CompletedAt = nil
			update.CompletedAtSet = true
		}
	}
	if request.DueAt != nil {
		timezone := s.cfg.Timezone
		if request.Timezone != nil {
			timezone = *request.Timezone
		}
		dueAt, err := parseOptionalZonedTime(*request.DueAt, s.requestTimezone(timezone), "dueAt")
		if err != nil {
			return storage.TaskUpdate{}, err
		}
		update.DueAt = dueAt
		update.DueAtSet = true
	}
	return update, nil
}

func (s *Server) buildReminderRecord(userID string, request createReminderRequest) (storage.Reminder, error) {
	title := strings.TrimSpace(request.Title)
	if title == "" {
		return storage.Reminder{}, errors.New("title is required")
	}
	timezone := s.requestTimezone(request.Timezone)
	triggerAt, err := parseRequiredZonedTime(request.TriggerAt, timezone, "triggerAt")
	if err != nil {
		return storage.Reminder{}, err
	}
	priority := strings.TrimSpace(request.Priority)
	if priority == "" {
		priority = "normal"
	}
	if !validPriority(priority) {
		return storage.Reminder{}, errors.New("priority must be low, normal, or high")
	}
	deliveryMethod := strings.TrimSpace(request.DeliveryMethod)
	if deliveryMethod == "" {
		deliveryMethod = "web"
	}
	if !validDeliveryMethod(deliveryMethod) {
		return storage.Reminder{}, errors.New("deliveryMethod must be web or telegram")
	}
	return storage.Reminder{
		UserID: userID, Title: title, TriggerAt: *triggerAt, Timezone: timezone,
		RecurrenceRule: strings.TrimSpace(request.RecurrenceRule),
		Priority:       priority, DeliveryMethod: deliveryMethod, Status: "scheduled",
	}, nil
}

func (s *Server) buildReminderUpdate(request updateReminderRequest) (storage.ReminderUpdate, error) {
	var update storage.ReminderUpdate
	if request.Title != nil {
		title := strings.TrimSpace(*request.Title)
		if title == "" {
			return storage.ReminderUpdate{}, errors.New("title cannot be empty")
		}
		update.Title = &title
	}
	if request.TriggerAt != nil {
		timezone := s.cfg.Timezone
		if request.Timezone != nil {
			timezone = *request.Timezone
		}
		triggerAt, err := parseRequiredZonedTime(*request.TriggerAt, s.requestTimezone(timezone), "triggerAt")
		if err != nil {
			return storage.ReminderUpdate{}, err
		}
		update.TriggerAt = triggerAt
		update.TriggerAtSet = true
	}
	if request.Timezone != nil {
		timezone := s.requestTimezone(*request.Timezone)
		update.Timezone = &timezone
	}
	if request.RecurrenceRule != nil {
		recurrenceRule := strings.TrimSpace(*request.RecurrenceRule)
		update.RecurrenceRule = &recurrenceRule
		update.RecurrenceRuleSet = true
	}
	if request.Priority != nil {
		priority := strings.TrimSpace(*request.Priority)
		if !validPriority(priority) {
			return storage.ReminderUpdate{}, errors.New("priority must be low, normal, or high")
		}
		update.Priority = &priority
	}
	if request.DeliveryMethod != nil {
		deliveryMethod := strings.TrimSpace(*request.DeliveryMethod)
		if !validDeliveryMethod(deliveryMethod) {
			return storage.ReminderUpdate{}, errors.New("deliveryMethod must be web or telegram")
		}
		update.DeliveryMethod = &deliveryMethod
	}
	if request.Status != nil {
		status := strings.TrimSpace(*request.Status)
		if !validReminderStatus(status) {
			return storage.ReminderUpdate{}, errors.New("status must be scheduled, delivered, or cancelled")
		}
		update.Status = &status
	}
	return update, nil
}

func (s *Server) scheduleReminder(ctx context.Context, reminder storage.Reminder) error {
	if s.reminderQueue == nil {
		return errors.New("reminder queue is not configured")
	}
	task, err := jobs.NewReminderDeliveryTask(reminder)
	if err != nil {
		return err
	}
	_, err = s.reminderQueue.EnqueueContext(ctx, task,
		asynq.ProcessAt(reminder.TriggerAt),
		asynq.TaskID(jobs.ReminderTaskID(reminder)),
		asynq.MaxRetry(5),
	)
	if errors.Is(err, asynq.ErrDuplicateTask) {
		return nil
	}
	if err != nil {
		return err
	}
	return s.store.CreateScheduledJob(ctx, jobs.TypeReminderDelivery, reminder.ID, reminder.TriggerAt)
}

func (s *Server) requestTimezone(value string) string {
	timezone := strings.TrimSpace(value)
	if timezone == "" {
		timezone = strings.TrimSpace(s.cfg.Timezone)
	}
	if timezone == "" {
		return "Europe/Kyiv"
	}
	return timezone
}

func parseRequiredZonedTime(value, timezone, field string) (*time.Time, error) {
	parsed, err := parseOptionalZonedTime(value, timezone, field)
	if err != nil {
		return nil, err
	}
	if parsed == nil {
		return nil, fmt.Errorf("%s is required", field)
	}
	return parsed, nil
}

func parseOptionalZonedTime(value, timezone, field string) (*time.Time, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil, nil
	}
	if parsed, err := time.Parse(time.RFC3339, value); err == nil {
		utc := parsed.UTC()
		return &utc, nil
	}
	location, err := time.LoadLocation(timezone)
	if err != nil {
		return nil, fmt.Errorf("timezone %q is invalid", timezone)
	}
	layouts := []string{
		"2006-01-02T15:04",
		"2006-01-02T15:04:05",
		"2006-01-02 15:04",
		"2006-01-02 15:04:05",
	}
	for _, layout := range layouts {
		if parsed, err := time.ParseInLocation(layout, value, location); err == nil {
			utc := parsed.UTC()
			return &utc, nil
		}
	}
	return nil, fmt.Errorf("%s must be RFC3339 or a local datetime", field)
}

func validTaskStatus(value string) bool {
	switch value {
	case "open", "done", "cancelled":
		return true
	default:
		return false
	}
}

func validReminderStatus(value string) bool {
	switch value {
	case "scheduled", "delivered", "cancelled":
		return true
	default:
		return false
	}
}

func validPriority(value string) bool {
	switch value {
	case "low", "normal", "high":
		return true
	default:
		return false
	}
}

func validDeliveryMethod(value string) bool {
	switch value {
	case "web", "telegram":
		return true
	default:
		return false
	}
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
	var request createMessageRequest
	if err := decodeJSON(w, r, &request); err != nil {
		return
	}
	request.Text = strings.TrimSpace(request.Text)
	if request.Text == "" {
		writeError(w, http.StatusBadRequest, "text is required")
		return
	}

	response, serviceErr := s.completeTextMessage(r.Context(), s.requestUserID(r), r.PathValue("id"), "web", core.ModalityText, request.Text)
	if serviceErr != nil {
		s.writeServiceError(w, serviceErr)
		return
	}
	writeJSON(w, http.StatusCreated, response)
}

type serviceError struct {
	status  int
	message string
	err     error
}

func newServiceError(status int, message string, err error) *serviceError {
	return &serviceError{status: status, message: message, err: err}
}

func (s *Server) completeTextMessage(ctx context.Context, userID, conversationID, channel, modality, text string) (createMessageResponse, *serviceError) {
	conversation, err := s.store.GetConversation(ctx, userID, conversationID)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			return createMessageResponse{}, newServiceError(http.StatusNotFound, "conversation not found", err)
		}
		return createMessageResponse{}, newServiceError(http.StatusInternalServerError, "storage request failed", err)
	}

	traceID := newTraceID()
	userMessage, err := s.store.AddMessage(ctx, storage.Message{
		ConversationID: conversationID,
		UserID:         userID,
		Role:           "user",
		Channel:        channel,
		Modality:       modality,
		Content:        text,
		TraceID:        traceID,
	})
	if err != nil {
		return createMessageResponse{}, newServiceError(http.StatusInternalServerError, "storage request failed", err)
	}

	route := agent.RouteRequest(agent.Request{Text: text}, s.models)
	assistantText := deterministicResponse(route.Intent)
	var modelUsage *core.NovaUsage
	modelStartedAt := time.Now()
	if route.Mode == "model" {
		if s.responder == nil {
			return createMessageResponse{}, newServiceError(http.StatusServiceUnavailable, "OPENAI_API_KEY is not configured", nil)
		}
		messages, listErr := s.store.ListMessages(ctx, userID, conversationID, 100)
		if listErr != nil {
			return createMessageResponse{}, newServiceError(http.StatusInternalServerError, "storage request failed", listErr)
		}
		modelInput, listErr := s.buildModelInput(ctx, userID, conversationID, messages)
		if listErr != nil {
			return createMessageResponse{}, newServiceError(http.StatusInternalServerError, "storage request failed", listErr)
		}
		estimatedUsage := usage.EstimateRequest(route.Model, agent.SystemProfile+"\n"+modelInput, 512)
		if err := s.checkBudget(ctx, userID, estimatedUsage.EstimatedCostUSD); err != nil {
			if errors.Is(err, errBudgetExceeded) {
				return createMessageResponse{}, newServiceError(http.StatusTooManyRequests, "NOVA budget limit reached; try again after the budget period resets", err)
			}
			s.logger.Error("budget check failed", "trace_id", traceID, "error", err)
			return createMessageResponse{}, newServiceError(http.StatusServiceUnavailable, "budget check is temporarily unavailable", err)
		}
		if usageResponder, ok := s.responder.(UsageResponder); ok {
			var result core.ModelResponse
			result, err = usageResponder.RespondWithModelUsage(ctx, route.Model, agent.SystemProfile, modelInput)
			assistantText = result.Text
			modelUsage = &result.Usage
		} else {
			assistantText, err = s.responder.RespondWithModel(ctx, route.Model, agent.SystemProfile, modelInput)
			estimatedUsage = usage.EstimateResponse(route.Model, modelInput, assistantText)
			modelUsage = &estimatedUsage
		}
		if err != nil {
			s.logger.Error("model response failed", "trace_id", traceID, "error", err)
			return createMessageResponse{}, newServiceError(http.StatusBadGateway, "model response failed", err)
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
		if err := s.store.RecordUsageEvent(ctx, storage.UsageEvent{
			UserID: userID, Feature: modelUsage.Feature, Model: modelUsage.Model,
			InputTokens: modelUsage.InputTokens, CachedInputTokens: modelUsage.CachedInputTokens,
			OutputTokens: modelUsage.OutputTokens, EstimatedCostUSD: modelUsage.EstimatedCostUSD,
			LatencyMS: int(time.Since(modelStartedAt) / time.Millisecond),
			TraceID:   traceID,
		}); err != nil {
			s.logger.Error("usage event failed", "trace_id", traceID, "error", err)
			return createMessageResponse{}, newServiceError(http.StatusServiceUnavailable, "usage accounting is temporarily unavailable", err)
		}
	}

	assistantMessage, err := s.store.AddMessage(ctx, storage.Message{
		ConversationID: conversationID,
		UserID:         userID,
		Role:           "assistant",
		Channel:        channel,
		Modality:       "text",
		Content:        assistantText,
		TraceID:        traceID,
	})
	if err != nil {
		return createMessageResponse{}, newServiceError(http.StatusInternalServerError, "storage request failed", err)
	}
	s.refreshConversationSummary(ctx, userID, conversationID)
	return createMessageResponse{
		Conversation: conversation,
		UserMessage:  userMessage,
		Assistant:    assistantMessage,
		Route: routeResponse{
			Mode: stringOrEmpty(route.Mode), Intent: stringOrEmpty(route.Intent),
			Tier: string(route.Tier), Model: stringOrEmpty(route.Model),
		},
		Usage: modelUsage,
	}, nil
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

func (s *Server) writeTaskError(w http.ResponseWriter, err error) {
	if errors.Is(err, storage.ErrNotFound) {
		writeError(w, http.StatusNotFound, "task not found")
		return
	}
	s.writeStorageError(w, err)
}

func (s *Server) writeReminderError(w http.ResponseWriter, err error) {
	if errors.Is(err, storage.ErrNotFound) {
		writeError(w, http.StatusNotFound, "reminder not found")
		return
	}
	s.writeStorageError(w, err)
}

func (s *Server) writeServiceError(w http.ResponseWriter, err *serviceError) {
	if err == nil {
		return
	}
	if err.err != nil && err.status >= http.StatusInternalServerError {
		s.logger.Error("service request failed", "status", err.status, "error", err.err)
	}
	writeError(w, err.status, err.message)
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
		return "Команду нагадування розпізнано. Створити точне нагадування вже можна у вкладці «Задачі»."
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
