-- Persist queue intent in the same transaction as the reminder, for every write path.
CREATE TABLE IF NOT EXISTS reminder_dispatches (
  reminder_id UUID PRIMARY KEY REFERENCES reminders(id) ON DELETE CASCADE,
  trigger_at TIMESTAMPTZ NOT NULL,
  status TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'queued', 'failed', 'inactive')),
  failures INTEGER NOT NULL DEFAULT 0 CHECK (failures >= 0),
  next_check_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  lease_token UUID,
  lease_until TIMESTAMPTZ,
  last_error TEXT NOT NULL DEFAULT '',
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS reminder_dispatches_ready_idx ON reminder_dispatches(next_check_at)
  WHERE status IN ('pending', 'queued');

CREATE OR REPLACE FUNCTION nova_sync_reminder_dispatch() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF NEW.status <> 'scheduled' THEN
    UPDATE reminder_dispatches SET status = 'inactive', lease_token = NULL, lease_until = NULL,
      last_error = '', updated_at = now() WHERE reminder_id = NEW.id;
  ELSIF TG_OP = 'INSERT' OR OLD.status IS DISTINCT FROM NEW.status OR OLD.trigger_at IS DISTINCT FROM NEW.trigger_at THEN
    INSERT INTO reminder_dispatches(reminder_id, trigger_at) VALUES (NEW.id, NEW.trigger_at)
    ON CONFLICT (reminder_id) DO UPDATE SET trigger_at = EXCLUDED.trigger_at, status = 'pending',
      failures = 0, next_check_at = now(), lease_token = NULL, lease_until = NULL, last_error = '', updated_at = now();
  END IF;
  RETURN NEW;
END;
$$;

CREATE OR REPLACE TRIGGER nova_reminder_dispatch_sync
AFTER INSERT OR UPDATE OF trigger_at, status ON reminders
FOR EACH ROW EXECUTE FUNCTION nova_sync_reminder_dispatch();

-- Existing reminders also participate in recovery; the relay bounds overdue delivery.
INSERT INTO reminder_dispatches(reminder_id, trigger_at)
SELECT id, trigger_at FROM reminders WHERE status = 'scheduled'
ON CONFLICT (reminder_id) DO NOTHING;
