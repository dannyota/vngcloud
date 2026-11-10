# vServer Floating IPs Design

Status: [Owner decisions](#owner-decisions) accepted; review gates open.

Public addresses are opt-in, off by default. Require explicit security
groups, a conservative monthly quote, `MaxPrice` defaulting to zero, and
`--yes` on disconnects/deletes. This design adds derived server addresses,
public create, and IP management, without purchase or cleanup authority.

[Paid writes](vserver-paid-writes.md),
[ADR 0002](../adr/0002-write-api-conventions.md), and
[ADR 0003](../adr/0003-toggle-writes.md) supply the base conventions.
The [API](vserver-floating-ips-api.md) owns wire evidence and gaps; the
[checks](vserver-floating-ips-checks.md) own tests and live procedures.
All new behavior below is unimplemented and subject to independent review.

## Terms

Use "floating IP" for new operations; retain `network.WANIP`/`ListWANIPs`.
`PublicIP` covers internal-interface NAT, excluding External Interfaces.
[Mapping](vserver-floating-ips-api.md#terms-and-records) rests on published
schema/first-party code, not populated live evidence.

## Non-goals

External Interfaces, `GET elastic-ips`, second-interface operations,
standalone allocation, reservation changes, tags, load balancer and NAT
addresses, and writes to OpenTofu-managed resources are outside scope.
Auto-allocation after create has a separate [release gate](#releases).

## Cost

Use live quotes, never hard-coded prices. The
[credit-history decision](vserver-floating-ips-api.md#credit-history-decision-procedure)
recorded inclusion: a create with `attachFloating` debits only the server
quote. Create therefore quotes and guards the server only and shows a zero
`FLOATING IP` line ([guard](#quote-and-price-guard)). The server-plus-IP sum
below stays the conservative path for configurations outside those terms,
such as later auto-allocation or reservation changes.

The 347,800 + 120,000 = 467,800 VND sum was the pre-L4 reference; L4 ran
in `han-1` under an owner-raised 500,000 VND cap.
`MaxPrice` guards the quoted monthly estimate, not the final bill: the API
accepts no price cap or quote token. Prices can change before send. Detach
is not proof billing stopped; no guaranteed refund or spending ceiling.

## R1: public address on server reads

Add `PublicIP string` to `compute.Server`, tagged `json:"-"`. Derive it
once during decoding, ignoring any incoming `publicIp` key:

1. Inspect only `InternalInterfaces[].FloatingIP`, in response order.
2. Skip empty values. Each non-empty value must parse with `net/netip`,
   have no zone identifier, and be global unicast and non-private.
   Reject loopback, link-local, multicast, unspecified, whitespace, and
   control characters. Unmap IPv4-mapped IPv6 before these checks.
3. Set the canonical address only if all non-empty values are valid and
   identify one distinct address. Otherwise leave `PublicIP` empty.
   Keep interface fields unchanged; an empty derived field is not proof
   that no IP is allocated or attached.

Apply this to `GetServer`, `ListServers`, `ListServersBySecurityGroup`,
and every write response or wait that decodes a `Server`. Clear a stale
value on repeated unmarshal. External interface fields never supply it.
`192.0.2.0/24` addresses remain valid synthetic fixture values.

The CLI renders `Server.PublicIP` and `Items[].PublicIP` using Go names;
`--query Server.PublicIP --output text` selects the address. Standard JSON
marshaling omits it; no new read command is needed. Keyed literals remain
compatible; strict CLI JSON consumers and unkeyed literals may need updates.
Do not claim universal source or byte compatibility.

R1 needs no paid resource; schema/synthetic cases claim no populated live
address. Bound reused reads under [limits](#identifiers-and-retries).

## R2: floating IP on create

### Input and body

Add default-false `AttachFloatingIP bool` and `AllowOpenLogin bool` to
`compute.CreateServerInput`. Send `attachFloating` explicitly, never the
override. Reject override without opt-in before requests. Keep create
validation, key/secret user data choice, explicit groups, duplicate checks,
and auto-renew default.

`QuoteCreateServer` takes `*CreateServerInput`, requiring only priced fields.
Validate supplied IDs/optional shapes; name/key/VPC/subnet/groups are optional.
Ignore `MaxPrice`, `NoWait`, and exposure override; do no exposure reads.
Actual create applies [login exposure](#login-exposure) in the SDK for all
callers, including those outside the CLI.

### Quote and price guard

With `AttachFloatingIP:false`, retain the single request and quote values.
With true, quote server then `elastic-ip` in the same zone using the
[observed body](vserver-floating-ips-api.md#elastic-ip-quote).
One private helper shared by `compute` and `network` builds this quote;
never import `network` into `compute` or add an SDK dependency.

Keep `pricing.GetQuoteOutput`. Sum `OptimumPrice`, `OriginalPrice`, and
`DiscountPrice`. Compute `DiscountPercent` as
`100 * (OriginalPrice - OptimumPrice) / OriginalPrice`, or zero when the
summed original is zero. Validate all values and sums as finite and
non-negative; overflow or inconsistent original/discount totals is an
`*vngcloud.APIError` with code `InvalidResponse`, not an order.
Missing or null `optimumPrice` is also invalid; non-positive component
prices return `vngcloud.ErrUnpriced`. An error gives no partial quote.

Preserve server properties then IP properties and their numeric values.
Prefix each IP property's name with `FLOATING IP: ` and set its description
to `Conservative Elastic IP estimate; extra create charge unconfirmed`.
With no IP properties, append one `FLOATING IP` property with that same
description, optimum/monthly equal to the IP optimum, its discount percent,
and nil current price. Never append a second subtotal to populated lines.

Create compares the sum to `MaxPrice` and sends one `Once` order without
redirects. `MonthlyPrice` is the guarded estimate, not a confirmed debit;
its type stays unchanged. No user data reaches quotes. Shape validation
precedes requests; exposure checks precede quotes and refresh before send.

The manager accepted inclusion, so create with `AttachFloatingIP` sends no
IP quote and removes its contribution from totals, `MaxPrice`, and
`MonthlyPrice`. Keep a zero `FLOATING IP` line, nil current price, and
`Included in server order for the verified configuration` description.
This is an SDK annotation; preserve `ErrUnpriced`. The follow-up defines
proven terms; others retain the conservative path. Separate charging keeps
the sum with a confirmed-charge description; ambiguity blocks tagging.
Update tests/help/wiki/generated docs together. No runtime ledger lookup or
auto-switch; reservation/retention stays gated.

### Wait

Keep the 15 min create wait. After `ACTIVE`, allow 2 min for a valid internal
address and path-safe IP ID. Poll every 5 s; child deadlines cover requests
and sleeps, bounded by caller context. `ERROR` returns `compute.ErrFailed`.
Failed confirmation/bound returns safe Output and `compute.ErrNotSettled`,
preserving cause and advising `get-server`/`list-servers`, never auto-recreate.
`NoWait` returns UUID/name and guarded price; empty address means unobserved,
not allocation failure.

### CLI

Add `--attach-floating-ip` and `--allow-open-login` to create; expose the
priced opt-in on quote. The quote does not need the override flag.
Help explains the conservative monthly estimate, uncertain IP charge, ports,
read-before-write limits, and cleanup through the console until R3.
Public create needs no `--yes`; finite `--max-price` is consent.

## R3: floating IP management

Keep methods in `network`, already importing `compute`
(`network/types.go:4`), reusing configuration and context. Shared
quote/rule internals import neither service; CLI uses only public SDK.

For each new method use `func (c *Client) Name(ctx context.Context,
in *NameInput) (*NameOutput, error)`. Required fields carry
`vngcloud:"required"`; nil inputs fail `ErrInvalidInput`.

- `GetFloatingIPInput`: required `FloatingIPID string`.
  `GetFloatingIPOutput`: `WANIP WANIP`.
- `AttachFloatingIPInput`: required `ServerID string` and `FloatingIPID
  string`; `AllowOpenLogin bool`, `NoWait bool`. Empty-ID allocation
  and its `MaxPrice float64` field stay gated on the extension.
- `AttachFloatingIPOutput`: `Server *compute.Server`, `FloatingIPID
  string`, `PublicIP string`, `MonthlyPrice *float64`, `Changed bool`.
  Existing-IP attachment leaves price nil, meaning unquoted ongoing
  cost, not free. An allocation extension would fill its quoted price.
- `DetachFloatingIPInput`: required `ServerID string`, plus `Delete bool`
  and `NoWait bool`. Reject `Delete && NoWait` before any request.
- `DetachFloatingIPOutput`: `Server *compute.Server`, `FloatingIPID
  string`, `Detached bool`, `Deleted bool`, `Changed bool`, and
  `BillingContinues *bool`. Nil means unknown; true means the retained
  record may keep charging under the billing rules; false requires
  confirmed absence. Never present an unknown result as free.
- `DeleteFloatingIPInput`: required `FloatingIPID string`, `NoWait bool`.
  `DeleteFloatingIPOutput`: `FloatingIPID string`, `Deleted bool`, and
  `BillingContinues *bool`, with the same meanings.

`Changed` means acceptance or confirmed mutation; false with an error does
not prove no change. Partial outputs keep known IDs and safe observations,
never invented settlement. No-op returns `Changed` false and nil error.

### List and get

Keep `ListWANIPsInput`, its paging defaults, and its Output alias.
Add `IP string`, `FixedIP string`, and `Zone network.Zone` to `WANIP`
only with the schema and raw-row checks. Preserve every existing field;
absence in one response never authorizes removing a public field.
Do not silently remap `IPAddress` or `FloatingIP` to another wire key.

`GetFloatingIP` filters `ListWANIPs` by exact `UUID`. Do not invent a
detail endpoint or rely on the unverified `ids` filter. Walk pages of
1000, at most 50 pages and 30 s per walk, checking cancellation between
pages. Require page/size consistency, stable non-negative totals,
unique non-empty UUIDs, progress, and a final count equal to `totalItem`.
Only a complete walk can prove absence; malformed, missing, changing, or
excessive metadata returns `APIError{Code: "InvalidResponse"}`. Define
and verify the empty-page encoding in the free read before shipping.
A match can return early after validating that page; no match is
`vngcloud.ErrNotFound`. Walks provide no atomic inventory snapshot.

### Attach

Validate all caller IDs before reads. The allocation extension would
also validate finite non-negative `MaxPrice`. Read server, then named IP.
An identical attachment on both records is a no-op. A different attached
ID is `ErrInUse`; a floating ID without an address, an address without
an ID, or disagreement between records is `ErrUnexpectedStatus`, not
permission to allocate.

Require `ACTIVE` or `STOPPED`, no External Interfaces, and exactly one
internal interface with a path-safe UUID and VPC/subnet references.
Multiple interfaces remain gated until primary-interface selection is
verified. Require known equal server/IP zone IDs and an unattached IP;
missing identity or attachment data fails closed. The live raw row must
establish an explicit unattached encoding, not merely an omitted field.
Run the exposure check, re-read the attachment state before send, and
refuse a changed snapshot rather than silently retargeting it.

Send `PUT servers/{serverId}/wan-ips/{floatingIpId}/attach` with that
interface's ID. No new allocation or quote occurs for an existing ID;
this path cannot ship until the no-new-charge evidence gate passes.
Confirm that the exact IP ID appears on the same interface with a valid
address and the WAN row names that interface. Any other address is not
success. API-side conflict checks remain necessary after the pre-read.

### Detach and delete

Detach finds the unique interface holding both IP fields. All absent is a
no-op; incomplete/conflicting fields refuse. Bind work to that ID/interface,
not row order or `PublicIP`. After 204, confirm the ID absent from the
server and WAN explicitly unattached or gone. Never delete a replacement.

Default detach keeps the IP, names it in Output, and reports ongoing
billing. With `Delete`, proceed only after detach returns nil and confirms.
The [mutation outcomes](vserver-floating-ips-api.md#mutation-outcomes)
define nested failures and cleanup flags, including confirmed deletion
with an uncertain acknowledgement. Repeating detach cannot recover the ID.

Delete first reads the named record and refuses attached or ambiguous
state with `ErrInUse` or `ErrUnexpectedStatus`. Send `DELETE wanIps/{id}`
without a body, then wait for a complete list proving absence. A
pre-read 404/absence remains `ErrNotFound`; absence after an accepted or
uncertain delete confirms absence, not a successful acknowledgement.
No implicit delete, rollback, adoption, or cleanup runs on create/attach
failure.

### Waits

Attach and detach poll every 2 s for at most 2 min; delete polls every
2 s for at most 1 min. The whole confirmation phase, including each
list walk and transport retry, shares that deadline. Caller cancellation
wins. Confirm only the exact captured identity and expected state.
All new mutations send once with `Once` and `NoRedirect`, including
delete to avoid replaying a stale pre-read. The
[mutation outcome rules](vserver-floating-ips-api.md#mutation-outcomes)
define confirmation, `NoWait`, errors, and partial Output for attach,
detach, and delete. An uncertain send retains `network.ErrNotSettled`
even when reads confirm the target; observed cleanup flags still update.
Do not resend. Confirmation never authorizes another paid allocation.

### Identifiers and retries

Validate every caller ID before any request and every returned ID before
reuse with `core.CheckPathID`; reject the reserved caller IP ID `auto`.
Apply `MaxBody` before capture/decode: 8 MiB for server/WAN/group/mutation
responses; retain each quote's enforced 1 MiB
[bound](vserver-paid-writes-api.md#quote-decoding-and-bounds).

Reads/quotes keep the finite configured retry count: default three
retries per send phase and at most one 401 recovery, all inside the
operation deadline. Introduce no outer retry. Preflight and each quote
have a 30 s deadline; waits use injected clocks. The
[security obligations](vserver-floating-ips-checks.md#security-review)
also require credential-reflection checks on reused response paths.

## Login exposure

[Published behavior](vserver-floating-ips-api.md#published-behavior)
does not prove that key-only API creation disables password login.
Help and wiki recommend narrow source prefixes and disabling password
login through user data or guest configuration. Never parse or log secret
user data to claim the guest is safe. Do not probe guest login in L4.

The accepted guard uses a shared internal rule reader and evaluator for
`compute` create and `network` attach, using the existing
security-group endpoints. `compute.ErrOpenLogin` and
`network.ErrOpenLogin` alias one private sentinel. No CLI-only bypass.

1. For public create use the explicitly named groups; for attach use
   all groups from the fresh server read. Deduplicate IDs; require 1 to
   64 groups, at most 4096 rules in total, an 8 MiB body per read, and
   30 s for the whole guard. Missing/null rule data, permission errors,
   incomplete schema, and limit breaches refuse the write.
2. Evaluate the union of ingress grants on TCP ports 22, 234, 3389,
   and 3490, separately for IPv4 and IPv6. Parse and mask CIDRs, merging
   bounded address intervals so two `/1` rules cannot bypass a `/0`
   check. Check all groups together. Ignore only known egress rules.
3. Observed protocols are `any` and `TCP` in either case; `any` covers
   TCP only with ports 0 to 65535. TCP integer ranges cover their stated
   ports. The guard reads `remoteIpPrefix`; `remoteGroupId` is set on
   every observed rule, so it never narrows exposure. Other protocols,
   direction, or ether type, null or absent ports, and null, empty, or
   invalid prefixes return `ErrOpenLogin`. Do not ignore inactive-looking
   rules without evidence that their status prevents application.
4. Full address-space coverage for any listed port/family returns
   `ErrOpenLogin`. Narrow prefixes pass this particular check, but are
   not a promise of secure access. Arbitrary guest ports and reachable
   members of remote groups remain outside the check's knowledge.
5. Run before quoting, then repeat immediately before send. On attach,
   re-read group membership and interface identity too. Any changed rule
   or membership snapshot refuses rather than restarts. `AllowOpenLogin`
   bypasses exposure evaluation only, never malformed identity, response
   bounds, permission errors, price checks, or attachment guards.

No API condition binds a write to the rule snapshot. Another actor can
open a group after the last read or after attachment. Rechecks reduce
stale reads; they cannot eliminate that race. This limit is accepted;
use dedicated restricted groups and serialize changes. The SDK never
edits groups or the guest as a side effect.

## CLI

R3 adds `network get-floating-ip`, `attach-floating-ip`,
`detach-floating-ip [--delete]`, and `delete-floating-ip`; keep
`list-wan-ips`. Map Input fields to kebab-case flags. Existing-IP attach
needs explicit IDs and no `--yes`; detach and delete always need `--yes`.
No command prompts or retries. Read-only profiles refuse every write
with exit 2 before requests; reads and quotes remain available.

Use the shared [error mappings](cli.md#errors-and-exit-codes) and
`internal/cli/errors.go` precedence without command-specific exceptions.
Success exits 0; `NotSettled` exits 1 before wrapped auth, not-found, or
context causes; cancellation exits 1 before authentication failures,
which exit 3; remaining not-found exits 4, usage/config/consent exits 2,
and other failures exit 1. A direct mutation 401 exits 3 without retry.
Reuse `ResourceInUse` for `network.ErrInUse` and all existing codes; add
only `OpenLogin` (exit 1) for the new exposure sentinel. Preserve
`errors.Is`/`errors.As` through nested failures and shared classification.
Print safe partial Output on stdout and errors/warnings on stderr.
Retained/unknown IP deletion warns of charges even under `--query`;
confirmed deletion with `NotSettled` reports cleanup complete and no
repeat delete, not an ongoing-charge warning. Never print raw responses.

Each implementation release updates its SDK wiki and generated CLI docs
with `make gen-docs`: `CLI-Compute.md`, `CLI-Network.md`,
`Compute-Servers.md`, `Network.md`, `Services.md`, and
`Billing-and-Pricing.md` as affected. The design pass does not edit them.

## Releases

R1 ships free bounded reads with schema/synthetic checks. R2 needs R1,
quote fixtures, independent fix confirmation, guard, and create/billing/cleanup
evidence from [L4](vserver-floating-ips-checks.md#live-run), then the
price correction above if inclusion is confirmed, before its tag.
R2 can ship before R3 only with a verified, manager-approved console cleanup
route and wiki instructions; otherwise it stays gated, not quietly
coupled to unshipped SDK management methods.

R3 needs R2 and management L4: existing-IP attach/detach/delete/get and
populated WAN models. Each release needs unit checks, independent review,
and authorized evidence; standalone R3 needs new purchase approval.

Defer empty-ID auto-allocation and `QuoteAttachFloatingIP` to a gated
extension. Its intended quote takes `*AttachFloatingIPInput`, needs
`ServerID` to read the zone, rejects an existing IP ID, and returns
`pricing.GetQuoteOutput`; attach would send `auto/attach` once after a
positive IP quote within `MaxPrice`. Neither method/branch ships until
its quote, charge, orphan behavior, and separate live run are established.

## Owner decisions

Decisions accepted by the manager under delegated owner authority:

1. Use derived `Server.PublicIP` for list and query use; External
   Interface addresses stay outside it to keep the mapping evidence-based.
2. Use the shared SDK exposure guard with `AllowOpenLogin`; its snapshot
   and guest-port limits are accepted. It protects SDK and CLI callers.
3. Keep the IP on detach with explicit billing Output to preserve the
   address; `--delete` performs detach-then-delete.
4. Use floating IP names in `network`, keeping existing WAN APIs
   unchanged to preserve callers and service ownership.
5. Keep the existing quote Output type, checked sums, and labeled fallback
   properties. The accepted price interpretation is in [Cost](#cost).
6. Use one-IP L4 with a 500,000 VND gross quote cap; defer auto-allocation
   to avoid another purchase. Evidence gates and manager run-time approval
   naming account, region, cleanup method, and mode still apply.

The manager accepts [Cost](#cost) and its evidence-dependent
[pre-tag correction](#quote-and-price-guard) under delegated owner authority.
Independent fix confirmation, evidence, and run-time approval remain gates.

## Evidence gates

[Missing evidence](vserver-floating-ips-api.md#missing-evidence) blocks each
dependent operation. Schemas/code/tests/approval cannot replace raw billing
or IAM evidence. Keep standard-library-only SDK and public import paths.
