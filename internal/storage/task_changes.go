package storage

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"nova.local/core/internal/core"
)

func (s *Store) GetTask(ctx context.Context, userID, taskID string) (Task, error) {
	if s == nil || s.db == nil {
		return Task{}, errors.New("database is not configured")
	}
	var task Task
	err := s.db.QueryRow(ctx, `SELECT id::text,user_id::text,title,COALESCE(details,''),status,due_at,completed_at,created_at,updated_at
		FROM tasks WHERE user_id=$1::uuid AND id=$2::uuid`, userID, taskID).Scan(
		&task.ID, &task.UserID, &task.Title, &task.Details, &task.Status, &task.DueAt, &task.CompletedAt, &task.CreatedAt, &task.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Task{}, ErrNotFound
	}
	return task, err
}

func (s *Store) TaskChangeResult(ctx context.Context, userID, key, hash string) (*Task, error) {
	if s == nil || s.db == nil {
		return nil, errors.New("database is not configured")
	}
	return taskChangeResult(ctx, s.db, userID, key, hash)
}

type taskChangeReader interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}

func taskChangeResult(ctx context.Context, db taskChangeReader, userID, key, hash string) (*Task, error) {
	var payload []byte
	var previousHash string
	err := db.QueryRow(ctx, `SELECT reason,arguments_hash FROM agent_actions
		WHERE user_id=$1::uuid AND idempotency_key=$2 AND status='EXECUTED'
		AND action_name IN ('task.complete','task.cancel','task.reschedule')
		ORDER BY created_at DESC LIMIT 1`, userID, key).Scan(&payload, &previousHash)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if previousHash != hash {
		return nil, errors.New("task request key reused with different arguments")
	}
	var result Task
	if err = json.Unmarshal(payload, &result); err != nil {
		return nil, fmt.Errorf("decode task change: %w", err)
	}
	return &result, nil
}

// Serialize retries by request key and reject stale selections. Task state and
// its successful audit receipt either both commit or neither does.
func (s *Store) ChangeOpenTask(ctx context.Context, expected Task, dueAt *time.Time, action AgentAction) (Task, error) {
	if s == nil || s.db == nil {
		return Task{}, errors.New("database is not configured")
	}
	if expected.UserID != action.UserID || action.IdempotencyKey == "" || action.ArgumentsHash == "" {
		return Task{}, errors.New("invalid task change identity")
	}
	status := "open"
	switch action.ActionName {
	case core.ActionTaskComplete:
		status = "done"
	case core.ActionTaskCancel:
		status = "cancelled"
	case core.ActionTaskReschedule:
		if dueAt == nil {
			return Task{}, errors.New("task deadline is required")
		}
	default:
		return Task{}, errors.New("unsupported task change")
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return Task{}, err
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, action.UserID+":"+action.IdempotencyKey); err != nil {
		return Task{}, err
	}
	prior, err := taskChangeResult(ctx, tx, action.UserID, action.IdempotencyKey, action.ArgumentsHash)
	if err != nil {
		return Task{}, err
	}
	if prior != nil {
		return *prior, tx.Commit(ctx)
	}
	if status == "open" && !dueAt.After(time.Now()) {
		return Task{}, errors.New("task deadline must be in the future")
	}
	var result Task
	err = tx.QueryRow(ctx, `UPDATE tasks SET status=$4,
		due_at=CASE WHEN $4='open' THEN $5 ELSE due_at END,
		completed_at=CASE WHEN $4='done' THEN now() ELSE NULL END,updated_at=now()
		WHERE user_id=$1::uuid AND id=$2::uuid AND updated_at=$3 AND status='open'
		RETURNING id::text,user_id::text,title,COALESCE(details,''),status,due_at,completed_at,created_at,updated_at`,
		expected.UserID, expected.ID, expected.UpdatedAt, status, dueAt).Scan(
		&result.ID, &result.UserID, &result.Title, &result.Details, &result.Status, &result.DueAt, &result.CompletedAt, &result.CreatedAt, &result.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Task{}, ErrNotFound
	}
	if err != nil {
		return Task{}, err
	}
	payload, err := json.Marshal(result)
	if err != nil {
		return Task{}, err
	}
	_, err = tx.Exec(ctx, `INSERT INTO agent_actions(user_id,trace_id,action_name,status,arguments_hash,idempotency_key,reason)
		VALUES($1::uuid,$2,$3,'EXECUTED',$4,$5,$6)`, action.UserID, action.TraceID, action.ActionName, action.ArgumentsHash, action.IdempotencyKey, string(payload))
	if err != nil {
		return Task{}, fmt.Errorf("audit task change: %w", err)
	}
	return result, tx.Commit(ctx)
}
