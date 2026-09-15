package jobs

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/hibiken/asynq"
	"nova.local/core/internal/storage"
)

type dispatchStoreFake struct {
	items          []storage.ReminderDispatch
	attempted      bool
	attemptErr     error
	status, detail string
	next           time.Time
	stale          bool
}

func (f *dispatchStoreFake) ClaimReminderDispatches(context.Context, int) ([]storage.ReminderDispatch, error) {
	return f.items, nil
}
func (f *dispatchStoreFake) ReminderDeliveryAttempted(context.Context, storage.Reminder) (bool, error) {
	return f.attempted, f.attemptErr
}
func (f *dispatchStoreFake) FinishReminderDispatch(_ context.Context, _ storage.ReminderDispatch, status, detail string, next time.Time) (bool, error) {
	f.status, f.detail, f.next = status, detail, next
	return !f.stale, nil
}

type dispatchQueueFake struct {
	state           asynq.TaskState
	exists          bool
	err, enqueueErr error
	enqueues        int
}

func (q *dispatchQueueFake) State(context.Context, storage.Reminder) (asynq.TaskState, bool, error) {
	return q.state, q.exists, q.err
}
func (q *dispatchQueueFake) Enqueue(context.Context, storage.Reminder) error {
	q.enqueues++
	return q.enqueueErr
}

func TestReminderRecoveryDecisions(t *testing.T) {
	now := time.Now().UTC()
	for _, tc := range []struct {
		name, status       string
		queue              dispatchQueueFake
		attempted, expired bool
		enqueues           int
	}{
		{"unavailable", "pending", dispatchQueueFake{err: errors.New("offline")}, false, false, 0},
		{"restored", "queued", dispatchQueueFake{}, false, false, 1},
		{"retrying", "queued", dispatchQueueFake{exists: true, state: asynq.TaskStateRetry}, true, false, 0},
		{"archived", "failed", dispatchQueueFake{exists: true, state: asynq.TaskStateArchived}, true, false, 0},
		{"completed but unconfirmed", "failed", dispatchQueueFake{exists: true, state: asynq.TaskStateCompleted}, true, false, 0},
		{"missing after attempt", "failed", dispatchQueueFake{}, true, false, 0},
		{"expired", "failed", dispatchQueueFake{}, false, true, 0},
		{"enqueue fails", "pending", dispatchQueueFake{enqueueErr: errors.New("offline")}, false, false, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := testReminder("telegram", "scheduled", now.Add(-time.Minute))
			if tc.expired {
				r.TriggerAt = now.Add(-25 * time.Hour)
			}
			store := &dispatchStoreFake{items: []storage.ReminderDispatch{{Reminder: r}}, attempted: tc.attempted}
			result, err := DispatchReminders(context.Background(), store, &tc.queue, now)
			if err != nil || store.status != tc.status || result.Checked != 1 || tc.queue.enqueues != tc.enqueues {
				t.Fatalf("result=%+v err=%v status=%s enqueues=%d", result, err, store.status, tc.queue.enqueues)
			}
			if tc.status != "queued" && store.detail == "" {
				t.Fatal("failure lost its explanation")
			}
			if tc.status == "pending" && !store.next.Equal(now.Add(5*time.Second)) {
				t.Fatalf("backoff=%v", store.next)
			}
		})
	}
}

func TestDispatchBackoffIsBounded(t *testing.T) {
	if dispatchBackoff(-1) != 5*time.Second || dispatchBackoff(2) != 20*time.Second || dispatchBackoff(1000) != 5*time.Minute {
		t.Fatal("unexpected backoff")
	}
}

func TestStaleClaimCannotReportRecovery(t *testing.T) {
	store := &dispatchStoreFake{stale: true, items: []storage.ReminderDispatch{{Reminder: testReminder("web", "scheduled", time.Now())}}}
	result, err := DispatchReminders(context.Background(), store, &dispatchQueueFake{}, time.Now())
	if err != nil || result.Checked != 0 || result.Enqueued != 0 {
		t.Fatalf("stale claim counted: %+v %v", result, err)
	}
}
