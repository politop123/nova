# ADR 0005: Single-use Telegram reminder controls

Date: 2026-09-15

Status: accepted

## Context

Telegram is NOVA's primary daily channel. Requiring a new AI conversation just to mark a reminder done or postpone it adds latency and cost. Callback payloads are untrusted and may be repeated or arrive while a delivery worker is still finalizing.

## Decision

New private reminder notifications offer `reminder.complete`, `reminder.snooze` for ten minutes, and `reminder.snooze` for one hour. They enter typed Go actions after WRITE policy checks, never the AI planner. A reminder's `delivered` state means notification sent; `completed` is the user's explicit completion. Completing a reminder does not imply completing an unrelated task.

Store a random UUID grant before delivery, bound to the reminder, schedule timestamp, NOVA user, Telegram channel, and private recipient. The grant expires in seven days; delivery retries reuse it without resetting expiry or consumption. The keyboard contains only the grant and a fixed action selector, not titles, user data, or secrets.

The webhook must verify a configured secret and explicit allowlist. A callback's sender must be a human matching the private chat; both configured user/chat allowlist entries must match. Inaccessible messages, inline-mode callbacks and groups cannot execute controls. Grant ownership and the current schedule/status are rechecked in PostgreSQL.

Lock the grant and reminder and atomically commit the mutation, existing durable-dispatch trigger, successful action audit, two shared-conversation messages, and consumed result. Concurrent or different-button replays return the first committed result. Cancelled, rescheduled, expired or foreign grants do not mutate anything. Both `scheduled` and `delivered` are actionable because the user may click before the worker confirms delivery; existing schedule/status guards prevent the worker overwriting completion or snooze.

After a database result, acknowledge the callback and replace the original notification with the result and an empty keyboard. Keep DB and provider calls bounded. A provider UI failure does not repeat or undo the committed action; the Web dashboard can still show it through shared-message polling. Telegram notification delivery itself remains subject to the existing uncertain external-delivery window.

The Telegram adapter follows the [Bot API callback contract](https://core.telegram.org/bots/api#callbackquery), [inline keyboard fields](https://core.telegram.org/bots/api#inlinekeyboardbutton), and [callback acknowledgement method](https://core.telegram.org/bots/api#answercallbackquery).

## Consequences and verification

- Old Telegram messages are not retrofitted. Newly snoozed deliveries issue a grant for the new schedule.
- The worker's PostgreSQL dispatcher queues snoozed reminders even during Redis outages; callback success does not depend on immediate Redis submission.
- These owner-only controls are not general-purpose CONFIRM approval for payments, destructive actions, or messages to other people.
- Adapter tests verify payloads, action selectors and keyboard removal. PostgreSQL tests cover concurrency, ownership, expiry, stale schedules, worker-finalization races, durable dispatch, shared history and audit rollback. Webhook tests cover authentication, unknown update fields, replay and provider UI failure. Web tests distinguish delivered/completed labels and read-only terminal history.
- Grants are retained with reminder/audit history and cascade when the reminder/user is deleted. Expiry blocks execution; a separate retention policy may prune expired grant history later.
