# Public NAT HCM live check

The single paid run that live-verifies Public NAT create and delete in
`hcm-3` under the [NAT purchase amendment](network-writes-nat.md); the SDK
already enables them per account. The
[API evidence](network-writes-api.md) holds the shared NAT envelopes.

## Preflight result

Two preflights on the approved account stopped before any order. The
catalog enables HCM03-1B and HCM03-1C, each offering Standard at 712,400
VND, while HCM03-1A is the default AZ and disabled. The NAT VPC picker
lists only HCM03-1A for both an existing VPC and the run's disposable VPC,
so the SDK guard refused 1B and 1C with "VPC does not list the
availability zone". The disposable VPC was removed and nothing was bought.
That guard was wrong; see the console evidence below.

## Console evidence

The HCM console lists VPCs with the picker filter `zoneType=AVAILABILITY`,
not by AZ, and every VPC, including ones whose subnets live in HCM03-1C,
reports `zones: [HCM03-1A]`. It reads subnets per AZ with
`vpcs/{vpcId}/subnets` and `zoneUuid`, but enabled Create for HCM03-1B on
a VPC whose only subnet was in HCM03-1C (no 1B order was sent). A console order in HCM03-1C on a disposable
VPC returned 201 with `resourceInfo` holding `isPoc`, `isEnableAutoRenew`
(console default true), `isBuyMorePoc`, `natName`, `packageUuid`,
`vpcUuid`, `regionUuid`, and `projectUuid`, plus `tagDetails`; no AZ or
subnet. The NAT reached ACTIVE in HCM03-1C, and the SDK's
`delete-nat-instance` removed it. The disposable VPC and subnet were then
deleted. HAN orders succeeded earlier on a VPC with no subnet.

## Run

Run the reviewed HCM-enabled build on the approved IAM-user account and
project. Set `VNGCLOUD_PROJECT_ID` to that project's ID. Serialize
VPC-creating runs and pause unrelated spending. After taking the run's
exclusive slot and before `CreateVPC`, the harness reads the VPC quota
entry with its own `hcm-3` configuration and stops unless the approved
project is in scope and at least one VPC slot is free. Supply `VNGCLOUD_ROOT_EMAIL`, `VNGCLOUD_USERNAME`,
`VNGCLOUD_PASSWORD`, and optionally `VNGCLOUD_TOTP_SECRET` through the
private IAM environment or `.env`. Prepare the read-only probe before the
run. Use a fresh, absolute `0700` hold directory owned by the runner
(`$nat_hold_dir`).

```sh
VNGCLOUD_LIVE_WRITE=1 VNGCLOUD_LIVE_NAT_WRITE=1 \
VNGCLOUD_LIVE_NAT_REGION=hcm-3 VNGCLOUD_LIVE_NAT_AZ=HCM03-1C \
VNGCLOUD_LIVE_NAT_MAX_PRICE=720000 \
VNGCLOUD_LIVE_NAT_HOLD_DIR="$nat_hold_dir" VNGCLOUD_LIVE_NAT_HOLD_MAX=15m \
go test -v -tags livewrite -count=1 -timeout=120m \
  -run '^TestLiveWriteNAT$' ./livetest/
```

Add `VNGCLOUD_LIVE_NAT_REGION` as a single-region selector for this test.
Allow `hcm-3` and `han-1`, default to `han-1`, and keep the same IAM-user
configuration throughout the run. In `hcm-3` the harness refuses to start
without an explicit `VNGCLOUD_LIVE_NAT_MAX_PRICE` at or below 720,000 VND
and without a valid hold directory; it never uses the default cap. It
passes `MaxPrice` equal to the smallest of the approved cap, the runner's
cap, and the verified available credit. Before the single NAT POST, require all
of the following.

1. A unique region UUID from `/regions` matching the pinned HCM gateway
   and an exact project scope. Take complete inventory baselines and stop
   on leftovers. Create one uniquely named disposable VPC with a free CIDR,
   prove its ownership in scope, and confirm an empty route baseline.
2. Explicit V3 `data.enabledForAll: true`, the enabled `AVAILABILITY` AZ
   named by `VNGCLOUD_LIVE_NAT_AZ` (the catalog offers more than one), the
   VPC exactly once in the picker's project and region, and one matching
   VND offer UUID
   and SKU in the AZ-filtered catalog. Check for the HAN test's Standard
   offer (`nat.s-standard`) in HCM. Keep the selected AZ and offer in the
   private report for comparison. If the offer or placement is ambiguous
   or unavailable, stop and resolve selection before any purchase. A first
   preflight stopped before ordering because HCM03-1B and HCM03-1C both
   offered Standard at 712,400 VND; `VNGCLOUD_LIVE_NAT_AZ` then names one
   candidate AZ, and any other value stops the run.
3. A valid positive quote at or below 720,000 VND and enough credit. Create
   rechecks `MaxPrice` against its immediate quote. If the HCM quote exceeds
   the cap or HCM needs a different offer, obtain owner approval before
   any purchase.

Pass requires a validated single direct order, debit matching its fresh
quote, a uniquely matched `ACTIVE` NAT, one default route in the disposable
VPC, and one matching PREPAID billing row with renewal `MANUAL` and explicit
null `renewPeriod`. Only then wait at `hold: waiting`. Use the private
`nat-ids.json` with the detail and rules entries of the
[read-only probe](network-nat-reads.md#pinned-read-only-probes), pinned to
HCM. Require `GetNATInstance` and `ListNATInboundRules` to satisfy the
[read contract](network-nat-reads.md#accepted-surface). Both need HTTP 200
and success. Detail must match the ACTIVE NAT, VPC, and package. Rules must
form one complete array with valid rows and unique IDs. An empty array
qualifies only the empty form. Record allowlisted field kinds and null
versus absent. Signal the harness with a regular `release` file immediately
after probing, even on failure. The probe writes a separate pass or fail
result for each read before `release`; a missing result or a failed read
fails the run even when cleanup succeeds. Hold timeout
or probe failure fails the run and cleanup continues under its own
deadline.

Require exactly one guarded NAT DELETE, counted by the harness, whose own
response it checks for HTTP 200, `code: 0`, and `success: true`; NAT
absence alone does not prove the delete contract. Then require confirmed
NAT and billing absence and restoration of the empty route baseline. On
every cleanup path after the order, delete the VPC only when all three are
confirmed; otherwise leave it and record it as a leftover in the private
report. Finish with VPC deletion and reconciliation of debit, refund, and net cost against
private transactions. Failed checks or unresolved leftovers leave HCM
writes unverified and block further paid runs until resolved. Add sanitized HCM discovery, order and delete envelopes, lifecycle
and cost results here, and record observed detail and inbound-rule shapes
in the read design. Keep resource IDs, balances, transactions, and raw
responses private.

## Run results

Three runs of the SDK harness with an explicit `VNGCLOUD_LIVE_NAT_AZ` of
HCM03-1C passed every pre-order guard, quoted Standard at 712,400 VND, and
created one NAT each. Each reached ACTIVE in 5 to 6 minutes with renewal
MANUAL. The harness deleted it, saw NAT and billing absent, and removed the
disposable VPC; refunds left a net cost of 0 to 16 VND per run.
`GetNATInstance` passed on the held NAT. `ListNATInboundRules` returned
`InvalidResponse` because HCM rule IDs contain `_`; the read contract now
accepts them (see the [read evidence](network-nat-reads.md#observed-live-evidence)).
A full run with both probe reads passing is still to be recorded.
