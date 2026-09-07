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
