# Load Balancer

`loadbalancer` is `danny.vn/vngcloud/loadbalancer`, with its own `New(cfg)`.
It reads load balancers, listeners, pools, health monitors, pool members,
policies, tags, packages, and certificates; see the [Load Balancer section
of Services](Services.md#load-balancing) for the full read list and for
certificate writes. This page covers the paid and free writes: creating,
resizing, and deleting a load balancer, and creating, updating, and
deleting its pools (with health monitors and members), listeners, and L7
policies.

If a load balancer is managed by OpenTofu or Terraform, a write made here
drifts from that state; keep such a resource's writes in its own tool.

## Setup

```go
package main

import (
	"context"
	"log"

	"danny.vn/vngcloud"
	"danny.vn/vngcloud/loadbalancer"
)

func main() {
	ctx := context.Background()

	cfg, err := vngcloud.NewConfig(
		vngcloud.WithRegion("hcm-3"),
		vngcloud.WithIAMUser(&vngcloud.IAMUserAuth{
			RootEmail: "<root-email>",
			Username:  "<iam-username>",
			Password:  "<password>",
		}),
	)
	if err != nil {
		log.Fatal(err)
	}

	client := loadbalancer.New(cfg)
	_ = ctx
	_ = client
}
```

The rest of this page assumes `cfg` and `ctx` from this setup, plus
`client := loadbalancer.New(cfg)`.

## The price guard

`CreateLoadBalancer` and `ResizeLoadBalancer` both cost money. Each has a
matching `Quote` method, `QuoteCreateLoadBalancer` and
`QuoteResizeLoadBalancer`, that takes the same Input and prices it without
ordering anything; see [Billing and
Pricing](Billing-and-Pricing.md#quoting-a-paid-write). The write itself
quotes again with its own code before sending anything, and refuses with
`loadbalancer.ErrPriceAboveMax`, naming both amounts, when the quote's
`OptimumPrice` is above `Input.MaxPrice`. `MaxPrice` defaults to 0, so a
bare `CreateLoadBalancerInput{}` orders nothing: every package prices above
0 VND today. A create quote of 0 or less, or a resize quote of exactly 0,
refuses with `vngcloud.ErrUnpriced` whatever `MaxPrice` is, since nothing in vLB is free and such a quote means
the gateway could not price the input. Set `MaxPrice` to what `QuoteCreateLoadBalancer` returned, or
higher, to let the order through:

```go
in := &loadbalancer.CreateLoadBalancerInput{
	Name: "web", PackageID: packageID, Type: loadbalancer.TypeLayer4,
	Scheme: loadbalancer.SchemeInternal, SubnetID: subnetID, ZoneID: zoneID,
}
quote, err := client.QuoteCreateLoadBalancer(ctx, in)
if err != nil {
	log.Fatal(err)
}
in.MaxPrice = quote.OptimumPrice
created, err := client.CreateLoadBalancer(ctx, in)
```

A `MaxPrice` that is `NaN`, `+Inf`, `-Inf`, or negative fails with
`vngcloud.ErrInvalidInput` before any request, including the quote: none of
those values can be compared against a quote safely, so the guard fails
closed rather than order anyway. Once a create or a resize is sent, it is
never sent again, whatever the failure; see [Identifiers and
retries](#identifiers-and-retries).

## Creating and deleting load balancers

```go
created, err := client.CreateLoadBalancer(ctx, &loadbalancer.CreateLoadBalancerInput{
	Name:      "web",
	PackageID: packageID, // from client.ListPackages(ctx, in)
	Type:      loadbalancer.TypeLayer4,
	Scheme:    loadbalancer.SchemeInternal,
	SubnetID:  subnetID,
	ZoneID:    zoneID,
	MaxPrice:  400000,
})
if err != nil {
	log.Fatal(err)
}
log.Println(created.LoadBalancer.UUID, created.QuotedPrice)

if _, err := client.DeleteLoadBalancer(ctx, &loadbalancer.DeleteLoadBalancerInput{
	LoadBalancerID: created.LoadBalancer.UUID,
}); err != nil {
	log.Fatal(err)
}
```

`Scheme` has no default, unlike the console's `Internet`: `SchemeInternet`
gets a public address, and a listener on it with an open `AllowedCIDRs`
serves the whole internet, so the caller names that exposure explicitly.
`ZoneID` is also required, with no default: a load balancer's default zone
can be one the account cannot use.

`CreateLoadBalancer` is a `POST` and is never retried after a failure that
may already have reached the server: after any error that is not a 4xx
`*vngcloud.APIError`, the load balancer may have been ordered. List load
balancers by `Name` and match it exactly before ordering again, rather than
retrying blind. Without `NoWait`, it then waits for `CREATED`; see
[Busy and waits](#busy-and-waits).

`DeleteLoadBalancer` reads the load balancer first: an unknown id fails with
`vngcloud.IsNotFound(err) == true`, sending nothing, and one already
`DELETING` is waited on without a second `DELETE`. What the server does
with a load balancer's listeners and pools when it is deleted is not
decided by this SDK; the server's own behavior applies. `DELETE` is
idempotent and keeps the transport's normal retries. Without `NoWait`, it
then waits for a 404.

## Resizing

```go
resize := &loadbalancer.ResizeLoadBalancerInput{LoadBalancerID: lbID, PackageID: mediumPackageID}
quote, err := client.QuoteResizeLoadBalancer(ctx, resize)
if err != nil {
	log.Fatal(err)
}
resize.MaxPrice = quote.OptimumPrice
resized, err := client.ResizeLoadBalancer(ctx, resize)
if err != nil {
	log.Fatal(err)
}
log.Println(resized.Changed, resized.QuotedPrice)
```

`ResizeLoadBalancer` reads the load balancer first. The same `PackageID` as
its current one returns `Changed` false at once, quoting and sending
nothing. Otherwise it waits for the load balancer to be free (see
[Busy and waits](#busy-and-waits)), quotes, and sends the change as a `PUT`
that is never resent, whatever the failure (`transport.Request.Once`): a
resend could race a resize already in progress. A busy refusal from that
`PUT` itself returns `loadbalancer.ErrBusy` at once, with no retry, since
the write is paid; a rerun is safe because `ResizeLoadBalancer` always reads
first. Without `NoWait`, it then waits for `CREATED` with the new
`PackageID`.
Package IDs belong to one zone. Take the new `PackageID` from
`ListPackages` with `ZoneID` set to the load balancer's own `ZoneID` (read
it with `GetLoadBalancer`), never from a list without a zone. The SDK does
not check this: a package from another zone is refused by the server with a
400 `Invalid package id`, and nothing is charged.

## Pools and health monitors

```go
pool, err := client.CreatePool(ctx, &loadbalancer.CreatePoolInput{
	LoadBalancerID:      lbID,
	Name:                "web-pool",
	Protocol:            loadbalancer.PoolProtocolHTTP,
	HealthCheckProtocol: loadbalancer.HealthCheckProtocolHTTP,
	HealthCheckPath:     "/healthz",
	HealthCheckDomainName: "example.com",
})
if err != nil {
	log.Fatal(err)
}

updated, err := client.UpdatePool(ctx, &loadbalancer.UpdatePoolInput{
	LoadBalancerID: lbID, PoolID: pool.Pool.UUID,
	Algorithm: vngcloud.Ptr(loadbalancer.AlgorithmLeastConnection),
})

if _, err := client.DeletePool(ctx, &loadbalancer.DeletePoolInput{
	LoadBalancerID: lbID, PoolID: pool.Pool.UUID,
}); err != nil {
	log.Fatal(err)
}
```

Empty `Algorithm` sends `AlgorithmRoundRobin`; a zero threshold, interval, or
timeout sends the server's default (3, 3, 30, and 5). `Stickiness` and
`TLSEncryption` are `*bool`; an `HTTP` pool always sends both (`false` when
nil, as the server requires), other pools only when set. The HTTP health
check fields are refused with `vngcloud.ErrInvalidInput`, before any
request, unless `HealthCheckProtocol` is `HealthCheckProtocolHTTP`
or `HealthCheckProtocolHTTPS`. On an `HTTP` check, an empty `HealthCheckPath`,
`HealthCheckMethod`, `HealthCheckSuccessCode`, or `HealthCheckHTTPVersion`
sends `/`, `GET`, `200`, or `1.1`, since the server requires all four.
`HealthCheckDomainName` is sent only when set.

`CreatePool` is a `POST` and is never retried after an ambiguous failure;
list pools by `Name` and match exactly before creating it again. A Layer 7
load balancer takes only `PoolProtocolHTTP` pools; another `Protocol` returns
`vngcloud.ErrInvalidInput`, sending nothing. Without `NoWait`, it waits for
the pool to reach `CREATED`; see [Busy and waits](#busy-and-waits).

`UpdatePool` needs at least one field set, checked before any request. The
check protocol cannot change after create, so there is no field for it;
every other field is read first and resent as read when left unset,
including the whole health monitor. The `PUT` keeps the transport's normal
retries.

`DeletePool` lists the load balancer's listeners first and returns
`loadbalancer.ErrInUse`, sending nothing, when one names the pool as its
`DefaultPoolID`. A policy that redirects to the pool is left to the
server's own refusal, which also wraps `loadbalancer.ErrInUse`. `DELETE` is
idempotent.

## Pool members

The members API replaces a pool's whole member list on every write, so
`AddPoolMember`, `UpdatePoolMember`, and `RemovePoolMember` each read the
list first and change only the one member they name, by `Address` and
`Port`, never sending a caller-built whole list:

```go
added, err := client.AddPoolMember(ctx, &loadbalancer.AddPoolMemberInput{
	LoadBalancerID: lbID, PoolID: poolID, Address: "10.0.1.10", Port: 8080,
})
if err != nil {
	log.Fatal(err)
}
log.Println(added.Changed)

updated, err := client.UpdatePoolMember(ctx, &loadbalancer.UpdatePoolMemberInput{
	LoadBalancerID: lbID, PoolID: poolID, Address: "10.0.1.10", Port: 8080,
	Weight: vngcloud.Ptr(5),
})

if _, err := client.RemovePoolMember(ctx, &loadbalancer.RemovePoolMemberInput{
	LoadBalancerID: lbID, PoolID: poolID, Address: "10.0.1.10", Port: 8080,
}); err != nil {
	log.Fatal(err)
}
```

`Address` must parse as IPv4; `Port` and `MonitorPort` (when set) must be 1
to 65535. An unset `MonitorPort` sends `Port`. `Weight` 0 sends 1.

`AddPoolMember` of a member already present with the same `Name`, `Weight`,
`MonitorPort`, and `Backup` is a no-op: `Changed` is false and nothing is
sent. One present with any different field fails with
`vngcloud.ErrInvalidInput`, naming `UpdatePoolMember` instead; nothing is
sent either way. `UpdatePoolMember` or `RemovePoolMember` of a member that
is not there fails with `vngcloud.IsNotFound(err) == true`.

Each method also checks, before sending anything, that the pool's own
embedded member list (read alongside its status) names the same members, by
`Address` and `Port`, as the separate member list read used to build the
`PUT` body. A mismatch means the two reads landed moments apart and
something else changed the pool in between (or the server has not caught
up), so resending either list as the whole set would drop or fabricate
members; this returns `loadbalancer.ErrBusy` and sends nothing, and the
write can be retried. A pool read that omits its embedded member list
entirely skips this check.

Without `NoWait`, all three wait for the pool to settle after the write and
then confirm that a fresh read names exactly the list just sent; a mismatch
means another writer changed the pool and returns
`loadbalancer.ErrNotSettled`. Once the `PUT` is sent, it is not resent on a
rerun; each method simply reads the member list again from the start.

## Listeners

```go
listener, err := client.CreateListener(ctx, &loadbalancer.CreateListenerInput{
	LoadBalancerID: lbID,
	Name:           "web",
	Protocol:       loadbalancer.ProtocolHTTP,
	Port:           80,
	AllowedCIDRs:   []string{"10.0.0.0/24"},
	DefaultPoolID:  poolID,
})
if err != nil {
	log.Fatal(err)
}

updated, err := client.UpdateListener(ctx, &loadbalancer.UpdateListenerInput{
	LoadBalancerID: lbID, ListenerID: listener.Listener.UUID,
	TimeoutClient: vngcloud.Ptr(30),
})

if _, err := client.DeleteListener(ctx, &loadbalancer.DeleteListenerInput{
	LoadBalancerID: lbID, ListenerID: listener.Listener.UUID,
}); err != nil {
	log.Fatal(err)
}
```

`AllowedCIDRs` is required with no default, unlike some other SDKs, which
send `0.0.0.0/0`: an open listener on an internet-facing load balancer
serves the whole internet, so the caller names its own list. Each entry
must parse as an IPv4 CIDR prefix with no host bits set; the SDK joins them
with commas on the wire and splits them back on a read-merge update. A
`TimeoutClient`, `TimeoutMember`, or `TimeoutConnection` of 0 sends the
server's default (50, 50, and 5 seconds).

`ProtocolHTTP` on a `SchemeInternet` load balancer carries every request in
cleartext to whatever `AllowedCIDRs` allows to reach it; use `ProtocolHTTPS`
with a certificate for anything that must not be visible on the path.

`ProtocolHTTPS` requires `DefaultCertificateID`; any other `Protocol`
refuses `CertificateIDs`, `DefaultCertificateID`, and `ClientCertificateID`
all being set, with `vngcloud.ErrInvalidInput`, before any request. The SDK
never reads the certificate itself; the server checks that it exists. See
[Load Balancer section of Services](Services.md#load-balancing) for
`ImportCertificate` and `DeleteCertificate`.

`UpdateListener` needs at least one field set. It reads the listener,
applies every set field, and sends the full body with the read values for
the rest; the merged certificate fields are checked against the listener's
own unchangeable `Protocol` the same way `CreateListener` checks them. The
read never returns `blockedCidrs`, `defaultAction`, `alpnProtocols`, or
`tlsSecurityPolicy`, so `UpdateListener` never sends any of them. Whether
the server then wipes a value set for one of these fields, since the `PUT`
is a full replace, or keeps it because the field was left out of the body
entirely, is unverified until the live check: do not assume either one. The
`PUT` keeps the transport's normal retries.

Two updates from different processes can lose one; the API has no version
field.

## Policies

L7 policies live on a listener and need a Layer 7 load balancer:

```go
policy, err := client.CreatePolicy(ctx, &loadbalancer.CreatePolicyInput{
	LoadBalancerID: lbID, ListenerID: listener.Listener.UUID,
	Name:           "api-redirect",
	Action:         loadbalancer.ActionRedirectToPool,
	RedirectPoolID: apiPoolID,
	Rules: []loadbalancer.PolicyRuleInput{
		{Type: loadbalancer.PolicyRuleTypePath, CompareType: loadbalancer.CompareTypeStartsWith, Value: "/api"},
	},
})
if err != nil {
	log.Fatal(err)
}

updated, err := client.UpdatePolicy(ctx, &loadbalancer.UpdatePolicyInput{
	LoadBalancerID: lbID, ListenerID: listener.Listener.UUID, PolicyID: policy.Policy.UUID,
	KeepQueryString: vngcloud.Ptr(true),
})

if _, err := client.DeletePolicy(ctx, &loadbalancer.DeletePolicyInput{
	LoadBalancerID: lbID, ListenerID: listener.Listener.UUID, PolicyID: policy.Policy.UUID,
}); err != nil {
	log.Fatal(err)
}
```

`ActionRedirectToPool` requires `RedirectPoolID` and refuses `RedirectURL`;
`ActionRedirectToURL` requires `RedirectURL` and refuses `RedirectPoolID`.
Any other `Action` reaches the server as given. Every rule in `Rules` must
set `Type`, `CompareType`, and `Value`.

`UpdatePolicy` needs at least one field set. A set `Rules` replaces the
whole rule list; left unset, it resends the rules as read.

## Busy and waits

A load balancer refuses a write to it or to a child of it while its
`progressStatus` is `CREATING`, `CREATING-BILLING`, `UPDATING`, or
`DELETING`; a listener, pool, or policy refuses one while its own is
`CREATING`, `UPDATING`, or `DELETING`. Before every write except a load
balancer create or delete, the SDK waits, polling every 5 seconds for up to
10 minutes, until neither the load balancer nor the child it targets is
busy, and returns `loadbalancer.ErrBusy` past that bound, sending nothing.

A busy refusal of a free write (a pool, member, listener, or policy write)
means the server did not act: the SDK goes back to that same wait and sends
again, once, within the same bound. Only a refusal matching one of the
server's busy messages triggers this resend; any other error, or a second
busy refusal, is returned as is. A resize's busy refusal instead returns
`loadbalancer.ErrBusy` immediately, since it is paid and sent only once.

In one process, writes to the same load balancer queue rather than racing
into these refusals; across processes, or across two `Client` values, the
server's own refusal and this resend are what apply.

A settled create, update, or members replace waits for the resource to
reach `CREATED` while the load balancer is no longer busy; a settled delete
waits for a 404 the same way. If the load balancer or the child reaches
`ERROR`, the returned error wraps `loadbalancer.ErrFailed`. If the wait's
bound runs out, or a read or a sleep fails, such as from a canceled `ctx`,
it wraps `loadbalancer.ErrNotSettled`: a create or a resize must not be
repeated, while every other write here always reads first and so may be
rerun.

`NoWait` skips a write's own post-write wait (but never the pre-write busy
wait above) and returns the fields the write itself sent.

## Identifiers and retries

Every path id, including one carried in a body such as `PackageID`,
`SubnetID`, `DefaultPoolID`, `RedirectPoolID`, or a certificate id, is
checked before any request; a malformed one, `..` or `/` for example,
returns `vngcloud.ErrInvalidInput`.

Every create (`CreateLoadBalancer`, `CreatePool`, `CreateListener`,
`CreatePolicy`) is a `POST` and is never retried after a failure that may
already have reached the server: after any error that is not a 4xx
`*vngcloud.APIError`, list the matching resource by `Name` and match it
exactly before creating it again, rather than retrying blind. A create
response with no id is the same kind of error. `ResizeLoadBalancer`'s `PUT`
is sent at most once (`transport.Request.Once`). Every other update and
delete keeps the transport's normal retries.

## Errors

```go
var ErrPriceAboveMax = vngcloud.ErrPriceAboveMax
var ErrBusy          = errors.New("loadbalancer: resource busy")
var ErrInUse         = errors.New("loadbalancer: resource in use")
var ErrFailed        = errors.New("loadbalancer: write failed on the server")
var ErrNotSettled    = errors.New("loadbalancer: write accepted but not settled")
```

`ErrPriceAboveMax` means a create or resize quoted above `MaxPrice`;
nothing was ordered. `vngcloud.ErrUnpriced` means a create quoted 0 or less, or a
resize quoted 0; nothing was ordered. A negative resize quote is a refund
and is allowed. `ErrBusy` means a write found the load balancer, or
the child it targets, still busy past the pre-write wait's bound, or the
server refused a resize because the load balancer was busy; see
[Busy and waits](#busy-and-waits). `ErrInUse` means `DeletePool` was
refused because a listener still names the pool as its default, or the
server's own refusal named it in use. `ErrFailed` means a write's
post-write wait saw the load balancer or its child reach `ERROR`.
`ErrNotSettled` means a write was sent, and may have reached the server,
but no read confirmed its result, or a confirming read did not match what
was sent; see [Busy and waits](#busy-and-waits) for what to do next.

A quota, a duplicate name or port, a bad package, or a lack of credit comes
back as the server's own `*vngcloud.APIError`; see [Errors](Errors.md) for
the general error model. `loadbalancer.ErrCertificateInUse` keeps the
meaning it already has; see [Load Balancer section of
Services](Services.md#load-balancing).
