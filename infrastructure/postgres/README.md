# PostgreSQL and pgvector

The local database is created by `init.sql` when the Docker volume is first initialized. It contains the durable NOVA entities from the concept: conversations, summaries, memories, embeddings, tasks, reminders, policy, audit, notifications, usage events, and model budgets.

Future production changes belong in timestamped files under `migrations/`. Do not edit an already-applied migration; add a new one.
