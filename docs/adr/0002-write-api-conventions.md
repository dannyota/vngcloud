# 0002. Write API conventions

Status: Accepted

## Context

Budget writes in [billing](../design/billing.md) are the SDK's first write
APIs. Every later write, including paid creates, will copy their shape, so
their conventions last. Four problems need one answer each:

- The transport retries a request after a 5xx or a network error. A retried
  `POST` create can create a second resource, and a paid one costs money.
- Update APIs take partial bodies. A plain Go field cannot tell "set to
  false or 0" from "leave unchanged".
- Write paths carry IDs from the caller. `routes.URL` escapes `/` but not
  `..`, so an ID could make a write reach a different path.
- A user must be able to price a paid create before running it, and the
  quote must describe the same resource the create would make.

## Decision

1. Kind follows side effect. An operation is a write when it changes server
   state, whatever its HTTP method. A read that uses `POST`, such as a price
   quote, is a read.
2. No blind retry of non-idempotent requests. A `transport.Request` states
   whether it is idempotent, and `POST` defaults to not idempotent. A
   non-idempotent request is retried only after a 429 or a failed dial,
   where the server cannot have acted. A `POST` that is safe to repeat, such
   as a quote, sets idempotent explicitly. The CLI never retries a write.
3. Partial updates use pointers. Every optional field of an update Input is
   a pointer, and the SDK sends only non-nil fields. A create Input uses a
   pointer only where the zero value differs from the default. The CLI sets
   a pointer field only when the user gives its flag.
4. IDs in write paths are checked. The SDK rejects a path ID outside
   `^[A-Za-z0-9-]+$` before any request. A service whose IDs need other
   characters states its pattern in its design.
5. The SDK checks only required fields and input shape. Value rules, such as
   name patterns and limits, stay on the server, so a server change never
   blocks a valid request.
6. Destructive means not undoable by one more command. Such commands need
   `--yes`. A pause is not destructive; a delete is.
7. A write waits only when the API is asynchronous, and the design for that
   write defines the wait.
8. Every paid create has a quote that takes the create's own Input and
   builds the price request with the create's own code.
9. Every write API gets request body, success, and error status tests, and
   an adversarial review before its release.

## Consequences

- Transient 5xx errors on creates reach the caller instead of being hidden.
  The caller checks whether the resource exists before trying again.
- Update Inputs are less convenient in Go literals, which need pointer
  helpers. The SDK adds a generic `vngcloud.Ptr` helper.
- The CLI flag reflection gains pointer types.
- A paid create design costs a little more, because it must ship its quote
  too.
- The transport change touches every service, but reads keep their current
  retry behavior.
