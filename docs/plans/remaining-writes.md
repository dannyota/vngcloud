# Remaining writes

Continue the accepted designs in existing feature worktrees. Release one feature per tag. Do not merge network writes before their required live checks pass. Do not run paid or uncleanable writes without the owner's approval.

## Authorities

- [Network writes 2](../design/vserver-network-writes-2.md) owns DHCP options, private virtual IPs, and resource tags.
- The `monitor-log-alarm-writes` branch holds the accepted log alarm design in `docs/design/monitor-log-alarms.md`.
- [Paid vServer writes](../design/vserver-paid-writes.md) owns the remaining volume and server releases.
- [Load balancer writes](../design/lb-writes.md) owns the remaining load balancer releases.
- [Verification](../../instructions/verification.md) and [live data](../../instructions/live-data.md) govern checks and releases.

## Work order

| Branch | Remaining work | Release gate |
|-|-|-|
| `dhcp-options` | Run the corrected live test, record evidence, prepare release notes, merge, verify CI, tag | Confirm the probe's next-day bill and approve the live run |
| `virtual-ips` | Run live checks, record evidence, prepare release notes, merge, verify CI, tag | Confirm private virtual IP cost and approve the live run |
| `resource-tags` | Settle concurrency contract, collect missing live PUT-response fixture evidence, run the example and live checks, release | Owner decision on concurrent writers; cost evidence and approved live run |
| `monitor-log-alarm-writes` | Update from master, verify live response shapes, release | An ACTIVE log project and approval for the live run; the design records an exhausted monthly order quota |
| `paid-vserver` | Continue volume writes, server lifecycle, attachments, then resize in design order | Approved paid runs and spending caps; resolve owner decision 17 before its refusal probe |
| `paid-lb` | Continue create/delete, resize, pools/members, listeners, then policies in design order | Approved paid runs, credit, and next-day bills matching quotes |

Quote-only releases for paid vServer and load balancer work have already shipped. Preserve the remaining branch commits when integrating each later release.

## Open contract

Tag writes read and replace the whole user-tag list. A concurrent writer can add a tag after the read and lose that tag to the replacement without a confirmation mismatch. The accepted review invariant and wiki promise no such loss. Ask the owner whether to document a requirement to serialize writes per resource or hold tag writes until conditional updates are supported. Do not change that contract before the decision.

## Verification state

Local `make check` and live-write compile-only checks passed on all six feature branches in this session. DHCP, virtual IP, tagging, and log alarm review fixes are committed. Tagging still has the separate contract and live-evidence blockers above. Changes made after a branch's checks need fresh checks before commit. No live API checks ran during this continuation. No release is approved solely by local tests.
