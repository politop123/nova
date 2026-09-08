package jobs

import (
	"context"
	"log/slog"
	"sync"
	"testing"
	"time"

	"nova.local/core/internal/storage"
)

type fakeHeartbeatStore struct {
	mu         sync.Mutex
	beats      []storage.ServiceHeartbeat
	beatSignal chan storage.ServiceHeartbeat
}

func newFakeHeartbeatStore() *fakeHeartbeatStore {
	return &fakeHeartbeatStore{beatSignal: make(chan storage.ServiceHeartbeat, 4)}
}

func (s *fakeHeartbeatStore) UpsertServiceHeartbeat(ctx context.Context, heartbeat storage.ServiceHeartbeat) error {
	s.mu.Lock()
	s.beats = append(s.beats, heartbeat)
	s.mu.Unlock()
	select {
	case s.beatSignal <- heartbeat:
	default:
	}
	return nil
}

func TestStartHeartbeatWritesImmediately(t *testing.T) {
	store := newFakeHeartbeatStore()
	stop := StartHeartbeat(context.Background(), slog.Default(), store, HeartbeatConfig{
		ServiceName:  ServiceWorker,
		InstanceID:   "worker-test",
		Metadata:     map[string]string{"role": "asynq"},
		Interval:     time.Hour,
		WriteTimeout: time.Second,
	})
	defer stop()

	select {
	case beat := <-store.beatSignal:
		if beat.ServiceName != ServiceWorker || beat.InstanceID != "worker-test" || beat.Status != "ok" {
			t.Fatalf("unexpected heartbeat: %#v", beat)
		}
		if beat.Metadata["role"] != "asynq" {
			t.Fatalf("unexpected heartbeat metadata: %#v", beat.Metadata)
		}
	case <-time.After(time.Second):
		t.Fatal("expected an immediate heartbeat")
	}
}
