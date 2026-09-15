# Chat reminder management

Status: accepted, 2026-09-15.

Telegram and Web use the same model planner and typed Go executor for agenda queries, reminder cancellation, and rescheduling. The planner resolves a local agenda date or a reminder subject, optional original time, and new trigger time. Go resolves the subject against the current user's active reminders. Ambiguous or truncated searches ask for clarification; they never select a fuzzy best match.

Agenda queries filter dates in PostgreSQL before limiting results. Local calendar-day boundaries handle daylight-saving transitions. Undated tasks appear in the general agenda only.

Reminder changes use a PostgreSQL transaction with a request-scoped advisory lock and an optimistic reminder version check. The transaction persists the changed reminder and an audit record containing the result. Repeated requests return that result, and failed audits roll back the change. Queue submission follows the commit. The initial manual queue recovery limitation is superseded by [durable dispatch recovery](0004-durable-reminder-dispatch.md).

Queued deliveries compare their original trigger with the current reminder before sending. Stale jobs are skipped and audited. Changes cannot recall a Telegram message whose delivery has already started; exactly-once external delivery remains a separate worker concern.
