# NOVA

NOVA is a personal AI operating system with one server-side core for every channel. The first release focuses on the capabilities that make the product useful every day: continuous text conversations, long-term memory, reminders, proactive delivery, Telegram, safe tool execution, and cost telemetry.

The source concept is preserved at [`docs/NOVA_Technical_Concept_and_Roadmap_UA.docx`](docs/NOVA_Technical_Concept_and_Roadmap_UA.docx). The implementation order is defined in [`docs/IMPLEMENTATION_PLAN.md`](docs/IMPLEMENTATION_PLAN.md).

## Architecture

- `apps/api` - Go HTTP API and NOVA Core entry point.
- `apps/worker` - Go/Asynq jobs, reminder delivery, and proactive events.
- `apps/web` - Nuxt PWA shell.
- `internal/agent` - deterministic routing and model selection.
- `internal/memory` - context budgets and memory contracts.
- `internal/tools` - tool contracts and registry.
- `internal/policy` - READ, WRITE, and CONFIRM decisions.
- `internal/integrations` - adapters for Telegram and later providers.
- `internal/voice` - voice contracts reserved for v0.2.
- `internal/core` - channel-neutral input and output contracts.
- `infrastructure` - local PostgreSQL, pgvector, Redis, and container setup.

## Local start

Requirements: Go 1.25+, Node.js 22+, pnpm 10+, and Docker Desktop.

```bash
cp .env.example .env
pnpm install
pnpm infra:up
pnpm db:init
go run ./apps/api
```

In a second terminal, run `go run ./apps/worker`; in a third, run `pnpm dev` for the Web shell. `pnpm infra:full` starts the complete local stack in Docker.

The same commands are available as `make api`, `make worker`, `make web`, and `make verify`.

Default local endpoints:

- Web: `http://localhost:3000`
- API health: `http://localhost:4000/health`
- PostgreSQL: `localhost:5432`
- Redis: `localhost:6379`

If another local PostgreSQL instance already occupies port 5432, set
`POSTGRES_HOST_PORT=55432` and point `DATABASE_URL` to port 55432 before
starting the infrastructure.

`OPENAI_API_KEY` is optional for the scaffold. The health endpoint and infrastructure can run without it. Never place secrets in the web application or commit `.env`.

The schema runner is safe to repeat; Docker also applies the same initial
schema automatically when it creates a new PostgreSQL volume.

## Quality checks

```bash
go test ./...
go vet ./...
go build ./...
pnpm typecheck
pnpm test
pnpm build
pnpm format:check
```

## Current state

This repository is the implementation foundation for v0.1. It includes Go core contracts, a working API health endpoint, an Asynq worker bootstrap, a Nuxt PWA shell, local infrastructure, and the initial database schema. The current slice includes persisted conversations, a model-backed structured action planner with deterministic fallback, SSE text streaming, bounded conversation summaries, managed memories with provenance and soft-delete, tasks, scheduled reminders, Telegram webhook/voice handling, model usage accounting, action audit records, and daily/monthly budget guards. Product features are intentionally delivered as vertical slices in the order documented in the implementation plan.
