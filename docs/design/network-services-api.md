# vNetwork NAT and VPN API Evidence

Status: Populated list evidence verified (2026-10-11). These observations
support the [read design](network-services.md); they do not approve it.

Root-console paid probes establish Public NAT V2 with high availability
(HA) and site-to-site VPN list shapes. The schemas below contain field
names and types only. A null-only field has no proven non-null type.

## Verified requests

The regional console origins are:

- HCM: `https://hcm-3-vnetwork.console.greennode.ai`
- HAN: `https://han-1-vnetwork.console.greennode.ai`

HAN uses the same route templates. All paths include the gateway prefix.

```text
GET    /vnetwork-gateway/vnetwork/v1/regions
GET    /vnetwork-gateway/vnetwork/v1/{zoneId}/{projectId}/nats
DELETE /vnetwork-gateway/vnetwork/v1/{zoneId}/{projectId}/nats/{natId}
GET    /vnetwork-gateway/vnetwork/v1/{projectId}/vpns
DELETE /vnetwork-gateway/vnetwork/v1/{projectId}/vpns/{vpnId}
```

VPN paths have no zone segment. List and delete requests returned HTTP
200. VPN delete returned `{success}`; NAT delete returned
`{message, code, success}`. Delete evidence does not authorize SDK writes.

On 2026-10-10, the HCM empty lists also returned HTTP 200 in a fresh browser
context with only an IAM bearer Authorization header and no cookies.
No portal, project, or region headers were needed. The 2026-10-11 populated
probes used the root console. They do not establish SDK login-provider
behavior, service-account auth, or cookie-free HAN replay.

Both lists use a JSON `params` query. NAT sends:

```json
{"search":[],"sort":{},"page":1,"size":10}
```

VPN sends:

```json
{"search":[{"field":"any","value":""}],"sort":{},"page":1,"size":10}
```

The empty `any` filter is verified. Nonempty search, other filters, sort
orders, page advancement, and a size cap remain unverified. Page 1 and size
10 are accepted values, not evidence of omitted-query server defaults.

## Envelopes and regions

Regions returns boolean `success` and array `data`. Region entries have
string fields `uuid`, `name`, `gatewayUrl`, `vnetworkDashboard`, `code`,
and `vserverEndpoint`. Their presence alone does not establish the NAT
zone mapping or a trusted credential destination.

Populated NAT and VPN lists have this shape:

```text
success: boolean
data: object[]
page: integer
size: integer
totalPage: integer
total: integer
```

The verified empty NAT and VPN lists omit `data`:

```json
{"success":true,"page":1,"size":10,"totalPage":0,"total":0}
```

An empty endpoints list also omits `data`. Missing `data` is an empty
result only for a verified route and envelope. It is not a general rule
for all service lists. The read design specifies presence-aware decoding.

## Public NAT row

These types cover provisioning, failed HCM, and active HAN responses:

```text
uuid, natName, status: string
portalUserId, visible, message: null
natGatewayIp, publicIp: string | null
natPackage: NATPackage object
vpc: VPC object
subnet: null
createdAt, updatedAt: string
deletedAt, billingStatus: string | null
projectUuid, zoneUuid: string
```

`uuid` has the `nat-` prefix. `subnet` remains null even when ACTIVE.

`NATPackage`:

```text
id, uuid, name, createdAt, packageId: string
isDefault, resourceServiceId, billingSku, serviceName: null
price, currencyUnit, description, status: null
natServiceId, natVersion, billingUuid, checksum, lastSyncTime: null
monthlyPrice: integer
default: boolean
image: object
  id, uuid, imageType, imageVersion, licence: string
  flavorZoneIds: string[]
  packageLimit: object
    cpu, memory, diskSize: integer
  licenseKey: null
```

`monthlyPrice` is 0 and is not a package price. The units of the package
limit numbers are not established by these field names.

### VPC object

NAT `vpc` and VPN `vpcDetailModel` share this observed shape:

```text
uuid, name, cidr, status: string
regionId, projectId, lastSyncTime, dnsStatus: string
elasticIps: empty array; element type unverified
subnets, createdAt, updatedAt, regionUuid, projectUuid: null
secGroups, project, region, zones: null
```

## VPN row

Provisioning and active responses establish:

```text
subnetDetailModel: Subnet object
vpcDetailModel: VPC object
projectDetailModel: Project object
packageModel: VPNPackage object
packageUuid, vpnName, localNetworkCidr, createdAt, status, uuid: string
remoteGatewayIp, remoteNetworkCidr, ipSecConfig: null
localGatewayIp, vpnGatewayIp: string | null
vpnSites: VPNSite[]
billingStatus, zoneUuid: string
```

`Subnet`:

```text
uuid, name, status, cidr, subnetType: string
updatedAt, lastSyncTime, zoneId: string
vpcUuid, routeTableUuid, interfaceAclPolicyUuid: null
interfaceAclPolicyName, createdAt, vpc, zone: null
```

`Project`:

```text
id, backendProjectId, vserverProjectId: string
portalUserId: integer
```

`portalUserId` identifies the account and must be omitted from SDK models.

`VPNPackage`:

```text
uuid, name, packageId: string
monthlyPrice, tunnelLimit: integer
default: boolean
id, createdAt, resourceServiceId, billingSku, serviceName: null
description, currencyUnit, price: null
```

`monthlyPrice` is 0 and is not a package price.

`VPNSite`:

```text
remoteGatewayIp, preShareKey, uuid, status, siteName, createdAt: string
phase1IkeLifeTime, phase1Status: null
phase1Configs: object[]
  id: null
  phase1Algorithm, phase1Hash, phase1DhGroup: string
  phase1IkeLifeTime: string
tunnels: VPNTunnel[]
```

`VPNTunnel`:

```text
siteUuid, tunnelName, remoteNetworkCidr, uuid, status, createdAt: string
phase2IkeLifeTime, phase2DhGroup, phase2Status: null
phase2Configs: object[]
  id: null
  phase2Algorithm, phase2Hash, phase2DhGroup: string
  phase2IkeLifeTime: string
```

Sites, tunnels, and phase configuration arrays arrive inline in the VPN
list. Inventory needs no child route. Lifetime values inside configurations
are strings; the same-named outer fields are null. Phase status fields are
null in both provisioning and active records.

### VPN response security

Each `vpnSites[].preShareKey` is returned in plaintext. Every SDK VPN read
must set `transport.Request.Sensitive`. Never model, capture, log, or return
the key. Omit `projectDetailModel.portalUserId` as well. Use synthetic VPN
fixtures only, built from field names and types with invented values.
Do not persist or reuse a live secret-bearing body as a fixture source.
Sensitive requests and the read design's fixed-error rules must cover
successes, malformed bodies, and server errors.

## Status, billing, and cleanup observations

VPN connections, sites, and tunnels changed from `PROVISIONING` to `ACTIVE`
after about 3.5 minutes, even with an unreachable peer. `billingStatus`
remained `provisioning`; `phase1Status` and `phase2Status` were null.
`ACTIVE` therefore does not establish tunnel connectivity.

HAN NAT changed from `PROVISIONING` to `ACTIVE` after about 6 minutes.
Its gateway and public IP fields became strings and `billingStatus` became
`active`. HCM NAT changed from `PROVISIONING` to `ERROR` after about 5
minutes in a VPC with a stuck network ACL. Its `billingStatus` was null.
The observation does not establish the cause of the failure.

Creating a NAT adds a default route to the VPC route table; every VM in
that VPC then egresses through the NAT. Immediately after NAT deletion,
VPC deletion returned HTTP 400 with "currently being used by a vNetwork"
for under a minute.

Published package prices shown in the console on 2026-10-11:

| Service | Package | VND/month | Tunnel limit |
|---|---|---|---|
| VPN | Standard | 545,700 | 4 |
| VPN | Medium | 1,691,400 | 10 |
| Public NAT V2 HA | Standard | 712,400 | Not applicable |

Standard is the only NAT package. VPN checkout defaults auto-renew on.
VPN deletion refunded the full price within minutes. The failed NAT order
was automatically refunded in full; deleting ACTIVE NAT refunded its full
price at once. These are observed outcomes, not a refund guarantee.

## Evidence still needed

- SDK login-provider reads and safe IAM-denial behavior.
- NAT region-to-zone mapping, selected-project namespace, and trusted
  gateway selection for HCM and HAN; cookie-free HAN authorization.
- Multi-page behavior, additional search or sort inputs, and any size cap
  before those capabilities are claimed as verified.
- Non-null shapes for null-only fields and elements of empty arrays before
  exposing them in public models.
- Detail and NAT rule routes and schemas before adding those reads.

Populated inventory models no longer need another paid probe. No verified
child route is needed to expose inline VPN sites and tunnels. Follow the
[live-data rules](../../instructions/live-data.md) for account data and the
stricter VPN response rules above for secret-bearing bodies.
