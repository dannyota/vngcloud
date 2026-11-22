# Volume guard fixtures

`get_detached_volume_synthetic.json` is a synthetic raw response built from
the existing `Volume` fields in
[vServer paid writes](../../docs/design/vserver-paid-writes.md#volumes).
It exercises explicit empty attachment fields through `GetVolume` and
`DeleteVolume`. It is not a live capture or evidence that every regional
response includes these fields. Missing or null state fails closed.

The existing `get_volume.json`, `list_volumes.json`, and `list_snapshots.json`
remain unchanged and decode through the SDK tests. Adversarial responses in
the guard, key, wait, and pagination tests use synthetic inline data and
loopback servers; no live verification supplied these cases.
