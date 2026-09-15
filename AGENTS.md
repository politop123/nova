# NOVA development guide

## Product boundaries

- Keep one server-side NOVA Core. Channels transport input and render output; they do not own agent behavior.
- Treat the Web app as an auxiliary management dashboard for chat, memory, tasks, and debugging. The primary daily conversation and notifications channel is Telegram.
- Telegram must use the same server-side conversation, memory, policy, budget, and audit paths as Web. Do not build separate Telegram-only assistant behavior.
- Telegram voice messages are first-class input: download them server-side, transcribe them, normalize the transcript into `NovaInput`, then answer through NOVA Core.
- Telegram voice files may arrive with a `.oga`/Opus filename; normalize upload metadata to a supported `.ogg` filename and `audio/ogg` content type before sending audio to OpenAI transcription.
- Telegram voice download responses may use generic `application/octet-stream`; prefer Telegram's declared voice MIME type when it is audio-specific, strip MIME parameters, and log voice metadata/dimensions on download or transcription failures without logging bot tokens.
- Telegram webhook decoding must tolerate extra Bot API fields. Keep strict JSON validation for NOVA-owned APIs, but do not reject real Telegram updates because of unknown Telegram payload fields.
- The Web dashboard should reflect the shared conversation state, including Telegram-originated messages, through lightweight refresh/polling and duplicate-safe message merging.
- NOVA must be able to send proactive Telegram messages for reminders and important events after the user subscribes to the bot and the allowlist is configured.
- Reminder delivery must leave an auditable event trail (`attempted`, `sent`, `failed`, `skipped`) with attempt number, channel/provider, details, errors, and idempotency keys. This history is the source of truth for missed notifications and future phone-call escalation.
- NOVA can answer read-only DevOps questions about its own GitHub repository and deployment state. Keep repository status checks server-side, compare the latest GitHub commit with the deployed production SHA, and never expose GitHub tokens to Web or Telegram clients.
- NOVA can answer read-only operational health questions about itself. Build these answers from real server-side checks for API, PostgreSQL, Redis, worker heartbeat/visibility, reminders, Telegram, OpenAI configuration, and Git/deploy state; do not infer a healthy state when a component cannot be checked.
- Long-running background services should report service heartbeats through `service_heartbeats`; treat stale or missing heartbeats as degraded unless another real check proves the service is alive.
- Natural-language action requests can use arbitrary wording. Run them through the model-backed action planner first when OpenAI is configured; execute only validated typed actions (`memory.save`, `task.create`, `reminder.create`, `reminder.cancel`, `reminder.reschedule`) through server-side Go code. Deterministic parsing is only a fallback for obvious commands or when the planner is unavailable.
- `agenda.list` reads real open tasks and scheduled reminders. Resolve the local calendar date through the planner, filter in PostgreSQL before applying limits, and use calendar-day boundaries (including DST). A storage failure must not be reported as an empty agenda.
- Reminder changes must identify one active reminder belonging to the requesting user. Clarify missing/ambiguous subjects, original times, or new times; never guess a fuzzy best match or operate on a truncated candidate list. A provided original time selects at minute precision, matching the times shown in chat.
- Commit reminder cancellation/rescheduling and the successful audit result in one transaction, deduplicate by user/request key, and reject stale reminder versions. Old queued jobs must skip delivery after rescheduling.
- Every scheduled reminder must have a durable `reminder_dispatches` row written in the same transaction through the database trigger. Redis submission is an optimization; acknowledge saving during an outage only after checking that durable queue intent exists for the current schedule. Apply additive migrations before starting API/worker, including local full-stack startup.
- The worker reconciles due queue intents in bounded batches with expiring claims and backoff. Never reset live Asynq retry budgets or automatically replay archived tasks. Recover missing unattempted tasks; if a task vanished after an attempt began, record a terminal failure for manual review because Telegram might already have received it.
- Reserve each delivery attempt in PostgreSQL before contacting Telegram so duplicate consumers of the same attempt cannot both send. Do not claim exactly-once external delivery: timeouts or a crash between Telegram and database confirmation can still leave an uncertain outcome.
- Do not automatically send reminders more than 24 hours overdue. Record their failure and show them, delayed reminders, and queue recovery problems in operational status. Rescheduling to a new future time creates a new recovery intent.
- Web must refresh reminders after chat reminder mutations and new Telegram assistant replies, avoiding refreshes for already merged messages.
- New private Telegram reminder notifications include deterministic `reminder.complete` and `reminder.snooze` controls (10 or 60 minutes), without model calls. `completed` means the user marked the reminder done; `delivered` only means its notification was sent. These controls do not complete a separate task.
- Reminder callbacks require a nonempty verified webhook secret and an explicit private-chat/user allowlist; when both are configured both must match. Never trust callback data alone. Use opaque seven-day grants bound to user, recipient, reminder, and trigger time. Lock and consume each grant once, committing the reminder, durable dispatch intent, audit, and shared chat messages together. Reject cancelled, rescheduled, expired, foreign, or unsupported unconsumed grants. A replay returns the original result without further writes.
- Answer Telegram callbacks promptly and remove the keyboard after success. Provider UI failures must not roll back or repeat a committed action. These owner-only reminder controls are WRITE actions, not the future general-purpose CONFIRM approval mechanism.
- When the user asks for a personal fact or contact detail that is not in memory, NOVA must not stop at "I do not know"; it should ask the user to share the fact so it can remember it for next time. When the user then shares a stable personal/contact fact, save it through `memory.save` with provenance instead of treating it as transient chat.
- Never invent private facts such as phone numbers, addresses, or family contact details. Answer from stored memory only, or explicitly ask the user to provide the missing fact.
- Critical or missed notifications may later escalate to a phone call, but phone delivery must remain idempotent, auditable, budgeted, and bounded by quiet-hours/importance policy.
- Use deterministic code for scheduling, retries, permission checks, budgets, and state machines.
- Do not send full conversation history to a model. Build compact context from profile, rolling summary, relevant memories, and recent messages.
- Every external write, destructive action, payment, or message to another person must pass the CONFIRM policy.
- Log every agent action and tool call with an idempotency key when the operation can be repeated.
- Keep API keys, OAuth tokens, and integration secrets on the server.

## Delivery rules

- Implement v0.1 as the vertical slices in `docs/IMPLEMENTATION_PLAN.md`.
- Add tests with each package or application change.
- Database changes are additive SQL migrations under `infrastructure/postgres/migrations` once development starts; do not silently rewrite production migrations.
- Update architecture decisions in `docs/adr` when a cross-cutting choice changes.
- Voice and native applications remain outside v0.1 unless the core acceptance criteria pass.
- Keep the backend idiomatic Go: small packages, explicit dependencies, `context.Context` for I/O, structured `slog` logging, and graceful shutdown.
- Keep HTTP and backend contracts in `docs/openapi.yaml`; do not couple Go implementation types directly to Nuxt internals.
