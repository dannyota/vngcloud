# Private endpoint lifecycle

Status: Proposed. The SDK and CLI expose endpoint inventory only. Creation,
pricing, and deletion remain blocked by the
[API and billing evidence gates](network-endpoints-api.md).
Selected Hanoi catalog and VPC GETs work with IAM-user login. The subnet
response remains unresolved; no usable subnet catalog, quote, write, or
service-account authorization is established. The existing-command
[workload guide](../wiki/Storage-Workload-Access.md) is implemented separately.

Add the smallest platform lifecycle needed for a vStorage endpoint in an
explicit existing VPC and subnet. Keep credentials and application deployment
in the [workload access workflow](storage-workload-access.md). A private
endpoint changes network reachability; it grants no bucket rights.

## Scope and alternatives

Recommend one coherent lifecycle: quote, create, read, and delete, with
explicit private DNS choice and confirmed billing behavior. Existing
`ListEndpoints`, `GetEndpoint`, and `ListEndpointTags` keep their public
contracts. No new service package, root re-export, or dependency is needed.

Defer rename, mutable DNS, resize, renewal opt-in, endpoint policies, tags,
automatic VPC creation, security-group edits, workload binding, and automatic
cleanup of other resources. Published console rename behavior does not make
rename necessary for application access.

Alternatives are console-managed endpoints with existing SDK inventory, or
public regional HTTPS with existing credentials. Both remain available while
API evidence is incomplete. A delete-only release needs a separate owner
decision and verified deletion and billing semantics.

## Candidate public contract

These names and shapes are proposed SDK design, not observed wire schemas.
Evidence must establish each field's meaning before implementation. Any
required API field not represented here returns to design review; a raw body
escape or catch-all map is not permitted.

Methods use `(context.Context, *Input) (*Output, error)` on `network.Client`.

| SDK method | CLI command | Input |
|-|-|-|
| `QuoteCreateEndpoint` | `network quote-create-endpoint` | Create input |
| `CreateEndpoint` | `network create-endpoint` | Create input |
| `DeleteEndpoint` | `network delete-endpoint` | Delete input |

`CreateEndpointInput` has required `Name`, `ZoneID`, `VPCID`, `SubnetID`,
`ServiceID`, and `PackageID` strings, required `PrivateDNS *bool`, and
`MaxPrice float64`. `PrivateDNS` must be non-nil, so omission cannot silently
change shared DNS behavior. Region and project use configured selection.
`ZoneID` means the vNetwork route namespace. A distinct availability-zone
field is gated on evidence that establishes whether the API requires one.

Select service and package explicitly from verified offerings. The initial
scope accepts only a verified vStorage service, not an assumed literal ID or
the console label `Standard`. Catalog methods and their models remain
unassigned until evidence establishes their shape and necessity. No endpoint
write may hard-code catalog IDs or derive them from display names.

`DeleteEndpointInput` has required `ZoneID`, `VPCID`, and `EndpointID` strings.
There is no force, cascade, or `NoWait` flag. Creation and deletion each have
one bounded confirmation phase. Proposed bounds are 15 minutes for create
and 10 minutes for delete, polling every 5 seconds under a shorter caller
deadline. These are client bounds, not claimed provisioning times; verified
states and expected durations must support them before approval.

`QuoteCreateEndpointOutput` proposes `TotalPrice float64`, `Currency string`,
`PeriodMonths int`, and `AutoRenew bool`. The candidate purchase is one
prepaid month with renewal off. This is a desired safety contract, not a
claim that the endpoint product supports that billing model. If the service
uses metered or recurring billing that cannot meet it, revise the contract.
Do not label an unbounded usage estimate a purchase quote.

`CreateEndpointOutput` proposes `Endpoint *EndpointState`, `OrderID string`,
`TotalPrice float64`, `Currency string`, and `AutoRenew *bool`.
`EndpointState` proposes only typed `ID`, `Name`, `Status`, `VPCID`,
`SubnetID`, `ServiceID`, `PackageID`, `URL`, and `IP` strings, plus
`PrivateDNS *bool`. Populate fields only from verified observations. Keep
unknown status values; readiness classification needs service evidence.

Do not reuse the broad `Endpoint` inventory model in write outputs: it has
untyped fields whose contents have not been established for write safety.
Unknown fields, payment links, arbitrary nested objects, and server text
never enter new output models. `OrderID` comes only from a verified response
field. `AutoRenew=nil` means unconfirmed, never off.

`DeleteEndpointOutput` is empty only after confirmed removal. A create can
return safe partial output with an error after dispatch. Pre-dispatch errors
return nil output. No success output certifies traffic flow, a refund, or
application readiness.

## Validation and scope

Follow [ADR 0002](../adr/0002-write-api-conventions.md). Validate required
fields, ID shape, explicit DNS choice, and finite nonnegative `MaxPrice`
before discovery. Never infer a vStorage project from a vServer project.
The service's supported region mapping is a release gate.

Read the selected VPC and subnet and prove project, region, and membership.
Validate service and package eligibility from verified sources. Private DNS
requires observed VPC DNS support. Check the published per-service/per-VPC
DNS constraint without assuming it covers all endpoints or prevents races.
Reject a conflicting exact name in the selected scope. Take a complete
inventory baseline before create; incomplete or changing scans fail closed.

Before delete, require the exact endpoint ID and its VPC to match the
caller's explicit consent. Do not infer consent from an endpoint row. A
verified missing target before dispatch returns the shared not-found error.
Deletion must not implicitly delete a subnet, VPC, route, key, or policy.

Deletion also requires verified user ownership and consumer information.
Refuse VKS-managed or VKS-reused endpoints, and endpoints whose managed/shared
state cannot be established from an authoritative platform read. Names do
not prove ownership. `--yes` does not override this gate. Do not query a
cluster to establish it. API evidence must show how to distinguish ordinary
user-created endpoints from endpoints created, reused, or reconciled by VKS
before delete can ship. Known shared application use needs explicit operator
coordination; no SDK inventory can prove that every workload has stopped.

## Price, dispatch, and confirmation

Quote is a read and takes the exact create input. It shares the resolved
resource specification and price serializer with create; only `MaxPrice`
is ignored for quote. It reserves neither capacity nor a price.

Create validates scope, obtains a fresh quote, verifies amount, currency,
term, and renewal semantics, then compares `TotalPrice` with `MaxPrice`.
Zero cap refuses a purchase; equality passes. Missing, null, non-finite,
negative, zero, or untyped prices never imply a free resource. Free-create
support requires explicit verified evidence and a separately approved rule.
Do not reuse unrelated NAT, IP, or storage prices for endpoints. Traffic and
storage charges outside the quoted endpoint purchase remain separate costs.
Unless a verified server quote token binds the order, `MaxPrice` caps only
the immediate quote, not an atomic debit. That race needs explicit owner
acceptance; a different billed amount is an unsettled result, not success.

Send each mutation once with no redirects: no resend after 401, 429, 5xx,
failed dial, or ambiguous transport failure. Read retries follow the shared
bounded policy. The SDK and CLI have no outer write retries. Unknown auth
support or required checkout blocks create; do not forward tokens to payment
URLs, use browser cookies, or fall back to another region or API family.
New quote, mutation, and confirmation requests withhold raw captures and
server error bodies through the shared sensitive-request boundary. Fixed
safe errors retain only status, operation, retryability, and sentinels.

Success requires a matched identity, verified readiness, settled payment,
and observed renewal off. A create field saying renewal is off is not
confirmation. No renewal mutation is implicitly authorized. If a second
write is necessary, its route, inputs, outcome, and safety need an approved
amendment before creation can ship.

Prefer a returned resource ID. If absent, an evidence-backed correlation
rule must prove a unique new baseline match in the complete selected scope.
Do not adopt or delete a merely similar endpoint. The lack of a server
idempotency key requires operator serialization; client checks are not locks.

After a lost response, confirm by reads only. Never repeat create, follow a
checkout link, or delete a candidate as recovery. Delete confirmation must
establish both resource removal and the documented effect on future charges;
an empty page alone does not prove either. Preserve unresolved obligations.
Refund amount and timing are never guaranteed.
The confirmation bound starts after the mutation attempt and includes all
identity, provisioning, payment, and renewal reads. A caller deadline can
shorten it; timing out never proves that the mutation failed.

## CLI and errors

Quote works in read-only mode. Create and delete require a writable profile
and `--yes` before any request because endpoint and DNS changes affect shared
connectivity. Create also needs `--max-price`. Flags mirror the candidate
input names; `--private-dns=true` or `--private-dns=false` is explicit.
Resolve output format and query syntax before a mutation. No raw wire input
or secret flag is introduced.

Use existing shared input, configuration, auth, permission, not-found,
unpriced, and price-cap sentinels. Use `network.ErrNotSettled` for an
uncertain mutation, payment, renewal, or confirmation, and `network.ErrFailed`
only for a verified terminal provisioning failure. Preserve safe partial
create output. Errors name safe recovery commands and known IDs, never raw
response bodies or payment metadata.

Follow shared CLI exit precedence: local usage/read-only/consent is 2,
authentication is 3, confirmed not-found is 4, and price, permission,
unconfirmed mutation, and other API failures are 1. Cancellation retains
its shared precedence. Inspect the same scope with `network list-endpoints`
and `network get-endpoint`; unresolved billing still needs operator review.

## Release and owner decisions

1. The [workload guidance](storage-workload-access.md) is implemented using
   existing contracts, independently of endpoint writes.
2. Complete [API evidence](network-endpoints-api.md), then approve final
   SDK/CLI fields, regional and authentication scope, billing model, and
   confirmation rules. No endpoint code starts with an open dependent gate.
3. Write and independently review a bounded implementation plan for endpoint
   quote/create/delete, private typed models, guards, fixtures, examples,
   CLI commands, and matching wiki and generated reference updates.
4. Require adversarial review and the
   [endpoint checks](network-endpoints-checks.md) before landing.

Owner choices remain open: the proposed small lifecycle, explicit selectors
and DNS consent, the one-month renewal-off purchase model if supported, and
the safe partial-output and confirmation contracts. Approval of these choices
does not establish API facts or authorize a paid run.
