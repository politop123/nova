# NOVA development guide

## Product boundaries

- Keep one server-side NOVA Core. Channels transport input and render output; they do not own agent behavior.
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
