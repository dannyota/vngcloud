# Private endpoints

`network.Client` quotes, creates, and deletes one vStorage Standard private
endpoint in `hcm-3`. Other offers, zones, and regions are not supported.
`ListEndpoints`, `GetEndpoint`, and `ListEndpointTags` read inventory; see
[CLI Network](CLI-Network.md). An endpoint changes network reachability. It
does not grant bucket access and does not prove S3 connectivity.

## Requirements

- Region `hcm-3`, SDK IAM-user login, and default auth, VServer, and
  vNetwork endpoints. Any override returns `vngcloud.ErrInvalidConfig`
  before a request.
- An existing ACTIVE VPC (`net-<uuid>`) and subnet (`sub-<uuid>`) in
  availability zone `HCM03-1C`. The SDK never creates either.
- The project is confirmed through the VServer project list; a configured
  project ID only narrows that list.

The SDK resolves the category, the `vstorage.hcm03` Gateway service, and the
`Standard` package from the catalog and refuses a missing, repeated, or
conflicting row.

## Quote, create, delete

```go
in := &network.CreateEndpointInput{Name: "demo", VPCID: vpcID, SubnetID: subnetID, MaxPrice: 712400}
quote, err := client.QuoteCreateEndpoint(ctx, in)
out, err := client.CreateEndpoint(ctx, in)
del, err := client.DeleteEndpoint(ctx, &network.DeleteEndpointInput{EndpointID: out.EndpointID, VPCID: vpcID})
```

- `QuoteCreateEndpoint` reads a fresh monthly price in VND and ignores
  `MaxPrice`. It works on a read-only profile.
- `CreateEndpoint` takes the same quote, returns `vngcloud.ErrPriceAboveMax`
  when it exceeds `MaxPrice` (equality passes, zero blocks every purchase),
  and sends one POST. The body requests auto-renew off and private DNS off.
  The cap bounds the quote, not the debit, and is not a price lock. The call
  waits up to 15 minutes for `ACTIVE`. The output has the endpoint ID, the
  last verified status, and the quote. GreenNode billing recorded a created
  endpoint as `AUTO-RENEW` despite the request, and no read can confirm its
  renewal setting. Check billing after create. Use a fresh, unique name for
  every create.
- `DeleteEndpoint` requires exactly one ACTIVE endpoint of the named VPC and
  offer with no attached resource and no domain, sends one DELETE, and
  waits up to 10 minutes for the ID to leave a complete list. `Deleted` is
  true only then. Deleting an endpoint a cluster uses breaks that cluster's
  private access; the checks cannot detect that. Deletion promises no refund,
  and a refund can stay pending.
  In one probe, an endpoint that reused a just-deleted endpoint's name was
  deleted but never refunded, and billing still listed it as active. Check
  `billing list-resources` after delete.

## Errors

Pre-dispatch refusals and definitive 4xx replies return nil output and fixed
text. A 408, a 5xx, a lost reply, or any reply that cannot rule out execution
returns `network.ErrNotSettled` with the known ID and no retry. After
`ErrNotSettled`, run `network list-endpoints` and `network get-endpoint` in
the same scope and inspect billing before another write. Never repeat a
create to fix an unsettled one.

## CLI

```sh
vngcloud network quote-create-endpoint --region hcm-3 --name demo --vpc-id <net-id> --subnet-id <sub-id>
vngcloud network create-endpoint --region hcm-3 --name demo --vpc-id <net-id> --subnet-id <sub-id> --max-price <VND> --yes
vngcloud network delete-endpoint --region hcm-3 --endpoint-id <enp-id> --vpc-id <net-id> --yes
```

Both writes need `--yes`; create also needs `--max-price`. There is no
`--no-wait` or `--force`. Read-only profiles refuse both writes. Partial
output after `NotSettled` prints on stdout and ignores `--query`.
