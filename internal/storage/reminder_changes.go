package storage

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// ReminderChangeResult returns the persisted result of a previously executed request.
func (s *Store) ReminderChangeResult(ctx context.Context, userID, key string) (*Reminder, error) {
	if s == nil || s.db == nil {
		return nil, errors.New("database is not configured")
	}
	return reminderChangeResult(ctx, s.db, userID, key)
}

type reminderChangeReader interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}

func reminderChangeResult(ctx context.Context, db reminderChangeReader, userID, key string) (*Reminder, error) {
	var payload []byte
	err := db.QueryRow(ctx, `SELECT reason FROM agent_actions
		WHERE user_id = $1::uuid AND idempotency_key = $2 AND status = 'EXECUTED'
		AND action_name IN ('reminder.cancel', 'reminder.reschedule')
		ORDER BY created_at DESC LIMIT 1`, userID, key).Scan(&payload)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read reminder change: %w", err)
	}
	var result Reminder
	if err := json.Unmarshal(payload, &result); err != nil {
		return nil, fmt.Errorf("decode reminder change: %w", err)
	}
	return &result, nil
}

// ChangeScheduledReminder commits the change and its audit result together. The
// request lock deduplicates retries; the version check rejects stale selections.
func (s *Store) ChangeScheduledReminder(ctx context.Context, expected Reminder, triggerAt *time.Time, timezone string, action AgentAction) (Reminder, error) {
	if s == nil || s.db == nil {
		return Reminder{}, errors.New("database is not configured")
	}
	if action.UserID != expected.UserID || action.IdempotencyKey == "" ||
		(action.ActionName != "reminder.cancel" && action.ActionName != "reminder.reschedule") ||
		(action.ActionName == "reminder.reschedule" && (triggerAt == nil || !triggerAt.After(time.Now()))) {
		return Reminder{}, errors.New("invalid reminder change")
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return Reminder{}, err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, action.UserID+":"+action.IdempotencyKey); err != nil {
		return Reminder{}, err
	}
	previous, err := reminderChangeResult(ctx, tx, action.UserID, action.IdempotencyKey)
	if err != nil {
		return Reminder{}, err
	}
	if previous != nil {
		return *previous, tx.Commit(ctx)
	}
	status := "scheduled"
	if action.ActionName == "reminder.cancel" {
		status, triggerAt, timezone = "cancelled", nil, expected.Timezone
	}
	var result Reminder
	err = tx.QueryRow(ctx, `UPDATE reminders
		SET status = $4, trigger_at = COALESCE($5, trigger_at), timezone = $6, updated_at = now()
		WHERE user_id = $1::uuid AND id = $2::uuid AND status = 'scheduled' AND updated_at = $3
		RETURNING id::text, user_id::text, title, trigger_at, timezone, COALESCE(recurrence_rule, ''),
		priority, delivery_method, status, created_at, updated_at`, expected.UserID, expected.ID, expected.UpdatedAt,
		status, triggerAt, timezone).Scan(&result.ID, &result.UserID, &result.Title, &result.TriggerAt,
		&result.Timezone, &result.RecurrenceRule, &result.Priority, &result.DeliveryMethod, &result.Status,
		&result.CreatedAt, &result.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Reminder{}, ErrNotFound
	}
	if err != nil {
		return Reminder{}, fmt.Errorf("change scheduled reminder: %w", err)
	}
	payload, err := json.Marshal(result)
	if err != nil {
		return Reminder{}, err
	}
	_, err = tx.Exec(ctx, `INSERT INTO agent_actions
		(user_id, trace_id, action_name, status, arguments_hash, idempotency_key, reason)
		VALUES ($1::uuid, $2, $3, 'EXECUTED', $4, $5, $6)`, action.UserID, action.TraceID,
		action.ActionName, action.ArgumentsHash, action.IdempotencyKey, string(payload))
	if err != nil {
		return Reminder{}, fmt.Errorf("audit reminder change: %w", err)
	}
	return result, tx.Commit(ctx)
}
