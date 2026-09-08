CREATE TABLE IF NOT EXISTS service_heartbeats (
  service_name TEXT NOT NULL,
  instance_id TEXT NOT NULL,
  status TEXT NOT NULL DEFAULT 'ok',
  metadata_json JSONB NOT NULL DEFAULT '{}'::jsonb,
  started_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  last_seen_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (service_name, instance_id)
);

CREATE INDEX IF NOT EXISTS service_heartbeats_service_last_seen_idx
  ON service_heartbeats(service_name, last_seen_at DESC);
