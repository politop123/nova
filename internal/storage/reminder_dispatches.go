package storage

import (
	"context"
	"errors"
	"fmt"
	"time"
)

type ReminderDispatch struct {
	Reminder Reminder
	Token    string
	Failures int
}

type ReminderDispatchStats struct {
	Scheduled int
	Pending   int
	Failed    int
	Overdue   int
}

// Reserve the attempt before contacting a provider. Duplicate consumers of the
// same queue attempt must not both send (including during Redis recovery).
func (s *Store) ClaimReminderDeliveryAttempt(ctx context.Context, reminder Reminder, event ReminderDeliveryEvent) (bool, error) {
	if s == nil || s.db == nil {
		return false, errors.New("database is not configured")
	}
	if event.UserID != reminder.UserID || event.ReminderID != reminder.ID || event.Status != "attempted" || event.IdempotencyKey == "" || event.Attempt < 1 {
		return false, errors.New("invalid reminder delivery attempt")
	}
	result, err := s.db.Exec(ctx, `INSERT INTO reminder_delivery_events
		(reminder_id,user_id,channel,provider,status,attempt,detail,idempotency_key)
		SELECT id,user_id,delivery_method,$4,'attempted',$5,$6,$7 FROM reminders
		WHERE id=$1::uuid AND user_id=$2::uuid AND status='scheduled' AND trigger_at=$3
		ON CONFLICT (user_id,idempotency_key) DO NOTHING`, reminder.ID, reminder.UserID, reminder.TriggerAt,
		event.Provider, event.Attempt, event.Detail, event.IdempotencyKey)
	if err != nil {
		return false, fmt.Errorf("claim reminder delivery attempt: %w", err)
	}
	return result.RowsAffected() == 1, nil
}

// Claims expire after a process crash. The token prevents a late worker from
// overwriting a newer claim or a user's cancellation/reschedule.
func (s *Store) ClaimReminderDispatches(ctx context.Context, limit int) ([]ReminderDispatch, error) {
	if s == nil || s.db == nil {
		return nil, errors.New("database is not configured")
	}
	if limit < 1 || limit > 100 {
		limit = 25
	}
	rows, err := s.db.Query(ctx, `WITH ready AS (
		SELECT d.reminder_id FROM reminder_dispatches d JOIN reminders r ON r.id=d.reminder_id
		WHERE d.status IN ('pending','queued') AND r.status='scheduled' AND r.trigger_at=d.trigger_at
		AND d.next_check_at<=now() AND (d.lease_until IS NULL OR d.lease_until<now())
		AND d.trigger_at<=now()+interval '24 hours'
		ORDER BY d.next_check_at, d.reminder_id LIMIT $1 FOR UPDATE OF d SKIP LOCKED
	), claimed AS (
		UPDATE reminder_dispatches d SET lease_token=gen_random_uuid(), lease_until=now()+interval '30 seconds'
		FROM ready WHERE d.reminder_id=ready.reminder_id RETURNING d.*
	) SELECT r.id::text, r.user_id::text, r.title, r.trigger_at, r.timezone, COALESCE(r.recurrence_rule,''),
		r.priority, r.delivery_method, r.status, r.created_at, r.updated_at, c.lease_token::text, c.failures
		FROM claimed c JOIN reminders r ON r.id=c.reminder_id`, limit)
	if err != nil {
		return nil, fmt.Errorf("claim reminder dispatches: %w", err)
	}
	defer rows.Close()
	var result []ReminderDispatch
	for rows.Next() {
		var item ReminderDispatch
		r := &item.Reminder
		if err := rows.Scan(&r.ID, &r.UserID, &r.Title, &r.TriggerAt, &r.Timezone, &r.RecurrenceRule, &r.Priority,
			&r.DeliveryMethod, &r.Status, &r.CreatedAt, &r.UpdatedAt, &item.Token, &item.Failures); err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

func (s *Store) FinishReminderDispatch(ctx context.Context, item ReminderDispatch, status, detail string, nextCheck time.Time) (bool, error) {
	if s == nil || s.db == nil {
		return false, errors.New("database is not configured")
	}
	if status != "pending" && status != "queued" && status != "failed" {
		return false, errors.New("invalid dispatch status")
	}
	var applied bool
	err := s.db.QueryRow(ctx, `WITH changed AS (UPDATE reminder_dispatches SET status=$4, last_error=$5,
		failures=CASE WHEN $4='queued' THEN 0 ELSE failures+1 END,
		next_check_at=$6, lease_token=NULL, lease_until=NULL, updated_at=now()
		WHERE reminder_id=$1::uuid AND trigger_at=$2 AND lease_token=$3::uuid RETURNING reminder_id,failures
	), logged AS (
		INSERT INTO reminder_delivery_events(reminder_id,user_id,channel,provider,status,attempt,detail,idempotency_key)
		SELECT r.id,r.user_id,r.delivery_method,'nova_scheduler','failed',GREATEST(c.failures,1),$5,$7
		FROM changed c JOIN reminders r ON r.id=c.reminder_id WHERE $4='failed'
		ON CONFLICT (user_id,idempotency_key) DO NOTHING
	) SELECT EXISTS(SELECT 1 FROM changed)`,
		item.Reminder.ID, item.Reminder.TriggerAt, item.Token, status, detail, nextCheck,
		fmt.Sprintf("dispatch:%s:%s:failed", item.Reminder.ID, item.Reminder.TriggerAt.UTC().Format(time.RFC3339))).Scan(&applied)
	if err != nil {
		return false, fmt.Errorf("finish reminder dispatch: %w", err)
	}
	return applied, nil
}

func (s *Store) ReminderDispatchDurable(ctx context.Context, reminder Reminder) (bool, error) {
	if s == nil || s.db == nil {
		return false, errors.New("database is not configured")
	}
	var durable bool
	err := s.db.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM reminder_dispatches d JOIN reminders r ON r.id=d.reminder_id
		WHERE d.reminder_id=$1::uuid AND r.user_id=$2::uuid AND r.status='scheduled'
		AND d.trigger_at=$3 AND r.trigger_at=d.trigger_at AND d.status IN ('pending','queued'))`,
		reminder.ID, reminder.UserID, reminder.TriggerAt).Scan(&durable)
	return durable, err
}

// After a queue loss an already attempted Telegram send may have succeeded.
// Do not automatically replay it merely because Redis no longer knows its task.
func (s *Store) ReminderDeliveryAttempted(ctx context.Context, reminder Reminder) (bool, error) {
	if s == nil || s.db == nil {
		return false, errors.New("database is not configured")
	}
	pattern := fmt.Sprintf("reminder:%s:%s:attempt:%%:attempted", reminder.ID, reminder.TriggerAt.UTC().Format(time.RFC3339))
	var attempted bool
	err := s.db.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM reminder_delivery_events
		WHERE user_id=$1::uuid AND reminder_id=$2::uuid AND status='attempted' AND idempotency_key LIKE $3)`,
		reminder.UserID, reminder.ID, pattern).Scan(&attempted)
	return attempted, err
}

func (s *Store) ReminderDispatchSummary(ctx context.Context, userID string) (ReminderDispatchStats, error) {
	var stats ReminderDispatchStats
	if s == nil || s.db == nil {
		return stats, errors.New("database is not configured")
	}
	err := s.db.QueryRow(ctx, `SELECT count(*),
		count(*) FILTER (WHERE (d.status='pending' AND r.trigger_at<=now()+interval '5 minutes') OR d.reminder_id IS NULL),
		count(*) FILTER (WHERE d.status='failed'),
		count(*) FILTER (WHERE r.trigger_at<now()-interval '2 minutes')
		FROM reminders r LEFT JOIN reminder_dispatches d ON d.reminder_id=r.id AND d.trigger_at=r.trigger_at
		WHERE r.user_id=$1::uuid AND r.status='scheduled'`, userID).Scan(&stats.Scheduled, &stats.Pending, &stats.Failed, &stats.Overdue)
	return stats, err
}
