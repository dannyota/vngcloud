# vServer Free Writes: Checks

Status: Accepted (2026-09-27), with [vServer free writes](vserver-writes.md).

The tests and live checks for [vServer free writes](vserver-writes.md).
Terms and sentinels are defined there.

## Unit tests

Unit tests use `httptest` and an injected clock.

- Sanitized raw fixtures and decode tests for each create response in
  `testdata/network/` and `testdata/compute/`, including the integer `id`
  and `ruleId`, and a key create whose `privateKey` is `<secret>`.
- Request bodies: group create with and without description; group
  update of only `Name` resends the read description, and the reverse;
  rule create with `EtherType` derived from an IPv4 and an IPv6 prefix,
  and `PortRangeMax` 0 sent as `PortRangeMin`; key import and create.
- Shape refusals with no request: bare address, bad prefix, family
  mismatch, port 70000, min above max, `tcp` with port 0, empty update,
  multi-line public key, and a public key holding `PRIVATE KEY`, whose
  error does not contain the value.
- Delete guards: system group, group with a server, the server's
  `SecurityGroupInUse` message at 400 and 409, and a rule absent from the
  named group; each sends no `DELETE`.
- Group wait: `CREATING` to `ACTIVE`, `ERROR`, a 404 then `ACTIVE`, the
  bound, `NoWait`, poll spacing, and a cancelled context.
- Statuses 200, 201, 204, 400, 404, 409, and 5xx; no create retry after a
  502; a create response without an ID fails.
- Path ID rejection for `..`, `.`, `/`, `?`, and empty on every operation
  that takes an ID.
- Secret: `fmt` verbs, `slog`, and `json.Marshal` of `CreateSSHKeyOutput`
  give `[redacted]`; `--debug` output, stdout, and stderr never hold the
  fixture secret.
- CLI golden tests for every command; `--yes` on deletes and on
  world-open ingress for `0.0.0.0/0` and `::/0`, and none for
  `10.0.0.0/8`; read-only refusal with no request; the `--secret-file`
  cases of [vStorage](storage.md#testing).

## Live checks

Live runs follow [live data](../../instructions/live-data.md) and log only
statuses, counts, field names, types, and timings. Each write run needs
the owner's approval naming the account, region `hcm-3`, and resources.
The manager runs these before the code for each release merges.

Read-only, first:

1. `ListSecurityGroups` and `ListSecurityGroupRules` on the default group:
   which of `id` and `ruleId` are set and their types; `protocol` case;
   `portRangeMin` and `portRangeMax` on an `any` and an `icmp` rule; the
   prefix written for "anywhere"; `isSystem` and `system` on the default
   group.
2. Whether the `name` filter on security groups and SSH keys matches
   exactly or by substring.
3. `ListSSHKeys`, and a `GET` on one key when any exist: whether
   `privateKey` is ever present (log a boolean only) and the `status`
   values.

Writes on the test account, names `vngcloud-live-<8 hex>`:

4. Group: create without `zoneId` or `portal-user-id` on the IAM gateway;
   the create response shape; statuses and time to `ACTIVE`; the default
   rules made (count and direction). Create the same name again: status
   and message. Update with the same name and a new description; with
   `description` `""`; with `description` left out. Delete: status, and
   time until `GET` is 404. Repeat delete: status.
5. Rule, on the test group, with prefix `203.0.113.0/24`: create `tcp` 22;
   the response shape. Create it again: status and message. Create with
   `203.0.113.5/24`: stored as sent or masked. Create an `icmp` rule.
   Delete one rule at once after its create: busy or 204. Delete a rule
   through a second test group's ID: whether the server ignores the group
   in the path. Repeat delete: status.
6. SSH key: import a throwaway ED25519 public key made for the run;
   response shape. Import the same name again. Create a key: response
   shape, `privateKey` present (boolean) and its first line's key type
   only; `GET` after create: `privateKey` present (boolean). Delete both;
   repeat delete: status.
7. The next day's bill shows no vServer line.

Not checkable without a paid server: the status of a delete refused as
`SecurityGroupInUse`, and deleting a key a server uses. The SDK's pre-read
covers the first; the second stays on the server.

The live write test deletes leftover groups and keys whose names start with
`vngcloud-live-` and that have no servers, then runs steps 4 to 6. Its
`t.Cleanup`, registered as soon as each ID is known, deletes rules, then
groups, then keys, with its own context, and asserts none remain. If a
create fails, it lists by exact name and deletes a match. It never touches
the default group or a group without the prefix.
