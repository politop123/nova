# PostgreSQL and pgvector

The local database is created by `init.sql` when the Docker volume is first initialized. It contains the durable NOVA entities from the concept: conversations, summaries, memories, embeddings, tasks, reminders, reminder delivery events, service heartbeats, policy, audit, notifications, usage events, and model budgets.

Future production changes belong in timestamped files under `migrations/`. Do not edit an already-applied migration; add a new one.

Run `pnpm db:init` after starting local PostgreSQL; it applies the initial schema and additive migrations by default. The full Docker stack runs its migration service before API/worker. Reminder dispatch recovery requires `202609150001_reminder_dispatches.sql`, which adds the durable dispatch table and the trigger that keeps it in sync with every reminder write.
