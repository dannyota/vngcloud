# vServer Floating IPs Design

Status: [Owner decisions](#owner-decisions) accepted; evidence gates open.

Public addresses are an explicit opt-in, off by default. Keep explicit
security groups, a quote covering the whole monthly price, `MaxPrice`
defaulting to zero, and `--yes` on disconnects and deletes. This design
adds a derived server address, public server creation, and floating IP
management. It grants no live-run, purchase, or cleanup authority.

[Paid writes](vserver-paid-writes.md),
[ADR 0002](../adr/0002-write-api-conventions.md), and
[ADR 0003](../adr/0003-toggle-writes.md) supply the base conventions.
The [API](vserver-floating-ips-api.md) owns wire evidence and gaps; the
[checks](vserver-floating-ips-checks.md) own tests and live procedures.
All new behavior below is unimplemented and subject to independent review.

## Terms

Use "floating IP" for new operations; retain `network.WANIP`/`ListWANIPs`.
The address is NAT on an internal interface. External Interfaces are a
separate product. `PublicIP` covers only the floating address, not every
possible public network configuration.
The [record mapping](vserver-floating-ips-api.md#terms-and-records) is
published or supported by first-party code, not populated live evidence.

## Non-goals

External Interfaces, `GET elastic-ips`, second-interface operations,
standalone allocation, reservation changes, tags, load balancer and NAT
addresses, and writes to OpenTofu-managed resources are outside scope.
Auto-allocation after create has a separate [release gate](#releases).

## Cost

Use live quotes, never a hard-coded IP price. The recorded example is
347,800 VND for a small server plus 120,000 for an IP, totaling 467,800.
The server quote alone ignores `attachFloating`; see
[prices](vserver-paid-writes-api.md#prices). Detach does not establish
that billing stopped. The exact IP charge, reservation effects, and
refund remain [evidence gates](vserver-floating-ips-api.md#billing).

`MaxPrice` guards the quoted monthly total, not the final bill: the API
accepts neither a price cap nor a quote token on the order. A price can
change between quote and send. Disclose this limit; never claim the SDK
can enforce a server-side spending ceiling or guaranteed refund.

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

The CLI renders `Server.PublicIP` and `Items[].PublicIP` using Go field
names; `--query Server.PublicIP --output text` selects the bare address.
Standard `encoding/json` marshaling omits it. No new read command is
needed. This is additive for keyed struct literals and CLI output;
strict JSON consumers and unkeyed external struct literals may need
updates. Do not claim universal source or byte compatibility.

R1 needs no paid resource. Published schema and clearly labeled synthetic
positive cases support it; no fixture claims a populated live address.
Bound the reused server reads under [limits](#identifiers-and-retries).

## R2: floating IP on create

### Input and body

Add `AttachFloatingIP bool` and `AllowOpenLogin bool` to
`compute.CreateServerInput`; both default false. Send `attachFloating`
explicitly with its bool value. Never send `AllowOpenLogin` to the API.
Reject `AllowOpenLogin` without `AttachFloatingIP` before requests.
All existing create validation, key or secret user data choice, explicit
security groups, duplicate checks, and auto-renew default still apply.

`QuoteCreateServer` still takes `*CreateServerInput` and requires only
priced fields. Validate every supplied ID and optional field shape; do
not require a name, key, VPC, subnet, or security group for a quote.
Quotes ignore `MaxPrice`, `NoWait`, and the exposure override and perform
no exposure reads. Actual create applies [login exposure](#login-exposure)
in the SDK, including for callers who do not use the CLI.

### Quote and price guard

When `AttachFloatingIP` is false, retain the current single request and
`pricing.GetQuoteOutput` values. When true, send the server quote then
an `elastic-ip` quote using the same selected zone. The exact IP builder
is gated on the [probe](vserver-floating-ips-api.md#elastic-ip-quote).
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

Preserve server properties followed by IP properties. If IP properties
are absent, append `pricing.PriceProperty{Name: "FLOATING IP"}` with
`OptimumPrice` and `MonthlyPrice` equal to its quoted optimum,
`DiscountPercent` equal to its quote, `CurrentPrice` nil, and a fixed
description identifying a derived monthly subtotal. Never invent fields
that `PriceProperty` does not have or double-count a subtotal as a charge.
The probe must establish compatible monthly units and discount meanings.

Create compares only the sum to `MaxPrice`, then sends one order with
`Once` and no redirects. `CreateServerOutput.MonthlyPrice` is that sum;
the type stays unchanged. User data never enters either quote. Shape
validation precedes every request; exposure checks run before quoting
and are refreshed immediately before the order as specified below.

### Wait

Keep the existing 15 min server-create wait. After `ACTIVE`, allow up to
2 min more for one valid internal address and its path-safe floating ID.
Poll every 5 s with a child deadline covering requests and sleeps, always
bounded by the caller's context. `ERROR` returns `compute.ErrFailed`.
A bound or failed confirmation returns the last safe Output and
`compute.ErrNotSettled`; preserve the cause and say to inspect
`get-server` and `list-servers`, never repeat a create automatically.
`NoWait` returns the existing UUID/name fallback and summed price;
empty address fields then mean unobserved, not allocation failure.

### CLI

Add `--attach-floating-ip` and `--allow-open-login` to create; expose the
priced opt-in on quote. The quote does not need the override flag.
Help explains the summed monthly quote, extra charge, exposed ports,
read-before-write limits, and cleanup through the console until R3.
A public create needs no `--yes`; its finite `--max-price` is consent.

## R3: floating IP management

Keep public service methods in `network`, which already imports
`compute` (`network/types.go:4`). Construct its compute client from the
same configuration, preserving authentication, endpoint, and context.
Shared quote and rule-reading internals must not import either service.
The CLI calls only public SDK operations.

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

`Changed` means this invocation received acceptance or confirmed its
requested mutation. False alongside an error does not prove no change.
Partial outputs retain known IDs and safe observations, never fabricate
settlement. A no-op has `Changed` false and a nil error.

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

Detach locates the unique internal interface holding both IP fields.
No fields on any interface is a no-op; incomplete or conflicting fields
refuse. Bind all subsequent work to that IP ID and interface, not the
first row or just `PublicIP`. After the 204, confirm the captured ID is
absent from the server and the WAN row is explicitly unattached or gone.
A different replacement IP does not become this call's deletion target.

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
All new/reused read, quote, and mutation response paths need an 8 MiB
`MaxBody`, applied before capture or decode. `GetServer` and `ListWANIPs`
currently use zero (unlimited): this is a named implementation obligation,
along with server lists, group reads, and quote paths used here.

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
   check. Check all groups together. Ignore only known egress rules
   and known UDP/ICMP protocols for this TCP test.
3. Empty/any protocol covers TCP. Unknown protocol, direction, ether
   type, invalid/missing ports, or remote-group grants cannot establish
   safety: return `ErrOpenLogin`. For TCP, zero/absent ranges cover all
   ports; valid integer inclusive ranges cover their stated ports.
   Missing/invalid CIDRs also fail closed. Do not ignore inactive-looking
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

R1 ships the free derived field and bounded reads with schema/synthetic
checks. R2 depends on R1, the exact quote probe, the accepted guard, and
the create portion of [L4](vserver-floating-ips-checks.md#live-run).
R2 can ship before R3 only with a verified, manager-approved console cleanup
route and wiki instructions; otherwise it stays gated, not quietly
coupled to unshipped SDK management methods.

R3 depends on R2 and the management portion of L4: existing-IP attach,
detach, delete, get, and populated WAN models. Each release gets its own
unit checks, independent review, and authorized evidence. A standalone
R3 run needs a new approval; no release inherits permission to buy.

Defer empty-ID auto-allocation and `QuoteAttachFloatingIP` to a gated
extension. Its intended quote takes `*AttachFloatingIPInput`, needs
`ServerID` to read the zone, rejects an existing IP ID, and returns
`pricing.GetQuoteOutput`; attach would send `auto/attach` once after a
positive IP quote within `MaxPrice`. Neither method/branch ships until
its quote, charge, orphan behavior, and separate live run are established.

## Owner decisions

Decided by the manager under delegated owner authority:

1. Use derived `Server.PublicIP` for list and query use; External
   Interface addresses stay outside it to keep the mapping evidence-based.
2. Use the shared SDK exposure guard with `AllowOpenLogin`; its snapshot
   and guest-port limits are accepted. It protects SDK and CLI callers.
3. Keep the IP on detach with explicit billing Output to preserve the
   address; `--delete` performs detach-then-delete.
4. Use floating IP names in `network`, keeping existing WAN APIs
   unchanged to preserve callers and service ownership.
5. Keep the existing quote Output with checked sums and labeled fallback
   properties to preserve its type and explain the full monthly price.
6. Use one-IP L4 with a 467,800 VND gross quote cap; defer auto-allocation
   to avoid another purchase. Evidence gates and manager run-time approval
   naming account, region, cleanup method, and mode still apply.

## Evidence gates

The [missing evidence](vserver-floating-ips-api.md#missing-evidence)
remains blocking per operation. Schemas, provider code, synthetic tests,
and owner approval never substitute for raw billing or IAM evidence.
Keep the SDK standard-library only, with existing public import paths.
