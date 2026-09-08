CREATE TABLE IF NOT EXISTS reminder_delivery_events (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  reminder_id UUID NOT NULL REFERENCES reminders(id) ON DELETE CASCADE,
  user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  channel TEXT NOT NULL,
  provider TEXT,
  status TEXT NOT NULL CHECK (status IN ('attempted', 'sent', 'failed', 'skipped')),
  attempt INTEGER NOT NULL DEFAULT 1 CHECK (attempt >= 1),
  detail TEXT,
  error_text TEXT,
  idempotency_key TEXT,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (user_id, idempotency_key)
);

CREATE INDEX IF NOT EXISTS reminder_delivery_events_reminder_created_idx
  ON reminder_delivery_events(reminder_id, created_at DESC);

CREATE INDEX IF NOT EXISTS reminder_delivery_events_user_created_idx
  ON reminder_delivery_events(user_id, created_at DESC);
