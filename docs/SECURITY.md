# NOVA security baseline

## Secrets

- Keep API keys and OAuth refresh tokens in server-side secret storage.
- Encrypt integration credentials at rest with a separately managed key.
- Never expose provider secrets through Nuxt runtime public configuration.

## Actions

- Treat model plans as proposals only. The Go backend validates every typed action, applies policy and idempotency, and writes an audit record before any state-changing execution.
- READ actions may execute automatically within the authenticated user's scope.
- WRITE actions may execute automatically only when reversible and explicitly allowed by policy.
- CONFIRM actions require explicit approval for the exact payload before external communication, deletion, cancellation, payment, or another irreversible effect.
- Apply idempotency keys to reminder delivery, external messages, payments, and webhook processing.

## Data

- Every memory keeps provenance, confidence, scope, and optional expiry.
- Users can inspect, edit, and delete memories.
- Raw audio and transcripts have separate retention settings.
- Audit events are append-only at application level and avoid storing secrets.

## Operations

- Enforce rate limits, daily and monthly cost budgets, and loop detection.
- Use least-privilege database and provider credentials.
- Back up PostgreSQL and test restoration.
- Do not grant a model unrestricted SQL, shell, filesystem, or network access in production.
