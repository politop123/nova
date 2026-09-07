# NOVA v0.1 implementation plan

## Outcome

The first usable release is a single-user personal assistant that continues the same conversation through Web and Telegram, retrieves long-term memories, creates and delivers reminders, records every action, and reports model cost by feature. Voice is deliberately deferred until the core passes these acceptance criteria.

## Delivery sequence

### Phase 0 Foundation

1. Establish the Go module, Nuxt workspace, environment contract, Docker Compose, PostgreSQL with pgvector, Redis, API, worker, and web shell.
2. Add the initial schema runner and health checks for the API, database, and Redis.
3. Add CI-equivalent local commands for type checking, tests, build, and formatting.

Exit: a new machine can clone the project, copy `.env.example`, start dependencies, and receive healthy responses.

### Phase 1 Identity and conversations

1. Implement a single-user bootstrap that can later become multi-user auth.
2. Register devices and normalized channels.
3. Create conversations, sessions, and messages.
4. Add a streaming text endpoint and Web chat interface. **Done:** the Go API exposes an SSE stream and the Nuxt composer renders deltas while persisting the completed turn.
5. Persist one conversation across channel switches.

Exit: Web can send and stream a response; every message is stored with user, conversation, session, channel, and trace identifiers.

### Phase 2 Agent runtime and cost guardrails

1. Add deterministic routing for obvious commands before any model call.
2. Build the model router with Luna as default and explicit Terra and Sol escalation rules.
3. Enforce per-feature input and output budgets before requests. **Foundation done:** chat requests reserve a conservative input plus 512-token output estimate before calling a model.
4. Record `usage_events`, estimated cost, latency, feature, model, and cached tokens. **Done for chat:** token counts (including cached tokens), model, feature, trace ID, cost, and provider latency are persisted.
5. Add daily and monthly circuit breakers. **Done for chat:** requests are denied with HTTP 429 when either configured limit would be exceeded.

Exit: every model request is attributable and budgeted; a limit stops loops before they create an uncontrolled bill.

### Phase 3 Memory

1. Maintain a rolling conversation summary. **Foundation done:** older turns are compacted into `conversation_summaries` and the model receives a bounded summary plus recent-message window.
2. Extract candidate facts asynchronously after a turn.
3. Store source message, confidence, scope, and expiry.
4. Generate embeddings and retrieve top-K relevant memories.
5. Assemble a compact prompt from profile, summary, memories, and recent messages. **Foundation done:** active memories can be created, searched, edited, soft-deleted, and injected into the bounded model context with confidence and source provenance.
6. Provide a memory dashboard for view, edit, and delete. **API foundation done:** the memory CRUD contract is available; the visual dashboard remains next.

Exit: NOVA answers a question using an old fact without sending the full history and shows which memory supported the answer.

### Phase 4 Tools, permissions, and audit

1. Implement the typed Tool Registry.
2. Enforce READ, WRITE, and CONFIRM policy before execution.
3. Add idempotency for repeatable write operations.
4. Record proposed, confirmed, executed, failed, and cancelled actions.
5. Implement initial tools: memory search/save, task create/complete, reminder create/update/delete, notification send, and conversation search.

Exit: no CONFIRM action runs without a short-lived explicit approval tied to the exact action payload.

### Phase 5 Tasks, reminders, and proactive delivery

1. Add task and reminder APIs with timezone-aware parsing.
2. Schedule deterministic BullMQ jobs.
3. Deliver reminders without an LLM call when stored text is sufficient.
4. Add retries, dead-letter handling, cancellation, and idempotent delivery.
5. Record notification and delivery status.

Exit: create, edit, cancel, and receive a reminder reliably across restarts.

### Phase 6 Telegram

1. Verify the configured Telegram user allowlist.
2. Normalize incoming text and voice-message transcripts into `NovaInput`.
3. Route Telegram and Web through the same conversation service.
4. Deliver proactive reminders and confirmation buttons.
5. Reject replayed callbacks and untrusted users.

Exit: the same conversation continues between Web and Telegram, and proactive reminders arrive in Telegram.

### Phase 7 Operational hardening

1. Add structured logs, traces, metrics, rate limits, and backups.
2. Encrypt integration secrets and define data retention rules.
3. Add failure drills for Redis, PostgreSQL, provider timeouts, and repeated webhook delivery.
4. Build cost and audit dashboards.
5. Run the complete v0.1 acceptance suite.

Exit: the v0.1 readiness checklist in the source concept passes in a production-like environment.

## Next releases

- v0.2: push-to-talk, local VAD, streaming transcription, native Apple TTS, Calendar, Gmail, and Contacts.
- v0.3: on-demand Realtime voice, iOS, macOS, wake word, and outbound calls.
- v0.4: TSell integration, business dashboards, anomaly detection, and project memory.
- v1.0: advanced proactive policy, smart home, routines, and multi-device context.

## Backend implementation conventions

- Go `internal/` packages own agent, memory, tools, policy, integrations, voice contracts, and infrastructure clients.
- The OpenAI Go SDK is isolated behind `internal/ai/openaiadapter`; the rest of the core depends on a small interface.
- Redis jobs use Asynq; job payloads are versioned and idempotent.
- Nuxt communicates with the Go API through the OpenAPI contract.

## Definition of done for every slice

- User-visible acceptance scenario passes.
- Unit and integration tests cover success, denial, retry, and duplicate delivery.
- New data is auditable and deletable.
- Cost and latency are recorded when AI is used.
- Secrets stay server-side.
- Documentation and environment examples remain current.
