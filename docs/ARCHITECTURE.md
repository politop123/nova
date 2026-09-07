# NOVA architecture

## Core boundary

All channels create the same `NovaInput`. NOVA Core owns orchestration, memory retrieval, policy, tools, audit, and output. Web, Telegram, iOS, macOS, and phone adapters must never fork agent behavior.

```text
channel -> normalized input -> deterministic route -> compact context
        -> model decision -> policy -> tools -> audit -> response
        -> asynchronous summary and memory extraction
```

## Runtime responsibilities

- API: Go HTTP service that validates requests, authenticates users and devices, streams responses, and exposes management endpoints.
- Worker: Go/Asynq service that schedules reminders, processes asynchronous memory extraction, retries provider calls, and emits proactive delivery events.
- PostgreSQL: source of truth for identity, conversations, memory, tasks, reminders, policy state, audit, integrations, and usage.
- pgvector: semantic retrieval over approved memories.
- Redis and Asynq: disposable job transport; PostgreSQL remains the durable source of truth.
- Web: PWA channel and management dashboard.

## Conversation context and cost safety

The chat path loads a bounded rolling summary plus the most recent messages,
then sends that compact context to the selected model. After a turn, older
messages are compacted into `conversation_summaries`; this keeps prompt size
predictable before semantic memory extraction is added.

Managed memories are user-owned records with a kind, confidence, optional
project scope, source-message provenance, expiry, and soft deletion. The
current retrieval foundation uses PostgreSQL full-text search; pgvector
embeddings will replace or complement it once the embedding worker is added.

Every model turn performs a preflight budget check, records provider token
usage (including cached input), estimated USD cost, model, feature, trace ID,
and latency in `usage_events`. Daily and monthly limits fail closed with HTTP
429 before a provider call.

## Why Go for the backend

The backend is mostly long-lived I/O: HTTP streaming, provider calls, webhooks, reminder scheduling, and concurrent delivery. Go gives NOVA a small deployable binary, predictable concurrency, simple operational footprint, and a strong standard library. Nuxt remains separate because the web product benefits from its TypeScript-first UI ecosystem. OpenAPI is the boundary between the two.

## Prompt budget

The default context target is 2,000 to 4,000 tokens:

- profile: at most 500 tokens;
- rolling summary: at most 700 tokens;
- five to eight memories: at most 1,000 tokens;
- recent messages: at most 1,000 tokens.

## Trust boundary

Model output is a proposal, not authority. The policy package decides whether a tool may execute. A confirmation binds the user, action name, normalized arguments hash, expiry, and idempotency key. Database, shell, and provider credentials are never directly exposed to the model.
