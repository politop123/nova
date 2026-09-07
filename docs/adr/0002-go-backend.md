# ADR 0002 Go backend and Nuxt frontend

Status: accepted

## Context

NOVA needs a long-lived HTTP service, streaming model calls, webhook handling, deterministic scheduling, and a worker that stays efficient on a personal server. The web channel benefits from Nuxt and TypeScript, but that does not require the backend to share its language.

## Decision

Use Go for `apps/api`, `apps/worker`, and backend `internal/` packages. Use Asynq over Redis for background jobs. Use Nuxt for the Web/PWA channel. Define the network boundary with OpenAPI.

The first Go integration is isolated behind `internal/ai/openaiadapter`, which uses the official OpenAI Go SDK and keeps API-specific types out of the rest of the core.

## Consequences

The backend is a small, independently deployable binary with explicit concurrency and graceful shutdown. Shared TypeScript package coupling disappears, so generated or manually maintained OpenAPI contracts become important. Asynq replaces the original BullMQ proposal because BullMQ is Node-specific; Redis remains the same infrastructure dependency.
