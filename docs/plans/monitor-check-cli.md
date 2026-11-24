# vMonitor Check Notification Flags Plan

**Goal:** Attach or clear check notification channel lists without writing JSON, and point generic pricing help to vMonitor quotes.

**Approach:** Adapt flags to the existing `CheckNotifications` input at the command boundary. Do not add a provider call or change the SDK's full replacement on update. A partial update would need another read or a new SDK merge contract; neither is needed to expose the existing operation safely.

**Contracts:** [Check notifications](../design/monitor-alerts.md#check-notifications) and [vMonitor quotes](../design/monitor-quotas.md#quote-and-price-contract).

## Command behavior

- Both check commands accept repeatable `--in-alarm-channel-id`, `--up-channel-id`, and `--undetermined-channel-id` flags, one ID per occurrence. An explicitly empty value clears that list. Mixing an empty occurrence with another occurrence of the same flag, or supplying a whitespace-only ID, fails with exit 2 before any request.
- On create, each supplied flag replaces just its list and leaves the other JSON lists alone. On update, require all three flags or none; explicit empty values count. A full flag set replaces JSON `Notifications`. Without flags, existing JSON and no-change behavior stay unchanged.
- Use command-local state and the existing write guard and extra flag hooks. Do not add new SDK fields, requests, dependencies, or changes to write classification, output, or exit semantics.
- In `pricing get-quote` long help and its generated reference, name `monitor quote-create-notification-quota`, `monitor quote-create-synthetic-quota`, `monitor quote-renew-synthetic-quota`, and `monitor quote-create-log-project`. Do not imply the generic quote covers vMonitor.

## Execution

- [x] In `internal/cli`, write command tests for the full wire body, repeatable flags, explicit clears, JSON overlay on create and on update, partial update refusal, and bad input refusal with zero requests. Run them against the existing commands and observe failures. Existing JSON-only tests must still pass.
- [x] Add command-local flags and the input guard for `CreateCheckInput` and `UpdateCheckInput`. Run the focused check tests. Check that an ordinary update without flags sends the current lists unless JSON supplies a replacement, and a read-only profile sends no request.
- [x] Add a help test for the four vMonitor quote commands, observe failure, then update the pricing help source. Update the vMonitor generator notes and user examples, and run `make gen-docs` to update the CLI reference pages.
- [ ] Run `make check` with the pinned tools. The manager reviews the full diff, gets independent review before landing, removes the completed flags TODO, leaves the replacement or upgrade quote TODO gated, and records a release note.
