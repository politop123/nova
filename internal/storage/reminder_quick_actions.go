package storage

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"nova.local/core/internal/core"
)

var ErrReminderActionExpired = errors.New("reminder action is expired or stale")

type ReminderQuickActionResult struct {
	Reminder Reminder `json:"reminder"`
	Reply    string   `json:"reply"`
	Replayed bool     `json:"-"`
}

func (s *Store) CreateReminderActionGrant(ctx context.Context, r Reminder, channel, recipient string) (string, error) {
	if s == nil || s.db == nil {
		return "", errors.New("database is not configured")
	}
	if channel != "telegram" || recipient == "" {
		return "", errors.New("unsupported reminder recipient")
	}
	var id string
	err := s.db.QueryRow(ctx, `INSERT INTO reminder_action_grants(reminder_id,user_id,trigger_at,channel,recipient)
		SELECT id,user_id,trigger_at,$4,$5 FROM reminders WHERE id=$1::uuid AND user_id=$2::uuid
		AND trigger_at=$3 AND status='scheduled' AND delivery_method=$4
		ON CONFLICT (reminder_id,trigger_at,channel,recipient) DO UPDATE SET id=reminder_action_grants.id
		RETURNING id::text`, r.ID, r.UserID, r.TriggerAt, channel, recipient).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrReminderActionExpired
	}
	return id, err
}

// The grant, reminder mutation, audit and shared conversation commit together.
// A grant can select one action once, even across repeated callbacks/devices.
func (s *Store) ApplyReminderQuickAction(ctx context.Context, userID, grantID, channel, recipient, conversationID, traceID string, action core.ReminderQuickAction) (ReminderQuickActionResult, error) {
	var result ReminderQuickActionResult
	if s == nil || s.db == nil {
		return result, errors.New("database is not configured")
	}
	if !action.Valid() {
		return result, errors.New("unsupported reminder quick action")
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return result, err
	}
	defer tx.Rollback(ctx)
	var originalTime, expiresAt time.Time
	var storedResult []byte
	r := &result.Reminder
	err = tx.QueryRow(ctx, `SELECT r.id::text,r.user_id::text,r.title,r.trigger_at,r.timezone,COALESCE(r.recurrence_rule,''),
		r.priority,r.delivery_method,r.status,r.created_at,r.updated_at,g.trigger_at,g.expires_at,g.result_json
		FROM reminder_action_grants g JOIN reminders r ON r.id=g.reminder_id AND r.user_id=g.user_id
		WHERE g.id=$1::uuid AND g.user_id=$2::uuid AND g.channel=$3 AND g.recipient=$4 FOR UPDATE OF g,r`,
		grantID, userID, channel, recipient).Scan(&r.ID, &r.UserID, &r.Title, &r.TriggerAt, &r.Timezone, &r.RecurrenceRule,
		&r.Priority, &r.DeliveryMethod, &r.Status, &r.CreatedAt, &r.UpdatedAt, &originalTime, &expiresAt, &storedResult)
	if errors.Is(err, pgx.ErrNoRows) {
		return result, ErrNotFound
	}
	if err != nil {
		return result, err
	}
	now := time.Now().UTC()
	if !expiresAt.After(now) {
		return result, ErrReminderActionExpired
	}
	if len(storedResult) > 0 {
		if err = json.Unmarshal(storedResult, &result); err != nil {
			return result, err
		}
		result.Replayed = true
		return result, tx.Commit(ctx)
	}
	if !r.TriggerAt.Equal(originalTime) || (r.Status != "scheduled" && r.Status != "delivered") {
		return result, ErrReminderActionExpired
	}
	var ownsConversation bool
	if err = tx.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM conversations WHERE id=$1::uuid AND user_id=$2::uuid)", conversationID, userID).Scan(&ownsConversation); err != nil {
		return result, err
	}
	if !ownsConversation {
		return result, ErrNotFound
	}
	userText := "✅ Виконано: " + r.Title
	r.Status = "completed"
	result.Reply = "✅ Позначила виконаним: " + r.Title + "."
	if action != core.ReminderComplete {
		r.Status = "scheduled"
		r.TriggerAt = now.Add(action.Delay()).Truncate(time.Second)
		location, err := time.LoadLocation(r.Timezone)
		if err != nil {
			location = time.UTC
		}
		userText = fmt.Sprintf("Відкласти нагадування «%s» на %d хв.", r.Title, int(action.Delay()/time.Minute))
		result.Reply = fmt.Sprintf("⏰ Відклала нагадування «%s». Нагадаю %s.", r.Title, r.TriggerAt.In(location).Format("02.01 о 15:04"))
	}
	err = tx.QueryRow(ctx, `UPDATE reminders SET status=$3,trigger_at=$4,updated_at=now()
		WHERE id=$1::uuid AND user_id=$2::uuid RETURNING updated_at`, r.ID, userID, r.Status, r.TriggerAt).Scan(&r.UpdatedAt)
	if err != nil {
		return result, err
	}
	hash := sha256.Sum256([]byte(grantID + ":" + string(action)))
	_, err = tx.Exec(ctx, `INSERT INTO agent_actions(user_id,trace_id,action_name,status,arguments_hash,idempotency_key,reason)
		VALUES($1::uuid,$2,$3,'EXECUTED',$4,$5,$6)`, userID, traceID, action.ActionName(), hex.EncodeToString(hash[:]), "reminder-button:"+grantID, result.Reply)
	if err != nil {
		return result, err
	}
	_, err = tx.Exec(ctx, `INSERT INTO messages(conversation_id,user_id,role,channel,modality,content,trace_id,created_at)
		VALUES($1::uuid,$2::uuid,'user',$3,'text',$4,$6,now()),
		($1::uuid,$2::uuid,'assistant',$3,'text',$5,$6,now()+interval '1 microsecond')`,
		conversationID, userID, channel, userText, result.Reply, traceID)
	if err != nil {
		return result, err
	}
	payload, err := json.Marshal(result)
	if err != nil {
		return result, err
	}
	_, err = tx.Exec(ctx, "UPDATE reminder_action_grants SET consumed_at=now(),result_json=$2::jsonb WHERE id=$1::uuid", grantID, string(payload))
	if err != nil {
		return result, err
	}
	return result, tx.Commit(ctx)
}
