# Durable reminder dispatch

Status: accepted, 2026-09-15.

A committed reminder must survive a failed Redis enqueue or a process crash before enqueue. An additive migration installs `reminder_dispatches` and a trigger on reminder insertion, trigger-time changes, and status changes. The same PostgreSQL transaction commits the reminder, dispatch intent, and (for chat changes) action audit. The migration backfills existing scheduled reminders without resetting existing dispatch results.

The worker checks 25 eligible dispatches every five seconds, within a 24-hour scheduling horizon. PostgreSQL claims expire in 30 seconds; random claim tokens prevent stale completions from overwriting a reschedule, cancellation, or another worker's claim. Each batch has a 20-second context deadline. Redis errors back off from five seconds to five minutes. Existing queued jobs are checked once per minute without altering their retry budget. New future schedules reset dispatch state. Cancelled/delivered reminders become inactive.

API requests verify durable dispatch intent before accepting a saved reminder. Immediate queue submission improves latency, but Redis unavailability does not turn an accepted durable reminder into a failed creation response. Local and production startup must run migrations before API/worker.

Missing, unattempted tasks can be reconstructed after Redis data loss. Archived or completed-but-unconfirmed tasks, and missing tasks with a recorded delivery attempt, are marked failed for review. A failed dispatch and its delivery-history event commit atomically. Reminders over 24 hours late are also stopped, including already queued jobs, to prevent obsolete notifications after a long outage.

Each provider attempt reserves a unique audit key in PostgreSQL before sending. This blocks concurrent consumers of the same attempt. It does not provide exactly-once Telegram delivery: timeouts, retries with a new attempt number, or a crash between provider acceptance and database confirmation can still create uncertainty. Automatic queue reconstruction deliberately stops when a prior attempt might have succeeded.

Operational status reports total scheduled reminders, imminent pending dispatches (within five minutes), reminders over two minutes late, and failed dispatches. Failed reminders remain visible and can be cancelled or moved to a new future time. The legacy `scheduled_jobs` table remains intact; new reminder scheduling uses `reminder_dispatches`.
