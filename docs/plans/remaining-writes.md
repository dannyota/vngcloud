# Remaining writes

Completed work stays checked below. [Short-term work](../../TODO.md) owns open actions and prerequisites. Follow the accepted domain contracts, [testing](../../instructions/testing.md), [release rules](../../instructions/release.md), and [live-data rules](../../instructions/live-data.md). Release one feature per tag; paid or uncleanable writes require owner approval.

## Completed work

- [x] Add DHCP option sets, private virtual IPs, and resource tags under the [network write contract](../design/vserver-network-writes-2.md).
- [x] Add vStorage reads, bucket create and delete, S3 keys, service-account keys, bucket policy, versioning, and CORS under the [storage contract](../design/storage.md) and its linked mechanisms.
- [x] Add paid server and volume writes under the [compute and volume contract](../design/vserver-paid-writes.md).
- [x] Add paid load balancer writes under the [load balancer contract](../design/lb-writes.md).
- [x] Add log alarm writes under the [monitor contract](../design/monitor-log-alarms.md).
- [x] Decode the IAM accounts API error wrapper under the [IAM API contract](../design/iam-writes-api.md).
- [x] Add CDN API-key configuration, certificate and API-key reads, Web Accelerator updates, status toggles, delete, and path purge under the [CDN API](../design/cdn-api.md), [write](../design/cdn-writes.md), and [CLI](../design/cdn-cli.md) contracts.
- [x] Add encrypted volume and server-disk inputs to create and quote, encryption type decoding, and the server volume envelope fix under the [encrypted volume contract](../design/encrypted-volumes.md).
- [x] Add flavor and volume-type lookup by zone, the create-server ID guide, and list-output query guidance under the [CLI usability contract](../design/cli-usability.md).
- [x] Limit quote requirements to priced inputs while validating supplied optional fields under the [quote contract](../design/cli-usability.md#quotes-ask-only-for-priced-values).
- [x] Document gateway root-disk quote text and zone enablement hints, add read-only profile setup guidance and empty-configuration path hints, and accept root version flags under the [CLI usability contract](../design/cli-usability.md#smaller-fixes).

CDN API create remains deferred under the [create contract](../design/cdn-writes.md#create). Certificate writes and later live-run prerequisites remain in [short-term work](../../TODO.md). The checked items record completed scope, not approval for another live run or release; [release notes](../../RELEASE_NOTES.md) retain versioned facts.
