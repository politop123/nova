package httpserver

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"nova.local/core/internal/storage"
)

func TestReminderHealthReflectsDispatchProblems(t *testing.T) {
	for _, stats := range []storage.ReminderDispatchStats{{Pending: 1}, {Failed: 1}, {Overdue: 1}} {
		if check := reminderDispatchStatusCheck(stats, nil); check.Status != checkStatusDegraded {
			t.Fatalf("problem appears healthy: %+v", check)
		}
	}
	if check := reminderDispatchStatusCheck(storage.ReminderDispatchStats{Scheduled: 1000}, nil); check.Status != checkStatusOK {
		t.Fatalf("healthy state: %+v", check)
	}
	if check := reminderDispatchStatusCheck(storage.ReminderDispatchStats{}, errors.New("offline")); check.Status != checkStatusDegraded {
		t.Fatal("database failure appears healthy")
	}
}

func TestScheduleReminderAcceptsDurableDispatchIntegration(t *testing.T) {
	url := os.Getenv("NOVA_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("disposable database is not configured")
	}
	ctx := context.Background()
	db, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, path := range []string{"../../../infrastructure/postgres/init.sql", "../../../infrastructure/postgres/migrations/202609150001_reminder_dispatches.sql"} {
		sql, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = db.Exec(ctx, string(sql)); err != nil {
			t.Fatal(err)
		}
	}
	var userID string
	if err = db.QueryRow(ctx, "INSERT INTO users DEFAULT VALUES RETURNING id::text").Scan(&userID); err != nil {
		t.Fatal(err)
	}
	defer db.Exec(ctx, "DELETE FROM users WHERE id=$1::uuid", userID)
	store := storage.New(db)
	r, err := store.CreateReminder(ctx, storage.Reminder{UserID: userID, Title: "offline queue", TriggerAt: time.Now().Add(time.Hour), Timezone: "Europe/Kyiv", Status: "scheduled", Priority: "normal", DeliveryMethod: "web"}, "test")
	if err != nil {
		t.Fatal(err)
	}
	s := &Server{store: store, logger: slog.Default()}
	if err = s.scheduleReminder(ctx, r); err != nil {
		t.Fatalf("durable reminder rejected while queue unavailable: %v", err)
	}
	if _, err = db.Exec(ctx, "DELETE FROM reminder_dispatches WHERE reminder_id=$1::uuid", r.ID); err != nil {
		t.Fatal(err)
	}
	if err = s.scheduleReminder(ctx, r); err == nil {
		t.Fatal("missing durable request was acknowledged")
	}
}
