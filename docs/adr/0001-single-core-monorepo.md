# ADR 0001 Single NOVA Core in a pnpm monorepo

Status: accepted

## Context

NOVA must preserve memory, policy, and action semantics across several user channels. Duplicating orchestration inside channel applications would create inconsistent behavior and fragmented audit trails.

## Decision

Use one TypeScript pnpm monorepo. NestJS hosts NOVA Core, Nuxt provides the web channel, BullMQ workers execute asynchronous jobs, and shared packages define agent, memory, tools, policy, voice, integrations, and transport contracts.

## Consequences

Channels remain thin and replaceable. Cross-cutting types can evolve together. Deployments can still separate API, worker, and web processes. Package boundaries must stay explicit so the monorepo does not become one undifferentiated application.
