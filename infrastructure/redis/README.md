# Redis

Redis is the disposable transport for Asynq jobs. PostgreSQL remains the source of truth for reminders, delivery state, audit, and usage. A Redis restart must be safe because jobs are idempotent and can be reconstructed from durable records.
