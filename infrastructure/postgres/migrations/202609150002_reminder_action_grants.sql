CREATE TABLE IF NOT EXISTS reminder_action_grants (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  reminder_id UUID NOT NULL REFERENCES reminders(id) ON DELETE CASCADE,
  user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  trigger_at TIMESTAMPTZ NOT NULL,
  channel TEXT NOT NULL,
  recipient TEXT NOT NULL,
  expires_at TIMESTAMPTZ NOT NULL DEFAULT now() + interval '7 days',
  consumed_at TIMESTAMPTZ,
  result_json JSONB,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (reminder_id, trigger_at, channel, recipient),
  CHECK ((consumed_at IS NULL) = (result_json IS NULL))
);

CREATE INDEX IF NOT EXISTS reminder_action_grants_user_idx ON reminder_action_grants(user_id);
