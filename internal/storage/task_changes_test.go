package storage

import (
	"context"
	"errors"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"nova.local/core/internal/core"
)

func TestTaskChangesIntegration(t *testing.T) {
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
	schema, err := os.ReadFile("../../infrastructure/postgres/init.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(ctx, string(schema)); err != nil {
		t.Fatal(err)
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
	userID, otherID := newUser(), newUser()
	newTask := func(dueAt *time.Time) Task {
		t.Helper()
		task, err := store.CreateTask(ctx, Task{UserID: userID, Title: "Оплатити рахунок", Details: "Task change test", Status: "open", DueAt: dueAt}, "")
		if err != nil {
			t.Fatal(err)
		}
		return task
	}
	action := func(name, key string) AgentAction {
		return AgentAction{UserID: userID, TraceID: "task-test", ActionName: name, ArgumentsHash: name + "-hash", IdempotencyKey: key}
	}
	deadline := time.Now().UTC().Add(time.Hour).Truncate(time.Second)
	for _, name := range []string{core.ActionTaskComplete, core.ActionTaskCancel, core.ActionTaskReschedule} {
		t.Run(name, func(t *testing.T) {
			original := newTask(nil)
			a := action(name, name)
			var wg sync.WaitGroup
			for i := 0; i < 4; i++ {
				wg.Add(1)
				go func() {
					defer wg.Done()
					_, err := store.ChangeOpenTask(ctx, original, &deadline, a)
					if err != nil {
						t.Error(err)
					}
				}()
			}
			wg.Wait()
			var count int
			if err := db.QueryRow(ctx, "SELECT count(*) FROM agent_actions WHERE user_id=$1::uuid AND idempotency_key=$2", userID, a.IdempotencyKey).Scan(&count); err != nil || count != 1 {
				t.Fatalf("audit=%d err=%v", count, err)
			}
			current, err := store.GetTask(ctx, userID, original.ID)
			if err != nil {
				t.Fatal(err)
			}
			switch name {
			case core.ActionTaskComplete:
				if current.Status != "done" || current.CompletedAt == nil || current.DueAt != nil {
					t.Fatalf("completed=%+v", current)
				}
			case core.ActionTaskCancel:
				if current.Status != "cancelled" || current.CompletedAt != nil || current.DueAt != nil {
					t.Fatalf("cancelled=%+v", current)
				}
			case core.ActionTaskReschedule:
				if current.Status != "open" || current.CompletedAt != nil || current.DueAt == nil || !current.DueAt.Equal(deadline) {
					t.Fatalf("rescheduled=%+v", current)
				}
			}
			prior, err := store.TaskChangeResult(ctx, userID, a.IdempotencyKey, a.ArgumentsHash)
			if err != nil || prior == nil || prior.ID != original.ID {
				t.Fatalf("receipt=%v %v", prior, err)
			}
			if _, err = store.TaskChangeResult(ctx, userID, a.IdempotencyKey, "different-hash"); err == nil {
				t.Fatal("receipt accepted mismatched arguments")
			}
			if foreign, err := store.TaskChangeResult(ctx, otherID, a.IdempotencyKey, a.ArgumentsHash); err != nil || foreign != nil {
				t.Fatal("cross-user receipt exposed")
			}
			if _, err = store.ChangeOpenTask(ctx, original, &deadline, action(name, name+"-stale")); !errors.Is(err, ErrNotFound) {
				t.Fatalf("stale selection accepted: %v", err)
			}
			if _, err = store.GetTask(ctx, otherID, original.ID); !errors.Is(err, ErrNotFound) {
				t.Fatal("cross-user task exposed")
			}
		})
	}
	t.Run("preserve existing deadline on completion", func(t *testing.T) {
		original := newTask(&deadline)
		updated, err := store.ChangeOpenTask(ctx, original, nil, action(core.ActionTaskComplete, "keep-deadline"))
		if err != nil || updated.DueAt == nil || !updated.DueAt.Equal(deadline) {
			t.Fatalf("deadline lost: %+v %v", updated, err)
		}
	})
	t.Run("foreign and invalid mutations", func(t *testing.T) {
		original := newTask(nil)
		a := action(core.ActionTaskCancel, "foreign")
		a.UserID = otherID
		if _, err := store.ChangeOpenTask(ctx, original, nil, a); err == nil {
			t.Fatal("wrong user accepted")
		}
		past := time.Now().Add(-time.Hour)
		for _, at := range []*time.Time{nil, &past} {
			if _, err := store.ChangeOpenTask(ctx, original, at, action(core.ActionTaskReschedule, "past")); err == nil {
				t.Fatal("invalid deadline accepted")
			}
		}
		if _, err := store.ChangeOpenTask(ctx, original, nil, action("task.delete", "unsupported")); err == nil {
			t.Fatal("unsupported action accepted")
		}
		original.UserID = otherID
		a.IdempotencyKey = "spoofed"
		if _, err := store.ChangeOpenTask(ctx, original, nil, a); !errors.Is(err, ErrNotFound) {
			t.Fatalf("spoofed owner accepted: %v", err)
		}
	})
	t.Run("audit failure is atomic", func(t *testing.T) {
		original := newTask(&deadline)
		if _, err := db.Exec(ctx, "ALTER TABLE agent_actions ADD CONSTRAINT nova_test_task_audit CHECK(trace_id <> 'reject-task-audit')"); err != nil {
			t.Fatal(err)
		}
		defer db.Exec(ctx, "ALTER TABLE agent_actions DROP CONSTRAINT nova_test_task_audit")
		a := action(core.ActionTaskComplete, "rollback")
		a.TraceID = "reject-task-audit"
		if _, err := store.ChangeOpenTask(ctx, original, nil, a); err == nil {
			t.Fatal("audit failure ignored")
		}
		current, err := store.GetTask(ctx, userID, original.ID)
		if err != nil || current.Status != "open" || current.CompletedAt != nil || !current.UpdatedAt.Equal(original.UpdatedAt) {
			t.Fatalf("partial mutation=%+v %v", current, err)
		}
		if receipt, err := store.TaskChangeResult(ctx, userID, a.IdempotencyKey, a.ArgumentsHash); err != nil || receipt != nil {
			t.Fatal("failed receipt saved")
		}
		a.TraceID = "retry-after-rollback"
		if _, err := store.ChangeOpenTask(ctx, original, nil, a); err != nil {
			t.Fatal(err)
		}
	})
}
