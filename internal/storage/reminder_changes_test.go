package storage

import (
	"context"
	"errors"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Run only against a disposable database: NOVA_TEST_DATABASE_URL=... go test ./internal/storage.
func TestReminderChangesIntegration(t *testing.T) {
	url := os.Getenv("NOVA_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("NOVA_TEST_DATABASE_URL is not configured")
	}
	ctx := context.Background()
	db, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	schema, err := os.ReadFile("../../infrastructure/postgres/init.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(ctx, string(schema)); err != nil {
		t.Fatal(err)
	}
	dispatchSchema, err := os.ReadFile("../../infrastructure/postgres/migrations/202609150001_reminder_dispatches.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(ctx, string(dispatchSchema)); err != nil {
		t.Fatal(err)
	}
	store := New(db)
	var userID, otherID string
	if err = db.QueryRow(ctx, "INSERT INTO users DEFAULT VALUES RETURNING id::text").Scan(&userID); err != nil {
		t.Fatal(err)
	}
	if err = db.QueryRow(ctx, "INSERT INTO users DEFAULT VALUES RETURNING id::text").Scan(&otherID); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	r, err := store.CreateReminder(ctx, Reminder{UserID: userID, Title: "Паспорт", TriggerAt: now.Add(time.Hour), Timezone: "Europe/Kyiv", Priority: "normal", DeliveryMethod: "web", Status: "scheduled"}, "seed")
	if err != nil {
		t.Fatal(err)
	}
	action := AgentAction{UserID: userID, TraceID: "test", ActionName: "reminder.reschedule", ArgumentsHash: "hash", IdempotencyKey: "move"}
	at := now.Add(2 * time.Hour)
	// Simultaneous retries must commit one mutation and one audit record.
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			got, err := store.ChangeScheduledReminder(ctx, r, &at, "Europe/Kyiv", action)
			if err != nil || !got.TriggerAt.Equal(at) {
				t.Errorf("concurrent move: %v %v", got, err)
			}
		}()
	}
	wg.Wait()
	var count int
	if err = db.QueryRow(ctx, "SELECT count(*) FROM agent_actions WHERE user_id=$1::uuid AND idempotency_key='move'", userID).Scan(&count); err != nil || count != 1 {
		t.Fatalf("audit count=%d err=%v", count, err)
	}
	prior, err := store.ReminderChangeResult(ctx, userID, "move")
	if err != nil || prior == nil || !prior.TriggerAt.Equal(at) {
		t.Fatalf("replay=%v err=%v", prior, err)
	}
	if foreign, err := store.ReminderChangeResult(ctx, otherID, "move"); err != nil || foreign != nil {
		t.Fatalf("foreign replay=%v err=%v", foreign, err)
	}
	stale := action
	stale.IdempotencyKey = "stale"
	if _, err = store.ChangeScheduledReminder(ctx, r, &at, "Europe/Kyiv", stale); !errors.Is(err, ErrNotFound) {
		t.Fatalf("stale update: %v", err)
	}
	wrongOwner := action
	wrongOwner.UserID = otherID
	wrongOwner.IdempotencyKey = "foreign"
	if _, err = store.ChangeScheduledReminder(ctx, *prior, &at, "Europe/Kyiv", wrongOwner); err == nil {
		t.Fatal("cross-user update accepted")
	}
	// An audit failure rolls the reminder change back.
	if _, err = db.Exec(ctx, "ALTER TABLE agent_actions ADD CONSTRAINT nova_test_reject_audit CHECK (trace_id <> 'reject-audit')"); err != nil {
		t.Fatal(err)
	}
	defer db.Exec(ctx, "ALTER TABLE agent_actions DROP CONSTRAINT nova_test_reject_audit")
	failed := action
	failed.TraceID = "reject-audit"
	failed.IdempotencyKey = "rollback"
	failed.ActionName = "reminder.cancel"
	if _, err = store.ChangeScheduledReminder(ctx, *prior, nil, "Europe/Kyiv", failed); err == nil {
		t.Fatal("audit failure was ignored")
	}
	current, err := store.GetReminder(ctx, userID, r.ID)
	if err != nil || current.Status != "scheduled" {
		t.Fatalf("rollback failed: %v %v", current, err)
	}
	if durable, err := store.ReminderDispatchDurable(ctx, current); err != nil || !durable {
		t.Fatalf("audit failure did not roll back dispatch: %v %v", durable, err)
	}
	cancel := action
	cancel.ActionName = "reminder.cancel"
	cancel.IdempotencyKey = "cancel"
	if _, err = store.ChangeScheduledReminder(ctx, current, nil, "Europe/Kyiv", cancel); err != nil {
		t.Fatal(err)
	}
	if durable, err := store.ReminderDispatchDurable(ctx, current); err != nil || durable {
		t.Fatalf("cancelled dispatch still active: %v %v", durable, err)
	}
	if _, delivered, err := store.DeliverReminder(ctx, userID, r.ID); err != nil || delivered {
		t.Fatalf("cancelled reminder delivered=%v err=%v", delivered, err)
	}
	// Filter before LIMIT: 105 older entries must not hide tomorrow's entries.
	start := now.AddDate(0, 0, 1)
	end := start.AddDate(0, 0, 1)
	if _, err = db.Exec(ctx, `INSERT INTO tasks(user_id,title,status,due_at)
		SELECT $1::uuid,'overdue','open',$2::timestamptz FROM generate_series(1,105)`, userID, now.Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err = store.CreateTask(ctx, Task{UserID: userID, Title: "Tomorrow", Status: "open", DueAt: &start}, "tomorrow-task"); err != nil {
		t.Fatal(err)
	}
	tasks, err := store.ListAgendaTasks(ctx, userID, &start, &end)
	if err != nil || len(tasks) != 1 || tasks[0].Title != "Tomorrow" {
		t.Fatalf("agenda tasks=%v err=%v", tasks, err)
	}
	if _, err = store.CreateReminder(ctx, Reminder{UserID: userID, Title: "Tomorrow", TriggerAt: start, Timezone: "Europe/Kyiv", Priority: "normal", DeliveryMethod: "web", Status: "scheduled"}, "tomorrow-reminder"); err != nil {
		t.Fatal(err)
	}
	reminders, err := store.ListAgendaReminders(ctx, userID, &start, &end)
	if err != nil || len(reminders) != 1 || reminders[0].Title != "Tomorrow" {
		t.Fatalf("agenda reminders=%v err=%v", reminders, err)
	}
	foreign, err := store.ListAgendaReminders(ctx, otherID, &start, &end)
	if err != nil || len(foreign) != 0 {
		t.Fatalf("foreign agenda=%v err=%v", foreign, err)
	}
}
