package jobs

import (
	"context"
	"log/slog"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hibiken/asynq"
	"github.com/jackc/pgx/v5/pgxpool"
	"nova.local/core/internal/storage"
)

// Uses disposable PostgreSQL and Redis only; never contacts Telegram or a model.
func TestReminderDispatchIntegration(t *testing.T) {
	databaseURL, redisURL := os.Getenv("NOVA_TEST_DATABASE_URL"), os.Getenv("NOVA_TEST_REDIS_URL")
	if databaseURL == "" || redisURL == "" {
		t.Skip("disposable database/Redis URLs are not configured")
	}
	ctx := context.Background()
	db, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	apply := func(path string) {
		t.Helper()
		sql, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = db.Exec(ctx, string(sql)); err != nil {
			t.Fatal(err)
		}
	}
	apply("../../infrastructure/postgres/init.sql")
	var userID string
	if err = db.QueryRow(ctx, "INSERT INTO users DEFAULT VALUES RETURNING id::text").Scan(&userID); err != nil {
		t.Fatal(err)
	}
	defer db.Exec(ctx, "DELETE FROM users WHERE id=$1::uuid", userID)
	store := storage.New(db)
	now := time.Now().UTC().Truncate(time.Second)
	newReminder := func(key string, at time.Time) storage.Reminder {
		t.Helper()
		r, err := store.CreateReminder(ctx, storage.Reminder{UserID: userID, Title: key, TriggerAt: at, Timezone: "Europe/Kyiv", Status: "scheduled", Priority: "normal", DeliveryMethod: "telegram"}, key)
		if err != nil {
			t.Fatal(err)
		}
		return r
	}
	r := newReminder("created before migration", now.Add(time.Hour))
	apply("../../infrastructure/postgres/migrations/202609150001_reminder_dispatches.sql")
	apply("../../infrastructure/postgres/migrations/202609150001_reminder_dispatches.sql")
	if durable, err := store.ReminderDispatchDurable(ctx, r); err != nil || !durable {
		t.Fatalf("backfill failed: %v %v", durable, err)
	}
	opt, err := asynq.ParseRedisURI(redisURL)
	if err != nil {
		t.Fatal(err)
	}
	client := asynq.NewClient(opt)
	defer client.Close()
	inspector := asynq.NewInspector(opt)
	defer inspector.Close()
	queue := AsynqReminderQueue{Client: client, Inspector: inspector}
	due := func() {
		t.Helper()
		if _, err := db.Exec(ctx, `UPDATE reminder_dispatches SET next_check_at=now() WHERE reminder_id IN (SELECT id FROM reminders WHERE user_id=$1::uuid)`, userID); err != nil {
			t.Fatal(err)
		}
	}
	checkStatus := func(r storage.Reminder, want string) {
		t.Helper()
		var got string
		if err := db.QueryRow(ctx, "SELECT status FROM reminder_dispatches WHERE reminder_id=$1::uuid", r.ID).Scan(&got); err != nil || got != want {
			t.Fatalf("dispatch %s: %s want %s err=%v", r.Title, got, want, err)
		}
	}
	run := func(q ReminderDispatchQueue) DispatchResult {
		t.Helper()
		result, err := DispatchReminders(ctx, store, q, time.Now().UTC())
		if err != nil {
			t.Fatal(err)
		}
		return result
	}

	// A real closed Redis connection fails, yet the durable request remains pending.
	offline := asynq.NewInspector(opt)
	offline.Close()
	run(AsynqReminderQueue{Client: client, Inspector: offline})
	checkStatus(r, "pending")
	due()
	if result := run(queue); result.Enqueued != 1 {
		t.Fatalf("recovery=%+v", result)
	}
	checkStatus(r, "queued")
	due()
	if result := run(queue); result.Enqueued != 0 {
		t.Fatalf("duplicate enqueue=%+v", result)
	}
	// Simulate losing exactly this test task from Redis, not flushing the database.
	if err := inspector.DeleteTask("default", ReminderTaskID(r)); err != nil {
		t.Fatal(err)
	}
	due()
	if result := run(queue); result.Enqueued != 1 {
		t.Fatalf("lost task recovery=%+v", result)
	}

	// Only one worker claims a row, and a reschedule invalidates that claim.
	due()
	claimed, err := store.ClaimReminderDispatches(ctx, 25)
	if err != nil || len(claimed) != 1 {
		t.Fatalf("claims=%v err=%v", claimed, err)
	}
	if other, err := store.ClaimReminderDispatches(ctx, 25); err != nil || len(other) != 0 {
		t.Fatalf("double claim=%v err=%v", other, err)
	}
	at := now.Add(2 * time.Hour)
	r, err = store.UpdateReminder(ctx, userID, r.ID, storage.ReminderUpdate{TriggerAt: &at, TriggerAtSet: true})
	if err != nil {
		t.Fatal(err)
	}
	if applied, err := store.FinishReminderDispatch(ctx, claimed[0], "failed", "stale", now); err != nil || applied {
		t.Fatalf("stale completion=%v err=%v", applied, err)
	}
	checkStatus(r, "pending")
	run(queue)
	// A crash after claiming releases work via lease expiry.
	due()
	claimed, err = store.ClaimReminderDispatches(ctx, 25)
	if err != nil || len(claimed) != 1 {
		t.Fatal("expected lease")
	}
	if _, err = db.Exec(ctx, "UPDATE reminder_dispatches SET lease_until=now()-interval '1 second' WHERE reminder_id=$1::uuid", r.ID); err != nil {
		t.Fatal(err)
	}
	if result := run(queue); result.Checked != 1 {
		t.Fatalf("lease recovery=%+v", result)
	}
	if err = store.CancelReminder(ctx, userID, r.ID); err != nil {
		t.Fatal(err)
	}
	checkStatus(r, "inactive")
	due()
	if result := run(queue); result.Checked != 0 {
		t.Fatalf("cancelled reminder reclaimed=%+v", result)
	}

	archived := newReminder("archived", now.Add(time.Hour))
	run(queue)
	if err = inspector.ArchiveTask("default", ReminderTaskID(archived)); err != nil {
		t.Fatal(err)
	}
	due()
	run(queue)
	checkStatus(archived, "failed")
	raceReminder := newReminder("rescheduled during send", now.Add(-time.Minute))
	movedAt := time.Now().UTC().Add(20 * time.Second).Truncate(time.Second)
	raceSender := &fakeTelegramSender{onSend: func() {
		_, err := store.UpdateReminder(ctx, userID, raceReminder.ID, storage.ReminderUpdate{TriggerAt: &movedAt, TriggerAtSet: true})
		if err != nil {
			t.Error(err)
		}
	}}
	raceMux := Handler(slog.Default(), store, HandlerConfig{Telegram: raceSender, TelegramChatID: 42})
	raceTask, err := NewReminderDeliveryTask(raceReminder)
	if err != nil {
		t.Fatal(err)
	}
	if err = raceMux.ProcessTask(ctx, raceTask); err != nil {
		t.Fatal(err)
	}
	raceCurrent, err := store.GetReminder(ctx, userID, raceReminder.ID)
	if err != nil || raceCurrent.Status != "scheduled" || !raceCurrent.TriggerAt.Equal(movedAt) {
		t.Fatalf("old send consumed new schedule: %v %v", raceCurrent, err)
	}
	checkStatus(raceCurrent, "pending")
	var failureEvents int
	if err = db.QueryRow(ctx, "SELECT count(*) FROM reminder_delivery_events WHERE reminder_id=$1::uuid AND provider='nova_scheduler' AND status='failed'", archived.ID).Scan(&failureEvents); err != nil || failureEvents != 1 {
		t.Fatalf("terminal audit=%d err=%v", failureEvents, err)
	}
	due()
	run(queue)
	checkStatus(archived, "failed")

	uncertain := newReminder("attempt already started", now.Add(-time.Minute))
	_, err = store.RecordReminderDeliveryEvent(ctx, storage.ReminderDeliveryEvent{UserID: userID, ReminderID: uncertain.ID, Channel: "telegram", Status: "attempted", Attempt: 1, IdempotencyKey: reminderDeliveryEventID(ReminderDeliveryPayload{ReminderID: uncertain.ID, TriggerAt: uncertain.TriggerAt.UTC().Format(time.RFC3339)}, "attempted", 1)})
	if err != nil {
		t.Fatal(err)
	}
	run(queue)
	checkStatus(uncertain, "failed")
	if _, exists, err := queue.State(ctx, uncertain); err != nil || exists {
		t.Fatalf("uncertain send was replayed: exists=%v err=%v", exists, err)
	}
	expired := newReminder("expired", now.Add(-25*time.Hour))
	run(queue)
	checkStatus(expired, "failed")

	// Deliver using the real database and a fake Telegram transport, then verify no requeue.
	deliver := newReminder("deliver once", now.Add(-time.Minute))
	run(queue)
	telegram := &fakeTelegramSender{}
	mux := Handler(slog.Default(), store, HandlerConfig{Telegram: telegram, TelegramChatID: 42})
	task, err := NewReminderDeliveryTask(deliver)
	if err != nil {
		t.Fatal(err)
	}
	if err = mux.ProcessTask(ctx, task); err != nil {
		t.Fatal(err)
	}
	if err = mux.ProcessTask(ctx, task); err != nil {
		t.Fatal(err)
	}
	if len(telegram.messages) != 1 {
		t.Fatalf("messages=%v", telegram.messages)
	}
	checkStatus(deliver, "inactive")
	stats, err := store.ReminderDispatchSummary(ctx, userID)
	if err != nil || stats.Failed != 3 {
		t.Fatalf("summary=%+v err=%v", stats, err)
	}
	concurrent := newReminder("concurrent consumers", now.Add(-time.Minute))
	event := storage.ReminderDeliveryEvent{ReminderID: concurrent.ID, UserID: userID, Channel: "telegram", Status: "attempted", Attempt: 1,
		IdempotencyKey: reminderDeliveryEventID(ReminderDeliveryPayload{ReminderID: concurrent.ID, TriggerAt: concurrent.TriggerAt.UTC().Format(time.RFC3339)}, "attempted", 1)}
	var claims atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ok, err := store.ClaimReminderDeliveryAttempt(ctx, concurrent, event)
			if err != nil {
				t.Error(err)
			}
			if ok {
				claims.Add(1)
			}
		}()
	}
	wg.Wait()
	if claims.Load() != 1 {
		t.Fatalf("multiple consumers reserved the same send: %d", claims.Load())
	}
	apply("../../infrastructure/postgres/migrations/202609150001_reminder_dispatches.sql")
	checkStatus(archived, "failed")
}
