# Volume guard fixtures

`get_detached_volume_synthetic.json` is a synthetic raw response whose top-level
`data` field names and types match a live detached volume detail response. Live
evidence showed `serverId` null and `serverIdList` an empty array. Synthetic
values replace account and resource data. The fixture exercises this shape
through `GetVolume` and `DeleteVolume`. Missing `serverId`, or missing or null
`serverIdList`, fails closed.

The existing `get_volume.json`, `list_volumes.json`, and `list_snapshots.json`
remain unchanged and decode through the SDK tests. Adversarial responses in
the guard, key, wait, and pagination tests use synthetic inline data and
loopback servers; no live verification supplied these cases.
