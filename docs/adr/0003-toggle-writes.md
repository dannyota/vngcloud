# 0003. Toggle writes: read first, send once, confirm by reading

Status: Accepted

## Context

Some GreenNode APIs change state with a toggle: a request that names no
target state and flips the current one. vMonitor's
`PUT /uptimes/status/{id}` is the first the SDK meets: it takes an empty
body and flips a check between `ENABLED` and `DISABLED`
([vMonitor](../design/monitor.md)).

A toggle breaks two things [ADR 0002](0002-write-api-conventions.md) relies
on:

- The transport treats every `PUT` as idempotent and retries it after a 5xx
  or a network error. A toggle sent twice undoes itself, and the caller sees
  success.
- Rule 2 still lets a non-idempotent request retry after a 429 or a failed
  dial, because the server cannot have acted. That holds for one request,
  but any resend acts on a status read taken before the first attempt. The
  longer the gap, the more likely that read is stale.

Callers want a target state, not a flip. aboutme's deploy pauses a check and
later resumes it; either step must be safe to run twice.

## Decision

1. The SDK never exposes a raw toggle. It exposes one operation per target
   state, such as `PauseCheck` and `ResumeCheck`.
2. The operation reads the current state first. When it already equals the
   target, the operation sends nothing and reports no change. When it is a
   value the SDK does not know, the operation sends nothing and fails.
3. The toggle request is sent at most once. `transport.Request` gains
   `Once`: no retry after any status or error, including a 429 and a failed
   dial, and no resend after a 401. This narrows ADR 0002 rule 2 for
   toggles; the rule stands for every other request.
4. After sending, the operation confirms by reading only. Reads may repeat
   within a bound that the service design states. The operation never sends
   the toggle again to reach the target.
5. A response that shows the server did not act (any 4xx, or a failed dial)
   returns that error. Any other failure, or a confirm read that never shows
   the target, returns a service sentinel meaning "the state is unconfirmed".
   The recovery is to run the same operation again, because it reads first.
6. The CLI never retries a toggle operation, and neither does the SDK above
   the transport.
7. A pause is not destructive (ADR 0002 rule 6), because the resume command
   undoes it. Toggle operations still get request, success, and error
   status tests, and an adversarial review before release (rule 9).

## Consequences

- A caller gets idempotent pause and resume: running either twice leaves the
  same state.
- The read-then-toggle race remains. Two callers that read the same state
  and both toggle leave it flipped back. The confirm read detects this for
  each caller and returns the unconfirmed sentinel; nothing prevents it. The
  API offers no conditional request.
- A toggle costs two or more requests instead of one.
- A transient 5xx or network error on the toggle reaches the caller as the
  unconfirmed sentinel when the confirm reads cannot settle it, instead of
  being retried away.
