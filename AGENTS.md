# NOVA development guide

## Product boundaries

- Keep one server-side NOVA Core. Channels transport input and render output; they do not own agent behavior.
- Treat the Web app as an auxiliary management dashboard for chat, memory, tasks, and debugging. The primary daily conversation and notifications channel is Telegram.
- Telegram must use the same server-side conversation, memory, policy, budget, and audit paths as Web. Do not build separate Telegram-only assistant behavior.
- Telegram voice messages are first-class input: download them server-side, transcribe them, normalize the transcript into `NovaInput`, then answer through NOVA Core.
- NOVA must be able to send proactive Telegram messages for reminders and important events after the user subscribes to the bot and the allowlist is configured.
- Natural-language action requests can use arbitrary wording. Run them through the model-backed action planner first when OpenAI is configured; execute only validated typed actions (`memory.save`, `task.create`, `reminder.create`) through server-side Go code. Deterministic parsing is only a fallback for obvious commands or when the planner is unavailable.
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
