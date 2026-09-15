package storage

import (
	"context"
	"errors"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"nova.local/core/internal/core"
)

func TestReminderQuickActionsIntegration(t *testing.T) {
	url := os.Getenv("NOVA_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("disposable database is not configured")
	}
	ctx := context.Background()
	db, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	for _, path := range []string{"../../infrastructure/postgres/init.sql",
		"../../infrastructure/postgres/migrations/202609150001_reminder_dispatches.sql",
		"../../infrastructure/postgres/migrations/202609150002_reminder_action_grants.sql",
		"../../infrastructure/postgres/migrations/202609150002_reminder_action_grants.sql"} {
		sql, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = db.Exec(ctx, string(sql)); err != nil {
			t.Fatal(err)
		}
	}
	store := New(db)
	newUser := func() string {
		t.Helper()
		var id string
		if err := db.QueryRow(ctx, "INSERT INTO users DEFAULT VALUES RETURNING id::text").Scan(&id); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _, _ = db.Exec(ctx, "DELETE FROM users WHERE id=$1::uuid", id) })
		return id
	}
	// Clean up before the connection pool closes.
	userID, otherID := newUser(), newUser()
	conversation, err := store.CreateConversation(ctx, userID, "Quick action test")
	if err != nil {
		t.Fatal(err)
	}
	otherConversation, err := store.CreateConversation(ctx, otherID, "Other")
	if err != nil {
		t.Fatal(err)
	}
	newGrant := func() (Reminder, string) {
		t.Helper()
		r, err := store.CreateReminder(ctx, Reminder{UserID: userID, Title: t.Name(), TriggerAt: time.Now().UTC().Add(-time.Minute).Truncate(time.Second), Timezone: "Europe/Kyiv", Status: "scheduled", Priority: "normal", DeliveryMethod: "telegram"}, "")
		if err != nil {
			t.Fatal(err)
		}
		id, err := store.CreateReminderActionGrant(ctx, r, "telegram", "42")
		if err != nil {
			t.Fatal(err)
		}
		return r, id
	}
	apply := func(grant string, action core.ReminderQuickAction) (ReminderQuickActionResult, error) {
		return store.ApplyReminderQuickAction(ctx, userID, grant, "telegram", "42", conversation.ID, "quick-test", action)
	}
	check := func(r Reminder, grant, status, dispatch string, wantMessages int) {
		t.Helper()
		current, err := store.GetReminder(ctx, userID, r.ID)
		if err != nil || current.Status != status {
			t.Fatalf("reminder=%+v err=%v", current, err)
		}
		var gotDispatch string
		if err = db.QueryRow(ctx, "SELECT status FROM reminder_dispatches WHERE reminder_id=$1::uuid", r.ID).Scan(&gotDispatch); err != nil || gotDispatch != dispatch {
			t.Fatalf("dispatch=%s err=%v", gotDispatch, err)
		}
		var audit int
		if err = db.QueryRow(ctx, "SELECT count(*) FROM agent_actions WHERE user_id=$1::uuid AND idempotency_key=$2", userID, "reminder-button:"+grant).Scan(&audit); err != nil || audit != wantMessages/2 {
			t.Fatalf("audit=%d err=%v", audit, err)
		}
	}
	t.Run("concurrent snooze is one mutation", func(t *testing.T) {
		r, grant := newGrant()
		if again, err := store.CreateReminderActionGrant(ctx, r, "telegram", "42"); err != nil || again != grant {
			t.Fatalf("grant retry=%q err=%v", again, err)
		}
		if _, _, err = store.DeliverReminderForSchedule(ctx, userID, r.ID, r.TriggerAt); err != nil {
			t.Fatal(err)
		}
		start := time.Now().UTC()
		var applied atomic.Int32
		var wg sync.WaitGroup
		for i := 0; i < 4; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				result, err := apply(grant, core.ReminderSnooze10)
				if err != nil {
					t.Error(err)
					return
				}
				if !result.Replayed {
					applied.Add(1)
				}
				if result.Reminder.TriggerAt.Before(start.Add(10*time.Minute-time.Second)) || result.Reminder.TriggerAt.After(time.Now().Add(10*time.Minute)) {
					t.Errorf("snooze time=%v", result.Reminder.TriggerAt)
				}
			}()
		}
		wg.Wait()
		if applied.Load() != 1 {
			t.Fatalf("mutations=%d", applied.Load())
		}
		check(r, grant, "scheduled", "pending", 2)
		result, err := apply(grant, core.ReminderComplete)
		if err != nil || !result.Replayed || result.Reminder.Status != "scheduled" {
			t.Fatalf("different button replay=%+v err=%v", result, err)
		}
		messages, err := store.ListMessages(ctx, userID, conversation.ID, 100)
		if err != nil || len(messages) != 2 || messages[0].Role != "user" || messages[1].Role != "assistant" || messages[1].Channel != "telegram" {
			t.Fatalf("shared messages=%+v err=%v", messages, err)
		}
		if _, sent, err := store.DeliverReminderForSchedule(ctx, userID, r.ID, r.TriggerAt); err != nil || sent {
			t.Fatalf("stale worker overwrote snooze: %v %v", sent, err)
		}
	})
	t.Run("complete before worker finalization", func(t *testing.T) {
		r, grant := newGrant()
		result, err := apply(grant, core.ReminderComplete)
		if err != nil || result.Reminder.Status != "completed" {
			t.Fatalf("completion=%+v err=%v", result, err)
		}
		check(r, grant, "completed", "inactive", 2)
		if _, sent, err := store.DeliverReminderForSchedule(ctx, userID, r.ID, r.TriggerAt); err != nil || sent {
			t.Fatalf("worker overwrote completion: %v %v", sent, err)
		}
	})
	t.Run("hour snooze", func(t *testing.T) {
		_, grant := newGrant()
		result, err := apply(grant, core.ReminderSnooze60)
		if err != nil || time.Until(result.Reminder.TriggerAt) < 59*time.Minute || time.Until(result.Reminder.TriggerAt) > time.Hour {
			t.Fatalf("hour snooze=%+v err=%v", result, err)
		}
	})
	t.Run("ownership and stale grants", func(t *testing.T) {
		for _, kind := range []string{"user", "recipient", "channel", "conversation", "cancelled", "rescheduled", "expired", "unknown", "action"} {
			t.Run(kind, func(t *testing.T) {
				r, grant := newGrant()
				uid, recipient, channel, cid, action := userID, "42", "telegram", conversation.ID, core.ReminderComplete
				wantErr := ErrNotFound
				switch kind {
				case "user":
					uid = otherID
				case "recipient":
					recipient = "43"
				case "channel":
					channel = "web"
				case "conversation":
					cid = otherConversation.ID
				case "cancelled":
					err = store.CancelReminder(ctx, userID, r.ID)
					wantErr = ErrReminderActionExpired
				case "rescheduled":
					at := time.Now().Add(time.Hour)
					_, err = store.UpdateReminder(ctx, userID, r.ID, ReminderUpdate{TriggerAt: &at, TriggerAtSet: true})
					wantErr = ErrReminderActionExpired
				case "expired":
					_, err = db.Exec(ctx, "UPDATE reminder_action_grants SET expires_at=now()-interval '1 second' WHERE id=$1::uuid", grant)
					wantErr = ErrReminderActionExpired
				case "unknown":
					grant = "00000000-0000-0000-0000-000000000000"
				case "action":
					action = "delete"
				}
				if err != nil {
					t.Fatal(err)
				}
				_, err = store.ApplyReminderQuickAction(ctx, uid, grant, channel, recipient, cid, "denied", action)
				if err == nil || (kind != "action" && !errors.Is(err, wantErr)) {
					t.Fatalf("accepted invalid action or wrong error: %v", err)
				}
				var consumed int
				if err = db.QueryRow(ctx, "SELECT count(*) FROM reminder_action_grants WHERE reminder_id=$1::uuid AND consumed_at IS NOT NULL", r.ID).Scan(&consumed); err != nil || consumed != 0 {
					t.Fatalf("grant consumed on rejection: %d %v", consumed, err)
				}
			})
		}
	})
	t.Run("audit failure rolls back everything", func(t *testing.T) {
		r, grant := newGrant()
		messagesBefore, err := store.ListMessages(ctx, userID, conversation.ID, 100)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = db.Exec(ctx, "ALTER TABLE agent_actions ADD CONSTRAINT nova_test_quick_audit CHECK(trace_id <> 'reject-quick-audit')"); err != nil {
			t.Fatal(err)
		}
		defer db.Exec(ctx, "ALTER TABLE agent_actions DROP CONSTRAINT nova_test_quick_audit")
		if _, err = store.ApplyReminderQuickAction(ctx, userID, grant, "telegram", "42", conversation.ID, "reject-quick-audit", core.ReminderSnooze10); err == nil {
			t.Fatal("audit failure ignored")
		}
		check(r, grant, "scheduled", "pending", 0)
		current, err := store.GetReminder(ctx, userID, r.ID)
		if err != nil || !current.TriggerAt.Equal(r.TriggerAt) {
			t.Fatalf("time changed on failure: %+v %v", current, err)
		}
		messagesAfter, err := store.ListMessages(ctx, userID, conversation.ID, 100)
		if err != nil || len(messagesAfter) != len(messagesBefore) {
			t.Fatal("messages persisted on rollback")
		}
		result, err := apply(grant, core.ReminderComplete)
		if err != nil || result.Replayed {
			t.Fatalf("grant not reusable after rollback: %+v %v", result, err)
		}
	})
}
