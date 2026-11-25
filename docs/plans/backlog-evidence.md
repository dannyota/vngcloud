# Backlog evidence plan

**Goal:** Close the evidence gates in [TODO.md](../../TODO.md) that the default IAM profile can qualify, first by reads and public sources, then by owner-approved create-then-delete runs paid from existing credits. Funding, payment methods, CDN and storage paid runs, manual renewal, and root-checkout NAT are out of scope.

**Rules:** [Live data](../../instructions/live-data.md), [live verification](../../instructions/testing.md#live-verification), and [workflow](../../instructions/workflow.md). Only the manager runs live calls. Workers prepare tools in the private scratchpad and never see credentials or raw responses. Balances, ceilings, IDs, and support records stay in the manager's private run ledger, never in this repository.

## Budget and stop conditions

- The private ledger sets a ceiling for each run, a ceiling for cumulative money not yet returned, and a reserve that stays available for the owner's running resources. A run that would breach any of them does not start. No top-up is possible.
- Run one paid resource at a time. Before the next run, reconcile available and held cash, the billing transactions, and the billing record of the previous resource. Money not yet returned counts as lost against the cumulative ceiling.
- Every create uses a fresh quote where a quote exists, with `--max-price` equal to that quote. The cap does not lock the charged price, so the ledger ceiling is checked against the quote first.
- A run works only in a VPC it created itself, named `vngcloud-live-<8 hex>`. Before the first write it records its generated names; it never borrows a VPC, route, or resource that existed before.
- A create is sent once. When its outcome is unclear, the run reads inventory and adopts only a resource with the exact generated name; it never sends the create again. Cleanup deletes children before parents, by ledger ID only.
- Stop the whole plan when a delete fails, a resource is left behind, renewal state is unknown, a balance drops without explanation, or a billing record stays active after its resource is gone. Hand the leftover to the owner before another paid run.

## Tools

- [x] Shape probe: a scratchpad Go program, compiled with a Go overlay, that sends allowlisted GETs to SDK-known first-party endpoints with the default IAM profile and prints only HTTP status and a key and kind tree. Identifier-like keys are hidden. It can emit a fixture with synthetic scalars. It follows no redirect and prints no IDs, values, or error text. It runs live only after independent review.
- [x] Run scripts: one scratchpad script per paid run, reviewed independently before any run, with a read-only plan mode, a ledger, and a cleanup mode.

## No-cost work first

- [ ] vMonitor per-location results (dashboard list and get shipped): find the routes in the console's public JavaScript, verify them with the shape probe on existing resources, then design, review, implement, and release each as its own slice.
- [ ] vMonitor quota upgrade quote: verify only a request proven not to place an order. Channel test send and dashboard delete are writes; they stay in discovery until a test destination or a test dashboard the owner controls exists.
- [x] NAT detail and rule routes and the endpoint billing record: record the console's routes before any purchase, with a pass criterion for each read.
- [ ] Held reads: check each gate separately by read. One Backup Center policy proves no backup server or restore point. An empty snapshot history proves no activation state. vDB engines beyond MySQL 8.0 need a populated row per engine. None of these justifies an activation or purchase.
- [ ] Release binaries: design reproducible builds, checksums, and provenance for review as a release-workflow change.
- [x] Root disk zone: no paid experiment. One tested pair would not justify a guard, and a safe harness costs more than the answer is worth. `CreateServer` instead adds a fixed hint to a 400 refusal, per [the design](../design/vserver-root-disk-zone.md#refusal-hint).

## Paid runs, in order

- [x] NAT (`han-1`): only after the detail and rule routes are known. In a VPC the run created, quote and create one NAT, confirm renewal is off, probe the routes while it is ACTIVE, delete it, confirm the default route and the NAT are gone, then delete the VPC and reconcile. An empty rule list verifies the envelope, not a rule row; the run creates no rule.

## Held

- Endpoint billing: a delete has already left an active prepaid record that needed a support refund. Another create would repeat that evidence without testing the toggle. It stays held until an endpoint-specific contract is reviewed and the owner accepts the refund risk for that run.
- VKS: there is no quote, and the recorded one-node create debited more than the ledger allows. It stays held.
- Snapshot service activation, backup runs, and vDB creates for other engines stay held.
