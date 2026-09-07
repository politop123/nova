package storage

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var ErrNotFound = errors.New("record not found")

type Store struct {
	db *pgxpool.Pool
}

func New(db *pgxpool.Pool) *Store {
	return &Store{db: db}
}

type Conversation struct {
	ID         string    `json:"id"`
	UserID     string    `json:"userId"`
	Title      string    `json:"title,omitempty"`
	ProjectKey string    `json:"projectKey,omitempty"`
	CreatedAt  time.Time `json:"createdAt"`
	UpdatedAt  time.Time `json:"updatedAt"`
}

type Message struct {
	ID             string    `json:"id"`
	ConversationID string    `json:"conversationId"`
	SessionID      string    `json:"sessionId,omitempty"`
	UserID         string    `json:"userId"`
	Role           string    `json:"role"`
	Channel        string    `json:"channel"`
	Modality       string    `json:"modality"`
	Content        string    `json:"content"`
	TraceID        string    `json:"traceId"`
	CreatedAt      time.Time `json:"createdAt"`
}

type ConversationSummary struct {
	ConversationID string    `json:"conversationId"`
	Summary        string    `json:"summary"`
	UpdatedAt      time.Time `json:"updatedAt"`
}

type Memory struct {
	ID              string     `json:"id"`
	UserID          string     `json:"userId"`
	Kind            string     `json:"kind"`
	Content         string     `json:"content"`
	Confidence      float64    `json:"confidence"`
	SourceMessageID string     `json:"sourceMessageId,omitempty"`
	ProjectKey      string     `json:"projectKey,omitempty"`
	ExpiresAt       *time.Time `json:"expiresAt,omitempty"`
	CreatedAt       time.Time  `json:"createdAt"`
	UpdatedAt       time.Time  `json:"updatedAt"`
}

type MemoryUpdate struct {
	Kind       *string
	Content    *string
	Confidence *float64
	ProjectKey *string
	ExpiresAt  *time.Time
}

type Task struct {
	ID          string     `json:"id"`
	UserID      string     `json:"userId"`
	Title       string     `json:"title"`
	Details     string     `json:"details,omitempty"`
	Status      string     `json:"status"`
	DueAt       *time.Time `json:"dueAt,omitempty"`
	CompletedAt *time.Time `json:"completedAt,omitempty"`
	CreatedAt   time.Time  `json:"createdAt"`
	UpdatedAt   time.Time  `json:"updatedAt"`
}

type TaskUpdate struct {
	Title          *string
	Details        *string
	Status         *string
	DueAt          *time.Time
	DueAtSet       bool
	CompletedAt    *time.Time
	CompletedAtSet bool
}

type Reminder struct {
	ID             string    `json:"id"`
	UserID         string    `json:"userId"`
	Title          string    `json:"title"`
	TriggerAt      time.Time `json:"triggerAt"`
	Timezone       string    `json:"timezone"`
	RecurrenceRule string    `json:"recurrenceRule,omitempty"`
	Priority       string    `json:"priority"`
	DeliveryMethod string    `json:"deliveryMethod"`
	Status         string    `json:"status"`
	CreatedAt      time.Time `json:"createdAt"`
	UpdatedAt      time.Time `json:"updatedAt"`
}

type ReminderUpdate struct {
	Title             *string
	TriggerAt         *time.Time
	TriggerAtSet      bool
	Timezone          *string
	RecurrenceRule    *string
	RecurrenceRuleSet bool
	Priority          *string
	DeliveryMethod    *string
	Status            *string
}

type UsageEvent struct {
	UserID            string
	Feature           string
	Model             string
	InputTokens       int
	CachedInputTokens int
	OutputTokens      int
	EstimatedCostUSD  float64
	LatencyMS         int
	TraceID           string
	CreatedAt         time.Time
}

func (s *Store) EnsureUser(ctx context.Context, userID string) error {
	if s == nil || s.db == nil {
		return errors.New("database is not configured")
	}
	_, err := s.db.Exec(ctx, `
		INSERT INTO users (id, external_subject)
		VALUES ($1::uuid, $1)
		ON CONFLICT (id) DO UPDATE SET updated_at = now()
	`, userID)
	if err != nil {
		return fmt.Errorf("ensure user: %w", err)
	}
	_, err = s.db.Exec(ctx, `
		INSERT INTO user_profiles (user_id)
		VALUES ($1::uuid)
		ON CONFLICT (user_id) DO NOTHING
	`, userID)
	if err != nil {
		return fmt.Errorf("ensure user profile: %w", err)
	}
	return nil
}

func (s *Store) CreateConversation(ctx context.Context, userID, title string) (Conversation, error) {
	var result Conversation
	err := s.db.QueryRow(ctx, `
		INSERT INTO conversations (user_id, title)
		VALUES ($1::uuid, NULLIF($2, ''))
		RETURNING id::text, user_id::text, COALESCE(title, ''), COALESCE(project_key, ''), created_at, updated_at
	`, userID, title).Scan(
		&result.ID, &result.UserID, &result.Title, &result.ProjectKey, &result.CreatedAt, &result.UpdatedAt,
	)
	if err != nil {
		return Conversation{}, fmt.Errorf("create conversation: %w", err)
	}
	return result, nil
}

func (s *Store) ListConversations(ctx context.Context, userID string) ([]Conversation, error) {
	rows, err := s.db.Query(ctx, `
		SELECT id::text, user_id::text, COALESCE(title, ''), COALESCE(project_key, ''), created_at, updated_at
		FROM conversations
		WHERE user_id = $1::uuid
		ORDER BY updated_at DESC
		LIMIT 100
	`, userID)
	if err != nil {
		return nil, fmt.Errorf("list conversations: %w", err)
	}
	defer rows.Close()

	result := make([]Conversation, 0)
	for rows.Next() {
		var conversation Conversation
		if err := rows.Scan(
			&conversation.ID,
			&conversation.UserID,
			&conversation.Title,
			&conversation.ProjectKey,
			&conversation.CreatedAt,
			&conversation.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan conversation: %w", err)
		}
		result = append(result, conversation)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list conversations rows: %w", err)
	}
	return result, nil
}

func (s *Store) GetConversation(ctx context.Context, userID, conversationID string) (Conversation, error) {
	var result Conversation
	err := s.db.QueryRow(ctx, `
		SELECT id::text, user_id::text, COALESCE(title, ''), COALESCE(project_key, ''), created_at, updated_at
		FROM conversations
		WHERE id = $1::uuid AND user_id = $2::uuid
	`, conversationID, userID).Scan(
		&result.ID, &result.UserID, &result.Title, &result.ProjectKey, &result.CreatedAt, &result.UpdatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return Conversation{}, ErrNotFound
	}
	if err != nil {
		return Conversation{}, fmt.Errorf("get conversation: %w", err)
	}
	return result, nil
}

func (s *Store) GetConversationSummary(ctx context.Context, userID, conversationID string) (ConversationSummary, error) {
	var result ConversationSummary
	err := s.db.QueryRow(ctx, `
		SELECT cs.conversation_id::text, cs.summary, cs.updated_at
		FROM conversation_summaries cs
		JOIN conversations c ON c.id = cs.conversation_id
		WHERE cs.conversation_id = $1::uuid AND c.user_id = $2::uuid
	`, conversationID, userID).Scan(&result.ConversationID, &result.Summary, &result.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return ConversationSummary{}, ErrNotFound
	}
	if err != nil {
		return ConversationSummary{}, fmt.Errorf("get conversation summary: %w", err)
	}
	return result, nil
}

func (s *Store) UpsertConversationSummary(ctx context.Context, userID, conversationID, summary string) error {
	if s == nil || s.db == nil {
		return errors.New("database is not configured")
	}
	_, err := s.db.Exec(ctx, `
		INSERT INTO conversation_summaries (conversation_id, summary)
		SELECT $1::uuid, $3
		WHERE EXISTS (
			SELECT 1 FROM conversations WHERE id = $1::uuid AND user_id = $2::uuid
		)
		ON CONFLICT (conversation_id) DO UPDATE SET summary = EXCLUDED.summary, updated_at = now()
	`, conversationID, userID, summary)
	if err != nil {
		return fmt.Errorf("upsert conversation summary: %w", err)
	}
	return nil
}

func (s *Store) CreateMemory(ctx context.Context, memory Memory) (Memory, error) {
	if s == nil || s.db == nil {
		return Memory{}, errors.New("database is not configured")
	}
	var result Memory
	err := s.db.QueryRow(ctx, `
		INSERT INTO memories (
			user_id, kind, content, confidence, source_message_id, project_key, expires_at
		)
		VALUES (
			$1::uuid, $2, $3, $4, NULLIF($5, '')::uuid, NULLIF($6, ''), $7
		)
		RETURNING id::text, user_id::text, kind, content, confidence::double precision,
			COALESCE(source_message_id::text, ''), COALESCE(project_key, ''), expires_at, created_at, updated_at
	`, memory.UserID, memory.Kind, memory.Content, memory.Confidence, memory.SourceMessageID,
		memory.ProjectKey, memory.ExpiresAt).Scan(
		&result.ID, &result.UserID, &result.Kind, &result.Content, &result.Confidence,
		&result.SourceMessageID, &result.ProjectKey, &result.ExpiresAt, &result.CreatedAt, &result.UpdatedAt,
	)
	if err != nil {
		return Memory{}, fmt.Errorf("create memory: %w", err)
	}
	return result, nil
}

func (s *Store) ListMemories(ctx context.Context, userID string, limit int) ([]Memory, error) {
	if s == nil || s.db == nil {
		return nil, errors.New("database is not configured")
	}
	if limit < 1 || limit > 100 {
		limit = 50
	}
	return s.queryMemories(ctx, `
		SELECT id::text, user_id::text, kind, content, confidence::double precision,
			COALESCE(source_message_id::text, ''), COALESCE(project_key, ''), expires_at, created_at, updated_at
		FROM memories
		WHERE user_id = $1::uuid AND deleted_at IS NULL
			AND (expires_at IS NULL OR expires_at > now())
		ORDER BY updated_at DESC
		LIMIT $2
	`, userID, limit)
}

func (s *Store) SearchMemories(ctx context.Context, userID, query string, limit int) ([]Memory, error) {
	if s == nil || s.db == nil {
		return nil, errors.New("database is not configured")
	}
	if limit < 1 || limit > 100 {
		limit = 8
	}
	query = strings.TrimSpace(query)
	if query == "" {
		return []Memory{}, nil
	}
	return s.queryMemories(ctx, `
		SELECT id::text, user_id::text, kind, content, confidence::double precision,
			COALESCE(source_message_id::text, ''), COALESCE(project_key, ''), expires_at, created_at, updated_at
		FROM memories
		WHERE user_id = $1::uuid AND deleted_at IS NULL
			AND (expires_at IS NULL OR expires_at > now())
			AND to_tsvector('simple', kind || ' ' || content) @@ plainto_tsquery('simple', $2)
		ORDER BY ts_rank(to_tsvector('simple', kind || ' ' || content), plainto_tsquery('simple', $2)) DESC,
			confidence DESC, updated_at DESC
		LIMIT $3
	`, userID, query, limit)
}

func (s *Store) queryMemories(ctx context.Context, query string, args ...any) ([]Memory, error) {
	rows, err := s.db.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("query memories: %w", err)
	}
	defer rows.Close()
	result := make([]Memory, 0)
	for rows.Next() {
		var memory Memory
		if err := rows.Scan(
			&memory.ID, &memory.UserID, &memory.Kind, &memory.Content, &memory.Confidence,
			&memory.SourceMessageID, &memory.ProjectKey, &memory.ExpiresAt, &memory.CreatedAt, &memory.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan memory: %w", err)
		}
		result = append(result, memory)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("query memories rows: %w", err)
	}
	return result, nil
}

func (s *Store) UpdateMemory(ctx context.Context, userID, memoryID string, update MemoryUpdate) (Memory, error) {
	if s == nil || s.db == nil {
		return Memory{}, errors.New("database is not configured")
	}
	var result Memory
	err := s.db.QueryRow(ctx, `
		UPDATE memories
		SET kind = COALESCE($3, kind),
			content = COALESCE($4, content),
			confidence = COALESCE($5, confidence),
			project_key = COALESCE($6, project_key),
			expires_at = COALESCE($7, expires_at),
			updated_at = now()
		WHERE id = $1::uuid AND user_id = $2::uuid AND deleted_at IS NULL
		RETURNING id::text, user_id::text, kind, content, confidence::double precision,
			COALESCE(source_message_id::text, ''), COALESCE(project_key, ''), expires_at, created_at, updated_at
	`, memoryID, userID, update.Kind, update.Content, update.Confidence, update.ProjectKey, update.ExpiresAt).Scan(
		&result.ID, &result.UserID, &result.Kind, &result.Content, &result.Confidence,
		&result.SourceMessageID, &result.ProjectKey, &result.ExpiresAt, &result.CreatedAt, &result.UpdatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return Memory{}, ErrNotFound
	}
	if err != nil {
		return Memory{}, fmt.Errorf("update memory: %w", err)
	}
	return result, nil
}

func (s *Store) DeleteMemory(ctx context.Context, userID, memoryID string) error {
	if s == nil || s.db == nil {
		return errors.New("database is not configured")
	}
	command, err := s.db.Exec(ctx, `
		UPDATE memories SET deleted_at = now(), updated_at = now()
		WHERE id = $1::uuid AND user_id = $2::uuid AND deleted_at IS NULL
	`, memoryID, userID)
	if err != nil {
		return fmt.Errorf("delete memory: %w", err)
	}
	if command.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) CreateTask(ctx context.Context, task Task, idempotencyKey string) (Task, error) {
	if s == nil || s.db == nil {
		return Task{}, errors.New("database is not configured")
	}
	var result Task
	err := s.db.QueryRow(ctx, `
		INSERT INTO tasks (user_id, title, details, status, due_at, idempotency_key)
		VALUES ($1::uuid, $2, NULLIF($3, ''), COALESCE(NULLIF($4, ''), 'open'), $5, NULLIF($6, ''))
		ON CONFLICT (user_id, idempotency_key) DO UPDATE SET updated_at = tasks.updated_at
		RETURNING id::text, user_id::text, title, COALESCE(details, ''), status, due_at, completed_at,
			created_at, updated_at
	`, task.UserID, task.Title, task.Details, task.Status, task.DueAt, idempotencyKey).Scan(
		&result.ID, &result.UserID, &result.Title, &result.Details, &result.Status, &result.DueAt,
		&result.CompletedAt, &result.CreatedAt, &result.UpdatedAt,
	)
	if err != nil {
		return Task{}, fmt.Errorf("create task: %w", err)
	}
	return result, nil
}

func (s *Store) ListTasks(ctx context.Context, userID, status string, limit int) ([]Task, error) {
	if s == nil || s.db == nil {
		return nil, errors.New("database is not configured")
	}
	if limit < 1 || limit > 100 {
		limit = 50
	}
	rows, err := s.db.Query(ctx, `
		SELECT id::text, user_id::text, title, COALESCE(details, ''), status, due_at, completed_at,
			created_at, updated_at
		FROM tasks
		WHERE user_id = $1::uuid AND (NULLIF($2, '') IS NULL OR status = $2)
		ORDER BY
			CASE status WHEN 'open' THEN 0 WHEN 'done' THEN 1 ELSE 2 END,
			due_at ASC NULLS LAST,
			updated_at DESC
		LIMIT $3
	`, userID, status, limit)
	if err != nil {
		return nil, fmt.Errorf("list tasks: %w", err)
	}
	defer rows.Close()
	result := make([]Task, 0)
	for rows.Next() {
		var task Task
		if err := rows.Scan(
			&task.ID, &task.UserID, &task.Title, &task.Details, &task.Status, &task.DueAt,
			&task.CompletedAt, &task.CreatedAt, &task.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan task: %w", err)
		}
		result = append(result, task)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list tasks rows: %w", err)
	}
	return result, nil
}

func (s *Store) UpdateTask(ctx context.Context, userID, taskID string, update TaskUpdate) (Task, error) {
	if s == nil || s.db == nil {
		return Task{}, errors.New("database is not configured")
	}
	var result Task
	err := s.db.QueryRow(ctx, `
		UPDATE tasks
		SET title = COALESCE($3, title),
			details = CASE WHEN $4 THEN NULLIF($5, '') ELSE details END,
			status = COALESCE($6, status),
			due_at = CASE WHEN $7 THEN $8 ELSE due_at END,
			completed_at = CASE WHEN $9 THEN $10 ELSE completed_at END,
			updated_at = now()
		WHERE id = $1::uuid AND user_id = $2::uuid
		RETURNING id::text, user_id::text, title, COALESCE(details, ''), status, due_at, completed_at,
			created_at, updated_at
	`, taskID, userID, update.Title, update.Details != nil, stringValue(update.Details), update.Status,
		update.DueAtSet, update.DueAt, update.CompletedAtSet, update.CompletedAt).Scan(
		&result.ID, &result.UserID, &result.Title, &result.Details, &result.Status, &result.DueAt,
		&result.CompletedAt, &result.CreatedAt, &result.UpdatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return Task{}, ErrNotFound
	}
	if err != nil {
		return Task{}, fmt.Errorf("update task: %w", err)
	}
	return result, nil
}

func (s *Store) CancelTask(ctx context.Context, userID, taskID string) error {
	now := time.Now().UTC()
	status := "cancelled"
	_, err := s.UpdateTask(ctx, userID, taskID, TaskUpdate{
		Status: &status, CompletedAt: &now, CompletedAtSet: true,
	})
	return err
}

func (s *Store) CreateReminder(ctx context.Context, reminder Reminder, idempotencyKey string) (Reminder, error) {
	if s == nil || s.db == nil {
		return Reminder{}, errors.New("database is not configured")
	}
	var result Reminder
	err := s.db.QueryRow(ctx, `
		INSERT INTO reminders (
			user_id, title, trigger_at, timezone, recurrence_rule, priority, delivery_method, status, idempotency_key
		)
		VALUES (
			$1::uuid, $2, $3, $4, NULLIF($5, ''), COALESCE(NULLIF($6, ''), 'normal'),
			COALESCE(NULLIF($7, ''), 'web'), COALESCE(NULLIF($8, ''), 'scheduled'), NULLIF($9, '')
		)
		ON CONFLICT (user_id, idempotency_key) DO UPDATE SET updated_at = reminders.updated_at
		RETURNING id::text, user_id::text, title, trigger_at, timezone, COALESCE(recurrence_rule, ''),
			priority, delivery_method, status, created_at, updated_at
	`, reminder.UserID, reminder.Title, reminder.TriggerAt, reminder.Timezone, reminder.RecurrenceRule,
		reminder.Priority, reminder.DeliveryMethod, reminder.Status, idempotencyKey).Scan(
		&result.ID, &result.UserID, &result.Title, &result.TriggerAt, &result.Timezone,
		&result.RecurrenceRule, &result.Priority, &result.DeliveryMethod, &result.Status,
		&result.CreatedAt, &result.UpdatedAt,
	)
	if err != nil {
		return Reminder{}, fmt.Errorf("create reminder: %w", err)
	}
	return result, nil
}

func (s *Store) ListReminders(ctx context.Context, userID, status string, limit int) ([]Reminder, error) {
	if s == nil || s.db == nil {
		return nil, errors.New("database is not configured")
	}
	if limit < 1 || limit > 100 {
		limit = 50
	}
	rows, err := s.db.Query(ctx, `
		SELECT id::text, user_id::text, title, trigger_at, timezone, COALESCE(recurrence_rule, ''),
			priority, delivery_method, status, created_at, updated_at
		FROM reminders
		WHERE user_id = $1::uuid AND (NULLIF($2, '') IS NULL OR status = $2)
		ORDER BY
			CASE status WHEN 'scheduled' THEN 0 WHEN 'delivered' THEN 1 ELSE 2 END,
			trigger_at ASC,
			updated_at DESC
		LIMIT $3
	`, userID, status, limit)
	if err != nil {
		return nil, fmt.Errorf("list reminders: %w", err)
	}
	defer rows.Close()
	result := make([]Reminder, 0)
	for rows.Next() {
		var reminder Reminder
		if err := rows.Scan(
			&reminder.ID, &reminder.UserID, &reminder.Title, &reminder.TriggerAt, &reminder.Timezone,
			&reminder.RecurrenceRule, &reminder.Priority, &reminder.DeliveryMethod, &reminder.Status,
			&reminder.CreatedAt, &reminder.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan reminder: %w", err)
		}
		result = append(result, reminder)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list reminders rows: %w", err)
	}
	return result, nil
}

func (s *Store) GetReminder(ctx context.Context, userID, reminderID string) (Reminder, error) {
	if s == nil || s.db == nil {
		return Reminder{}, errors.New("database is not configured")
	}
	var reminder Reminder
	err := s.db.QueryRow(ctx, `
		SELECT id::text, user_id::text, title, trigger_at, timezone, COALESCE(recurrence_rule, ''),
			priority, delivery_method, status, created_at, updated_at
		FROM reminders
		WHERE id = $1::uuid AND user_id = $2::uuid
	`, reminderID, userID).Scan(
		&reminder.ID, &reminder.UserID, &reminder.Title, &reminder.TriggerAt, &reminder.Timezone,
		&reminder.RecurrenceRule, &reminder.Priority, &reminder.DeliveryMethod, &reminder.Status,
		&reminder.CreatedAt, &reminder.UpdatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return Reminder{}, ErrNotFound
	}
	if err != nil {
		return Reminder{}, fmt.Errorf("get reminder: %w", err)
	}
	return reminder, nil
}

func (s *Store) UpdateReminder(ctx context.Context, userID, reminderID string, update ReminderUpdate) (Reminder, error) {
	if s == nil || s.db == nil {
		return Reminder{}, errors.New("database is not configured")
	}
	var result Reminder
	err := s.db.QueryRow(ctx, `
		UPDATE reminders
		SET title = COALESCE($3, title),
			trigger_at = CASE WHEN $4 THEN $5 ELSE trigger_at END,
			timezone = COALESCE($6, timezone),
			recurrence_rule = CASE WHEN $7 THEN NULLIF($8, '') ELSE recurrence_rule END,
			priority = COALESCE($9, priority),
			delivery_method = COALESCE($10, delivery_method),
			status = COALESCE($11, status),
			updated_at = now()
		WHERE id = $1::uuid AND user_id = $2::uuid
		RETURNING id::text, user_id::text, title, trigger_at, timezone, COALESCE(recurrence_rule, ''),
			priority, delivery_method, status, created_at, updated_at
	`, reminderID, userID, update.Title, update.TriggerAtSet, update.TriggerAt, update.Timezone,
		update.RecurrenceRuleSet, stringValue(update.RecurrenceRule), update.Priority, update.DeliveryMethod,
		update.Status).Scan(
		&result.ID, &result.UserID, &result.Title, &result.TriggerAt, &result.Timezone,
		&result.RecurrenceRule, &result.Priority, &result.DeliveryMethod, &result.Status,
		&result.CreatedAt, &result.UpdatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return Reminder{}, ErrNotFound
	}
	if err != nil {
		return Reminder{}, fmt.Errorf("update reminder: %w", err)
	}
	return result, nil
}

func (s *Store) CancelReminder(ctx context.Context, userID, reminderID string) error {
	status := "cancelled"
	_, err := s.UpdateReminder(ctx, userID, reminderID, ReminderUpdate{Status: &status})
	return err
}

func (s *Store) CreateScheduledJob(ctx context.Context, jobType, entityID string, runAt time.Time) error {
	if s == nil || s.db == nil {
		return errors.New("database is not configured")
	}
	_, err := s.db.Exec(ctx, `
		INSERT INTO scheduled_jobs (job_type, entity_id, run_at)
		VALUES ($1, NULLIF($2, '')::uuid, $3)
	`, jobType, entityID, runAt)
	if err != nil {
		return fmt.Errorf("create scheduled job: %w", err)
	}
	return nil
}

func (s *Store) DeliverReminder(ctx context.Context, userID, reminderID string) (Reminder, bool, error) {
	if s == nil || s.db == nil {
		return Reminder{}, false, errors.New("database is not configured")
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return Reminder{}, false, fmt.Errorf("begin reminder delivery: %w", err)
	}
	defer tx.Rollback(ctx)

	var reminder Reminder
	err = tx.QueryRow(ctx, `
		SELECT id::text, user_id::text, title, trigger_at, timezone, COALESCE(recurrence_rule, ''),
			priority, delivery_method, status, created_at, updated_at
		FROM reminders
		WHERE id = $1::uuid AND user_id = $2::uuid
		FOR UPDATE
	`, reminderID, userID).Scan(
		&reminder.ID, &reminder.UserID, &reminder.Title, &reminder.TriggerAt, &reminder.Timezone,
		&reminder.RecurrenceRule, &reminder.Priority, &reminder.DeliveryMethod, &reminder.Status,
		&reminder.CreatedAt, &reminder.UpdatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return Reminder{}, false, ErrNotFound
	}
	if err != nil {
		return Reminder{}, false, fmt.Errorf("get reminder for delivery: %w", err)
	}
	if reminder.Status != "scheduled" || reminder.TriggerAt.After(time.Now().UTC().Add(30*time.Second)) {
		if err := tx.Commit(ctx); err != nil {
			return Reminder{}, false, fmt.Errorf("commit skipped reminder delivery: %w", err)
		}
		return reminder, false, nil
	}

	idempotencyKey := "reminder:" + reminder.ID + ":delivery"
	_, err = tx.Exec(ctx, `
		INSERT INTO notifications (user_id, channel, title, body, status, idempotency_key, sent_at)
		VALUES ($1::uuid, $2, $3, $4, 'sent', $5, now())
		ON CONFLICT (user_id, idempotency_key) DO UPDATE
		SET status = 'sent', sent_at = COALESCE(notifications.sent_at, now())
	`, reminder.UserID, reminder.DeliveryMethod, reminder.Title, reminder.Title, idempotencyKey)
	if err != nil {
		return Reminder{}, false, fmt.Errorf("record reminder notification: %w", err)
	}
	err = tx.QueryRow(ctx, `
		UPDATE reminders
		SET status = 'delivered', updated_at = now()
		WHERE id = $1::uuid AND user_id = $2::uuid
		RETURNING id::text, user_id::text, title, trigger_at, timezone, COALESCE(recurrence_rule, ''),
			priority, delivery_method, status, created_at, updated_at
	`, reminder.ID, reminder.UserID).Scan(
		&reminder.ID, &reminder.UserID, &reminder.Title, &reminder.TriggerAt, &reminder.Timezone,
		&reminder.RecurrenceRule, &reminder.Priority, &reminder.DeliveryMethod, &reminder.Status,
		&reminder.CreatedAt, &reminder.UpdatedAt,
	)
	if err != nil {
		return Reminder{}, false, fmt.Errorf("mark reminder delivered: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return Reminder{}, false, fmt.Errorf("commit reminder delivery: %w", err)
	}
	return reminder, true, nil
}

func (s *Store) AddMessage(ctx context.Context, message Message) (Message, error) {
	var result Message
	err := s.db.QueryRow(ctx, `
		INSERT INTO messages (
			conversation_id, session_id, user_id, role, channel, modality, content, trace_id
		)
		VALUES (
			$1::uuid, NULLIF($2, '')::uuid, $3::uuid, $4, $5, $6, $7, $8
		)
		RETURNING id::text, conversation_id::text, COALESCE(session_id::text, ''), user_id::text,
			role, channel, modality, content, trace_id, created_at
	`, message.ConversationID, message.SessionID, message.UserID, message.Role, message.Channel, message.Modality, message.Content, message.TraceID).Scan(
		&result.ID,
		&result.ConversationID,
		&result.SessionID,
		&result.UserID,
		&result.Role,
		&result.Channel,
		&result.Modality,
		&result.Content,
		&result.TraceID,
		&result.CreatedAt,
	)
	if err != nil {
		return Message{}, fmt.Errorf("add message: %w", err)
	}
	return result, nil
}

func (s *Store) ListMessages(ctx context.Context, userID, conversationID string, limit int) ([]Message, error) {
	if limit < 1 || limit > 100 {
		limit = 20
	}
	rows, err := s.db.Query(ctx, `
		SELECT m.id::text, m.conversation_id::text, COALESCE(m.session_id::text, ''), m.user_id::text,
			m.role, m.channel, m.modality, m.content, m.trace_id, m.created_at
		FROM messages m
		JOIN conversations c ON c.id = m.conversation_id
		WHERE m.user_id = $1::uuid AND c.id = $2::uuid
		ORDER BY m.created_at DESC
		LIMIT $3
	`, userID, conversationID, limit)
	if err != nil {
		return nil, fmt.Errorf("list messages: %w", err)
	}
	defer rows.Close()

	result := make([]Message, 0)
	for rows.Next() {
		var message Message
		if err := rows.Scan(
			&message.ID,
			&message.ConversationID,
			&message.SessionID,
			&message.UserID,
			&message.Role,
			&message.Channel,
			&message.Modality,
			&message.Content,
			&message.TraceID,
			&message.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan message: %w", err)
		}
		result = append(result, message)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list messages rows: %w", err)
	}
	for left, right := 0, len(result)-1; left < right; left, right = left+1, right-1 {
		result[left], result[right] = result[right], result[left]
	}
	return result, nil
}

func (s *Store) RecordUsageEvent(ctx context.Context, event UsageEvent) error {
	if s == nil || s.db == nil {
		return errors.New("database is not configured")
	}
	_, err := s.db.Exec(ctx, `
		INSERT INTO usage_events (
			user_id, feature, model, input_tokens, cached_input_tokens, output_tokens,
			estimated_cost_usd, latency_ms, trace_id
		)
		VALUES ($1::uuid, $2, NULLIF($3, ''), $4, $5, $6, $7, $8, NULLIF($9, ''))
	`, event.UserID, event.Feature, event.Model, event.InputTokens, event.CachedInputTokens,
		event.OutputTokens, event.EstimatedCostUSD, event.LatencyMS, event.TraceID)
	if err != nil {
		return fmt.Errorf("record usage event: %w", err)
	}
	return nil
}

func (s *Store) CostSince(ctx context.Context, userID string, since time.Time) (float64, error) {
	if s == nil || s.db == nil {
		return 0, errors.New("database is not configured")
	}
	var total float64
	err := s.db.QueryRow(ctx, `
		SELECT COALESCE(SUM(estimated_cost_usd), 0)::double precision
		FROM usage_events
		WHERE user_id = $1::uuid AND created_at >= $2
	`, userID, since).Scan(&total)
	if err != nil {
		return 0, fmt.Errorf("calculate usage cost: %w", err)
	}
	return total, nil
}

func stringValue(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}
