# IDs for Create Server

`compute.CreateServer` and `compute.QuoteCreateServer` take IDs that each
come from a read call. This page names the call that finds each one. The
examples assume `cfg` and `ctx` from [Compute's Setup](Compute.md#setup).

| `CreateServerInput` field | SDK call | ID field on each row |
|-|-|-|
| `ZoneID` | `portal.ListZones` | `"id"` (rows are maps) |
| `FlavorID` | `compute.ListFlavors` with `ZoneID` | `FlavorID` |
| `ImageID` | `compute.ListOSImages` with `ZoneID` | `ID` |
| `RootDiskTypeID`, `DataDiskTypeID` | `volume.ListVolumeTypes` with `ZoneID` | `ID` |
| `VPCID` | `network.ListVPCs` | `UUID` |
| `SubnetID` | `network.ListSubnetsByVPC` | `UUID` |
| `SecurityGroupIDs` | `network.ListSecurityGroups` | `ID` |
| `SSHKeyID` | `compute.ListSSHKeys` | `ID` |
| `ServerGroupID` (optional) | `compute.ListServerGroups` | `UUID` |

A quote needs only the zone, flavor, image, and root disk type. The other
IDs matter when you create the server. The [CLI](#cli) section lists the
matching `vngcloud` commands.

## Zone, flavor, image, and disk type

Pick a zone first; the other three lookups take it.

```go
zones, err := portal.New(cfg).ListZones(ctx, nil)
if err != nil {
	log.Fatal(err)
}
```

An `IsEnabled` value of false on a zone is a hint, not a gate.

```go
flavors, err := compute.New(cfg).ListFlavors(ctx, &compute.ListFlavorsInput{
	ZoneID: "<zone-id>",
	Name:   "s2-general-1x2",
})
images, err := compute.New(cfg).ListOSImages(ctx, &compute.ListOSImagesInput{
	ZoneID: "<zone-id>",
})
types, err := volume.New(cfg).ListVolumeTypes(ctx, &volume.ListVolumeTypesInput{
	ZoneID: "<zone-id>",
})
```

`ListFlavors` with `ZoneID` covers every flavor zone in the zone and fills
each row's `FlavorZoneID`. Sold-out flavors stay in the list; skip rows with
`IsSoldOut` true. Without `Name`, the list holds every flavor. See
[Listing flavors](Compute.md#listing-flavors) and [Listing volume
types](Volume.md#listing-volume-types) for the fan-out rules.

## Network, key, and group

```go
vpcs, err := network.New(cfg).ListVPCs(ctx, nil)
subnets, err := network.New(cfg).ListSubnetsByVPC(ctx, &network.ListSubnetsByVPCInput{
	VPCID: "<vpc-id>",
})
groups, err := network.New(cfg).ListSecurityGroups(ctx, nil)
keys, err := compute.New(cfg).ListSSHKeys(ctx, nil)
serverGroups, err := compute.New(cfg).ListServerGroups(ctx, nil)
```

Then quote and create as shown on [Compute Servers](Compute-Servers.md).

## CLI

Each command prints `{"Items": [...]}`, so a `--query` starts with `Items`.
Rows print Go field names, except `portal list-zones`, whose rows are maps
with the keys `id` and `name`.

| `create-server` flag | Command |
|-|-|
| `--zone-id` | `portal list-zones` |
| (flavor zones) | `compute list-flavor-zones --zone-id <zone>` |
| `--flavor-id` | `compute list-flavors --zone-id <zone> --name <flavor>` |
| `--image-id` | `compute list-os-images --zone-id <zone>` |
| `--root-disk-type-id`, `--data-disk-type-id` | `volume list-volume-types --zone-id <zone> --iops 3000` |
| `--vpc-id` | `network list-vpcs` |
| `--subnet-id` | `network list-subnets-by-vpc --vpc-id <vpc>` |
| `--security-group-id` | `network list-security-groups` |
| `--ssh-key-id` | `compute list-ssh-keys` |
| `--server-group-id` (optional) | `compute list-server-groups` |

Pick the ID with one `--query` per lookup:

```sh
vngcloud portal list-zones --query "Items[].[id,name]"
vngcloud compute list-flavors --zone-id <zone> --name s2-general-1x2 \
  --query "Items[?!IsSoldOut].[FlavorID,FlavorZoneID]"
vngcloud compute list-os-images --zone-id <zone> \
  --query "Items[].[ID,ImageType,ImageVersion]"
vngcloud volume list-volume-types --zone-id <zone> --iops 3000 \
  --query "Items[].[ID,Name,MinSize,MaxSize]"
vngcloud network list-vpcs --query "Items[].[UUID,Name]"
vngcloud network list-subnets-by-vpc --vpc-id <vpc> \
  --query "Items[].[UUID,Name]"
vngcloud network list-security-groups --query "Items[].[ID,Name]"
vngcloud compute list-ssh-keys --query "Items[].[ID,Name]"
vngcloud compute list-server-groups --query "Items[].[UUID,Name]"
```

`--zone-id` on `list-flavors` and `list-volume-types` makes one request for
the zone's flavor zones or volume type zones, then one per flavor zone or
volume type zone. `--name` and `--iops` match exactly. A row's `ZoneID` is
the API's own zone identifier, not the network zone name; tell rows apart by
`FlavorZoneID` (flavors) or `VolumeTypeZoneID` (volume types). See
[CLI: Compute](CLI-Compute.md#list-flavors) and
[CLI: Volume](CLI-Volume.md#list-volume-types).
