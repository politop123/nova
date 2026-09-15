# ADR 0006: Task lifecycle through the shared chat planner

Date: 2026-09-15

Status: accepted

## Context

Tasks could be created in conversation but required the Web dashboard to complete or cancel. Telegram should support the same full daily workflow while keeping task deadlines distinct from notification schedules.

## Decision

Add `task.complete`, `task.cancel`, and `task.reschedule` to the existing planner capability contract, structured schema and Go execution path. Preserve the current model configuration, budget checks and endpoint. Extend the strict schema in accordance with [OpenAI Structured Outputs](https://developers.openai.com/api/docs/guides/structured-outputs); schema adherence is not authorization or evidence that the model interpreted a request correctly.

Include at most twenty open task titles/deadlines in compact planner input. Titles over 160 runes are omitted instead of converted to potentially misleading truncated selectors. The preview marks incompleteness and treats task text as data. It helps resolve paraphrased completion reports but is never the source of truth for selecting a unique task.

Go applies WRITE policy and matches all provided subject words against complete normalized title words of current open tasks belonging to the user. An explicitly identified original deadline can narrow candidates at minute precision; the new deadline belongs in `dueAt`, not `targetTime`. Ambiguity, an empty subject, invalid/past new deadlines, or a truncated search of 100 or more open tasks produces clarification, not a best guess. Closed tasks are not implicitly reopened. Negated, hypothetical, uncertain, quoted and third-party reports are excluded by planner instructions.

The storage transaction serializes each user/request key with an advisory lock, checks the argument hash on receipts, and updates only an open task with the expected `updated_at`. It stores the resulting task snapshot in the successful `agent_actions` receipt in the same transaction. Audit failure rolls back the task. A repeated executed request returns its original receipt; chat checks current state before presenting an old result. This deduplicates the same planner request; general inbound Telegram update deduplication remains separate work.

Completing sets status `done` and `completed_at`; cancelling sets `cancelled` and preserves history. Both preserve the original deadline. Rescheduling changes the deadline and leaves the task open. None of these actions executes the real-world task, sends payments, creates notifications, or silently changes reminders.

Existing chat persistence stores the validated reply for both channels. A `refreshTasks` response/SSE hint requests a task-list reload even when the top-level plan intent differs; it does not itself assert success. New Telegram assistant replies refresh both tasks and reminders. Low-confidence or empty task-mutation plans cannot pass through a proposed model success reply.

## Verification and limits

- Unit tests cover schema/capability consistency, bounded task previews, safe fallback, missing/ambiguous subjects, whole-word/time matching, stale receipts and Web refresh signals.
- Disposable PostgreSQL tests verify all three state transitions, concurrent retries, owner isolation, invalid deadlines, stale versions, preserved deadlines, argument mismatch and audit rollback.
- Fake-planner/provider integration tests exercise Web JSON, SSE and Telegram through shared state/history, including low confidence and empty action plans. They do not prove live-model natural-language accuracy; no paid model calls were used.
- No migration is needed: existing task fields and audit rows support this slice. Reopening tasks, removing deadlines, bulk changes, recurrence and task/reminder linking remain out of scope.
