# v1 hardening plan

Close credential disclosure, destructive guard bypasses, and mutation replay. Hardening alone does not qualify the first stable release.

## v1.0.0 feature gate

The gate covers AWS CLI-style resource creates, including creates that charge existing wallet credits. Account funding, top-ups, and payment-method management are out of scope.

- [x] Inventory and ship the in-scope creates. GLB (bare and TCP children), VKS, vDB MySQL, and the private endpoint ship; see the [resource creates plan](resource-creates.md).
- [x] Owner decision: site-to-site VPN and CDN Web Accelerator creates are out of scope for `v1.0.0` because they are rarely used. Held reads, other vDB engines, CDN certificates, and manual renewal orders are additive and ship in `1.x`.

## v1.0.0 release checklist

Tag `v1.0.0` only after every item below lands. Items that change a public name, type, field, query key, or exit code come first, because after `v1.0.0` they would be breaking.

- [ ] Shared settlement names: define `ErrNotSettled` and `ErrFailed` once in shared core with root exports, and keep each service's existing sentinel as an alias ([slice](#shared-settlement-names)).
- [ ] Endpoint help and wiki: the auto-renew-off request is not reflected in billing, which recorded `AUTO-RENEW`; a refund after delete can stay pending; give each create a fresh, unique name, because one endpoint that reused a just-deleted name was never refunded ([probe record](../design/network-endpoints-api.md#recorded-hcm-3-create-and-delete-probe)).
- [ ] Fix `network list-nat-instances`, which fails with `invalid regions envelope` because region rows carry a `VServerEndpoint` key; decode only the fields used, as the endpoint region read does.
- [ ] Require `--zone-id` on `volume get-default-volume-type`; the provider default zone `HCM03-1A` is disabled and returns 404.
- [ ] Document that `compute.Flavor.FlavorZoneID` holds the request zone ID (for example `HCM03-1B`) and `ZoneID` holds an internal zone UUID.
- [ ] Bound the IAM user login page and token response reads in `internal/iamuser/login.go`.
- [ ] Make the monitor quota tests and `TestRedactionBoundedHTTPErrorCost` stable under load, and bring the CI `go` job back under its 10-minute limit.
- [ ] Fix the documentation slice below: config fields, 401 and retry guidance, repeated-write recovery, private DNS, the resize cap example, and Python setup ([slice](#documentation-and-evidence)).
- [ ] Help and wiki: `compute create-server` user data with shell `runcmd` gets HTTP 403; each list command's output root (for example `Items`); `get-server-console-log` returns only the current boot; Telegram channel addresses.

## Cross-cutting rules

- Credential destinations: reject unsafe region labels before endpoint construction and never use a response-selected credential destination without matching an approved regional origin; preserve explicit caller endpoint overrides under their existing contract. Applies to `internal/core/client.go:87`, `internal/endpoints/endpoints.go:108`, `network/models.go:530`, and `network/network.go:676`.
- Credential lifetime and privacy: snapshot IAM configuration into private session state; treat passwords, TOTP seeds, sent tokens, refreshed tokens, issued secrets, and credential-bearing request fields as sensitive through the entire operation, dependent reads, capture hooks, and cleanup. Reject reflected credentials in required identities and returned models; omit unsafe optional fields only where the contract permits omission. Applies to `internal/core/client_options.go:178`, `internal/core/auth.go:24`, `internal/core/auth.go:104`, `internal/core/totp.go:32`, `compute/ssh_keys_write.go:208`, `portal/portal.go:47`, `storage/s3_keys.go:154`, `iam/service_accounts_write.go:96`, `iam/service_accounts.go:77`, `containerregistry/users_write.go:192`, `containerregistry/users_write.go:272`, `billing/billing.go:129`, `internal/billingresources/resources.go:96`, `monitor/channels_write.go:87`, `monitor/writes.go:197`, `internal/cli/svc_containerregistry.go:105`, and `internal/cli/svc_containerregistry.go:159`.
- Value-bearing diagnostics: untrusted values must not escape in error messages, codes, formatting, JSON, logs, captures, or retained causes; keep only safe classifications, context sentinels, and sanitized causes. Query failures use fixed text, and rendering failures after accepted writes must say the write succeeded. Applies to the preceding credential paths, `internal/cli/errors.go:61`, and `internal/cli/svc_monitor.go:411`; browser helper authentication and tool-token process arguments are additional local credential boundaries at `scripts/browser-creds.py:119` and `scripts/tools-outdated.sh:23`.
- Bounded reads: apply finite decompressed response limits before allocation, capture, and decoding on success and failure; reject overflow without partial output, and cap cumulative retained discovery bytes. Applies to default transport at `internal/transport/transport.go:547`, IAM login at `internal/iamuser/login.go:261` and `internal/iamuser/login.go:310`, all six budget writes at `billing/budgets.go:232`, VPN at `network/vpn.go:60`, older compute/network and portal calls, IAM/container registry/DNS/tagging/VKS/backup calls including `vks/vks.go:41`, `backup/backup.go:101`, and `tagging/tagging.go:359`, CDN at `cdn/vcdn.go:102`, older load balancer/global load balancer/monitor reads including `loadbalancer/price_guard.go:44`, and retained discovery responses at `internal/discovery/session.go:204`. Shared defaults close omitted limits; existing tighter operation limits remain effective.
- Send once: paid orders, destructive mutations, and snapshot replacement writes must not replay after ambiguous dispatch, including retries, 401 recovery, and redirects; reconcile with reads and report uncertainty. Applies to `network/routes_write.go:348`, `storage/buckets_delete.go:82`, `monitor/logprojects_write.go:142`, and budget writes at `billing/budgets.go:232`. Refuse operation redirects for quote POSTs at `pricing/pricing.go:135` too. Preserve existing send-once server, volume, NAT, IAM, load balancer, and native S3 behavior. A documented busy refusal can permit another attempt only after a fresh read, merge, and validation, including `loadbalancer/pool_members_write.go:157`.
- Identity and cleanup authority: require explicit matching identity and required state before guards, replacement writes, or confirmations; only attributable create evidence authorizes cleanup, never a name match alone. Applies to `compute/compute.go:115`, `volume/volume.go:117`, `volume/delete_volume.go:54`, `cdn/webaccelerator.go:75`, `cdn/update.go:159`, `iam/service_accounts_write.go:101`, `iam/guard.go:459`, `iam/policies.go:114`, `internal/cli/svc_iam.go:249`, `containerregistry/users_write.go:321`, and `internal/cli/svc_containerregistry.go:133`. Preserve an issued secret in an uncertain partial result even if the create ID is missing at `iam/service_accounts_write.go:91`.
- Complete evidence: an explicit empty collection differs from missing or null data; reject duplicate keys, conflicting envelopes, repeated pages or IDs, unstable totals, absent required preserved fields, and exhausted scan budgets before concluding absence or constructing replacement writes. Applies to `internal/core/pagination.go:66`, `internal/core/flexible.go:33`, `network/acls_write.go:157`, `storage/project_wait.go:68`, `tagging/tagging.go:367`, `dns/zones_write.go:183`, `containerregistry/repositories_write.go:118`, `containerregistry/users_write.go:414`, `monitor/logprojects_write.go:220`, and `monitor/logalarms_body.go:408`. Also reject malformed ordinary reads at `portal/portal.go:192` and `billing/billing.go:152`. Preserve specifically evidenced omitted-list behavior for empty regional storage project lists; do not generalize it to deletion guards.
- Complete aggregates: walk every supported pagination level with progress, identity, count, and cancellation checks, or return an incomplete-result error. Applies to server and server-group aggregates at `compute/compute.go:273`, network security-group aggregates, and both volume and snapshot pages at `volume/volume.go:359`; page metadata must remain available to DNS, registry, and IAM callers at `dns/dns.go:145`, `containerregistry/containerregistry.go:38`, and `iam/policies.go:41`.
- Deadlines: give polling requests and sleeps the earlier of the caller and operation deadline, and reject late success; canceled waiters must not change another caller's token or cancel shared work they do not own. Applies to `network/waits.go:64`, `storage/waits.go:41`, `volume/waits.go:58`, `dns/waits.go:97`, `containerregistry/waits.go:53`, `loadbalancer/waits.go:68`, `monitor/waits.go:35`, `storage/discovery.go:139`, `monitor/toggle.go:129`, and `monitor/update.go:109`. Shared auth/project coordination at `internal/core/auth.go:70`, `internal/core/client.go:211`, `internal/core/project.go:68`, and `internal/transport/discovery.go:126` needs the explicit contract decision below; billing history at `billing/history.go:20` inherits it.
- Honest outcomes: distinguish not sent, possibly sent, accepted but unconfirmed, and confirmed; keep useful partial output and safe error identities without allowing nested HTTP errors to override operation uncertainty. Applies to `tagging/tagging.go:395`, `iam/service_accounts_write.go:181`, `network/nat_write.go:21`, `network/nat_write_wait.go:24`, all newly bounded write responses, and CLI rendering after writes. Recovery guidance must recommend reads and ownership reconciliation, including `compute/waits.go:192`, `storage/s3_keys.go:212`, and registry ambiguous-create hints.

## Ownership and execution

Each slice runs in its own worktree and pull request, implemented by an implementer and reviewed by a reviewer under the repository routing. No slice authorizes live calls. IAM, registry, DNS, and tagging; Monitor; Load balancer and CDN; Developer credential and live-write safety; and Example checker start now. Core boundaries, CLI privacy and consent, Compute and network, Storage and volume, Billing and pricing, and SDK input and price contracts start after the setup cost estimate lands, because they share files with it. Shared settlement names and Documentation and evidence come last.

Slices are ordered by their highest risk. Lower-risk changes sharing a file stay with that file's owner instead of creating overlapping slices. Every owned directory includes its tests and new sibling files unless an exclusion is named; exclusions apply to existing and new matching files. Each slice owns the wiki pages, generator notes, and fixtures for its packages and updates them in the same change as the public surface; Documentation and evidence covers only cross-cutting documents. Design revisions that a decision requires are the manager's and land before the slice starts.

Source ownership is disjoint. Core boundaries excludes `internal/core/errors.go`; Compute and network and Storage and volume exclude the explicit SDK contracts files and their named tests. CLI privacy and consent owns all CLI adapters and generated-help sources, including adapters needed by SDK contracts. Workers can prepare independent changes in parallel after manager decisions; shared sentinel adoption, shared-core integration, SDK adapters, fixtures, and generated documentation require coordinated integration and checks before acceptance.

For each code slice, run its focused commands below while working and `make check` once before reporting; success means exit zero with the new regressions included. Report a failure outside the slice instead of retrying it. Keep synthetic adversarial inputs separate from sanitized raw fixtures and never introduce live credentials into tests.

## Core boundaries

- [x] Risk: high security and paid-create safety.
- Owned paths: `internal/core/` except `internal/core/errors.go`; `internal/endpoints/`; `internal/transport/`; `internal/tokencache/`; `internal/iamuser/`; `internal/discovery/`.
- Closes: region credential redirection at `internal/endpoints/endpoints.go:108` and `internal/core/client.go:87`; mutable IAM/cache identity at `internal/core/client_options.go:178` and `internal/core/client.go:134`; unsafe cache files at `internal/tokencache/cache.go:158`; credential formatting at `internal/core/auth.go:24` and `internal/core/totp.go:32`.
- Closes: unbounded transport/login at `internal/transport/transport.go:547` and `internal/iamuser/login.go:261`; invalid token lifespan at `internal/iamuser/login.go:189`; cumulative discovery retention at `internal/discovery/session.go:204`; false empty lists and duplicate guards at `internal/core/flexible.go:33` and `internal/core/pagination.go:66`; queued cancellation at `internal/transport/discovery.go:126` and the shared core locks listed above.
- Change: validate DNS-label regions before resolution, snapshot credential fields without copying mutexes, keep tokens in private redacting state, and reuse checked bounded file reads for IAM caches while preserving cache hashes. Refuse symlinks, unsafe modes, nonregular files, and oversized entries without blocking on FIFOs.
- Change: enforce a finite default transport body cap, separate login caps, and a cumulative discovery cache cap before copying responses; preserve tighter explicit limits, status classification, and uncertain-write identities. Reject token read errors and zero, negative, or overflowing lifespans; calculate expiry from exchange request start.
- Change: require supported list shapes and validated complete inventory evidence; expose presence and ID checks through internal helpers without changing exported SDK list shapes merely to track decoder presence. Coordinate helper callers with their domain owners.
- Breaking: malformed inputs and responses and unsafe cache entries newly fail; IAM pointer mutation and sharing behavior change. Credential field type changes would also break SDK compilation; no CLI flag removal is required.
- Decision: use `Secret` fields for password and TOTP seeds before v1, with migration guidance for string callers.
- Decision: finite defaults (8 MiB ordinary responses, 1 MiB login responses, 32 MiB cumulative discovery retention) plus explicit, justified per-operation limits; tests pin the numbers, which are not proven API maxima.
- Decision: independently cancellable waiters that keep single-flight work, token identity, and TOTP reuse prevention; the manager revises `docs/design/pricing-discovery.md` before this slice starts.
- Failing tests first: unsafe region forms emit zero requests; mutating caller credentials cannot swap cache identities; formatting and JSON with synthetic secrets cannot disclose them; symlink/mode/FIFO/oversize cache entries are refused promptly; response and decompression caps test N and N+1 bytes; cumulative retention rejects overflow before allocation; login read errors and lifespan boundaries fail without immediate repeated TOTP login.
- Failing tests first: missing/null/wrong list envelopes, repeated page identity, duplicate resource IDs, changing totals, and nonprogress cannot authorize create; explicit empty arrays remain valid. Hold auth, project, and refresh work while canceling a waiter; require prompt cancellation, no extra request or cache mutation, and unaffected holder completion if that option is approved.
- Checks: `go test -count=1 ./internal/core ./internal/endpoints ./internal/transport ./internal/tokencache ./internal/iamuser ./internal/discovery`; `go vet ./internal/core ./internal/endpoints ./internal/transport ./internal/tokencache ./internal/iamuser ./internal/discovery`; `make check`.

## CLI privacy and consent

- [x] Risk: high security and accidental deletion.
- Owned paths: `internal/cli/`; `cmd/vngcloud/`.
- Closes: value-bearing query diagnostics at `internal/cli/errors.go:61`; registry follow-up/cleanup leaks and unproven cleanup ownership at `internal/cli/svc_containerregistry.go:105`, `internal/cli/svc_containerregistry.go:133`, and `internal/cli/svc_containerregistry.go:159`; substituted IAM cleanup targets at `internal/cli/svc_iam.go:249`; null ACL priority at `internal/cli/svc_network_acl.go:45`.
- Closes: lost read-only latch at `internal/cli/configfile.go:378`; canceled configuration writes at `internal/cli/configure.go:102`; quota success hidden by query failure at `internal/cli/svc_monitor.go:411`; heterogeneous row corruption at `internal/cli/render.go:98`; ambiguity classification mismatch at `internal/cli/errors.go:539` and `internal/cli/errors_exit.go:121`.
- Closes: empty runtime flag help at `internal/cli/flags.go:198`, pricing notes omitted at `internal/cli/svc_pricing.go:18`, and historical JSON-only lists at `internal/cli/svc_dns.go:29` and monitor location inputs.
- Change: fixed query diagnostics without value-bearing causes; keep safe error identities and write-success warnings in every format. Carry issued-secret withholding through lookup and cleanup; preserve the original attributable create ID and save uncertain partial secrets without deleting a name-matched candidate.
- Change: reject absent/null ACL priority while accepting explicit zero; serialize config read/modify/write with a cross-process lock and current-latch checks, propagate cancellation through locks/input, and check before atomic publication. Build one ordered column set and resolve each row by key; classify project ambiguity as `InvalidConfig` with exit 2.
- Change: attach required/default/unit/enum/safety metadata to runtime help and generated references; expose repeatable DNS VPC and monitor location flags while retaining JSON input; adapt approved paging, encryption, and quote-output contracts without editing domain files.
- Open: runtime help shows the generated reference's operation notes and per-flag type and required markers; per-flag default, unit, and enum metadata needs authored contracts and remains open.
- Breaking: CLI diagnostics, error codes, affected table/text layouts, and invalid-input behavior change; approved SDK contract changes can also change CLI JSON/query keys. Repeatable list flags and help are additive.
- Decision: refuse automatic registry deletion when ownership is uncertain; remove the JSON-only list exceptions and add flags instead.
- Failing tests first: synthetic credentials never reach stderr in JSON/table/text or retained errors; successful quota orders retain success warnings after query failure; missing/null ACL priority sends nothing but explicit zero works; substituted IAM IDs and recreated registry names never become cleanup targets.
- Failing tests first: deterministic concurrent config edits cannot clear a completed read-only latch; canceled lock waits and piped input do not write; heterogeneous rows preserve all keyed values; project ambiguity class and exit agree; runtime help includes required fields, units, defaults, priority-zero meaning, consent, and pricing resource shape; repeatable flags preserve precedence.
- Checks: `go test -count=1 ./internal/cli ./cmd/vngcloud`; `go vet ./internal/cli ./cmd/vngcloud`; `make check`. Documentation and evidence regenerates references from these sources.

## Developer credential and live-write safety

- [x] Risk: high local credential disclosure and unintended cloud mutations.
- Owned paths: `scripts/`; `.github/workflows/`; `Makefile`; `livetest/`; a new test-only Python dependency lock file at `requirements-test.txt`.
- Closes: unauthenticated local helper at `scripts/browser-creds.py:119`; broad live-write selection at `Makefile:55` and shared-only gates at `livetest/live_write_test.go:11059`; tool-token argv leakage at `scripts/tools-outdated.sh:23`.
- Closes: missing script/offline safety suites at `.github/workflows/ci.yml:54` and `livetest/live_write_iam_user_mfa_test.go:1`; swallowed producer failures at `scripts/tools-outdated.sh:63` and `scripts/check-lengths.sh:26`; undeclared test dependency at `scripts/test-ci-workflows.py:59`; freshness blocking at `scripts/tools-outdated.sh:65`.
- Change: require a private per-run capability for credential and TOTP endpoints and pass it privately to the browser consumer; validate it before consuming one-shot state. Never place the capability in URLs, logs, or process arguments. Move curl authentication to protected stdin/descriptor input and disable tracing before token expansion.
- Change: anchor the billing target to its intended test, require operation-specific gates for every live mutation, and make offline consent/cleanup tests runnable by CI without reading credentials or executing API calls. Add `make scripts-test` to CI, explicitly check producer exit status, pin PyYAML for tests, and require Python with `tomllib` support.
- Breaking: no public SDK/CLI break; developer helper protocol, environment gates, and test setup change.
- Decision: keep blocking on every outdated pin, the owner's policy in `instructions/security.md`; this slice covers only the credential and live-write safety items.
- Failing tests first: missing/wrong/reused capabilities never return synthetic credentials or consume a valid first request; authenticated tool stubs see no token in argv/traces; billing selection cannot run a storage/key/policy mutation; shared-only gates refuse each legacy live write; offline harness runs with no environment/profile access; failed inventories cannot report success; a clean isolated Python setup runs workflow checks.
- Checks: `make scripts-test`; `make ci-workflows-check`; `python3 scripts/test-tools-outdated.py`; `actionlint .github/workflows/ci.yml .github/workflows/wiki.yml .github/workflows/tools.yml .github/workflows/codeql.yml`; `go test -tags live -run '^$' ./livetest/`; `go test -tags livewrite -run '^$' ./livetest/`; `go vet -tags live ./...`; `go vet -tags livewrite ./...`; `make check`. Never run `make live` or `make live-write` as acceptance checks.

## Compute and network guards

- [x] Risk: high credential disclosure, destructive authorization, and stale replacement writes.
- Owned paths: `compute/` except `resize_server.go` and `resize_server*_test.go`; `network/`; `portal/`; `project/`; `internal/floatingip/`.
- Closes: response-selected endpoints at `network/network.go:676` and substring region matching at `network/models.go:530`; SSH-key/model reflection at `compute/ssh_keys_write.go:208` and `portal/portal.go:47`; route replay at `network/routes_write.go:348`; ACL/subnet incomplete evidence at `network/acls_write.go:157`; substituted server state at `compute/compute.go:115`.
- Closes: late network waits at `network/waits.go:64`; truncated aggregates at `compute/compute.go:273` and network security-group aggregation; NAT cause loss at `network/nat_write.go:21`; misleading reboot recovery at `compute/waits.go:192`; server-group offset semantics at `compute/read_inputs.go:39` and `compute/compute.go:209`; malformed portal objects at `portal/portal.go:192`.
- Change: use fixed regional credential origins; reject token reflection and missing/mismatched server identities; send route replacements once and reconcile by read; validate every ACL page and exact final inventory before deletion confirmation or subnet guards. Walk all aggregate parent pages, reject malformed portal shapes, propagate real wait deadlines, and preserve safe NAT causes alongside settlement identity.
- Breaking: malformed reads newly fail; server-group inputs and CLI flags change if the rename is approved; other signatures remain stable.
- Decision: rename server-group `Page`/`Size` to `Offset`/`Limit` and migrate CLI flags; the manager replaces route replay claims with the send-once rule in the design before this slice starts.
- Failing tests first: foreign region dashboards receive no credential; reflected SSH-key IDs do not enter errors; mismatched/missing server ID sends no stop/delete; repeated ACL page cannot confirm absence after a failed DELETE; route 503/401/307/308 produces one dispatch; multipage aggregates return all rows or fail; canceled/late waits fail; NAT errors preserve safe context identity; portal arrays/scalars/null fail; server-group offset 2 and limit 100 produce the intended query.
- Checks: `go test -count=1 ./compute ./network ./portal ./project ./internal/floatingip`; `go vet ./compute ./network ./portal ./project ./internal/floatingip`; `make check`.

## Storage and volume guards

- [x] Risk: high data loss and issued-secret disclosure.
- Owned paths: `storage/` except `bucket_encryption.go` and `bucket_encryption*_test.go`; `volume/` except `resize_volume.go` and `resize_volume*_test.go`.
- Closes: replayed bucket deletion at `storage/buckets_delete.go:82`; ambiguous project bucket lists at `storage/project_wait.go:68`; reflected issued secrets at `storage/s3_keys.go:154`; missing volume state at `volume/volume.go:117` and `volume/delete_volume.go:54`.
- Closes: truncated snapshot aggregates at `volume/volume.go:359`; late waits at `storage/waits.go:41` and `volume/waits.go:58`; canceled region-discovery waiters at `storage/discovery.go:139`; unsafe cleanup hint at `storage/s3_keys.go:212`; unvalidated path IDs at `volume/volume.go:143` and `volume/volume.go:212`.
- Change: send bucket deletion once, report uncertainty, and require writers to stop before deletion because the service does not enforce emptiness. Require strict explicit complete project-bucket lists; preserve the separately evidenced regional project-list exception. Refuse reflected secrets and unknown/mismatched volume attachment state before destructive dispatch; complete both levels of snapshot pagination and validate path IDs before auth/project lookup.
- Change: use child deadlines for waits, cancellable region discovery, and baseline/ownership reconciliation hints instead of deleting unfamiliar keys.
- Breaking: no signatures or flags in this slice; malformed evidence newly fails and aggregates can issue more reads.
- Decision: send-once bucket deletion with a writer-quiescence rule; the manager revises `docs/design/storage-api.md` before this slice starts.
- Failing tests first: one emptiness read permits only one DELETE under retry/redirect/auth failures; missing/conflicting lists or duplicate keys send no project DELETE; generated secret reflected in ordinary fields never escapes; null/mismatched volume detail sends no DELETE; both pagination levels complete or error; stalled and late waits fail; canceled discovery waiters return independently; `..` IDs fail before any request; cleanup hints require attribution.
- Checks: `go test -count=1 ./storage ./volume`; `go vet ./storage ./volume`; `make check`.

## IAM, registry, DNS, and tagging evidence

- [x] Risk: high secret disclosure, wrong-resource cleanup, and destructive replacement.
- Owned paths: `iam/`; `containerregistry/`; `dns/`; `tagging/`.
- Closes: dependent-read leaks at `iam/service_accounts_write.go:96`, `iam/service_accounts.go:77`, and registry follow-up paths in `containerregistry/users_write.go`; substituted identities at `iam/service_accounts_write.go:101`, `iam/guard.go:459`, and `iam/policies.go:114`; unproven registry ownership at `containerregistry/users_write.go:321`.
- Closes: null tag/DNS association snapshots at `tagging/tagging.go:367` and `dns/zones_write.go:183`; lost uncertain outcomes at `tagging/tagging.go:395` and `iam/service_accounts_write.go:181`; missing-ID secret loss at `iam/service_accounts_write.go:91`; false registry absence at `containerregistry/repositories_write.go:118` and `containerregistry/users_write.go:423`.
- Closes: incomplete public paging at `dns/dns.go:145`, `containerregistry/containerregistry.go:38`, and `iam/policies.go:41`; late waits at `dns/waits.go:97` and `containerregistry/waits.go:53`; TXT comparison at `dns/records_write.go:359`.
- Change: carry operation secrets through follow-up reads, captures, errors, and cleanup; require matching IDs for all mutation evidence. Return missing-ID secrets as uncertain partial output, with no cleanup authority. Treat exact-name registry matches as candidates, never proof of creation ownership, and remove unsafe ambiguous-create deletion hints.
- Change: require every preserved replacement field explicitly, distinguish absence from an empty list, retain safe operation-level outcomes, validate registry list metadata, and bound waits. Compare TXT payloads exactly; normalize only DNS fields with evidenced case-insensitive semantics.
- Breaking: malformed evidence newly fails; partial output/error classifications change; pagination output shapes and additive flags can break SDK unkeyed literals and CLI queries.
- Decision: no automatic cleanup without attributable identity; add DNS and registry paging inputs only for established server parameters, otherwise return an explicit incomplete result; preserve IAM metadata instead of guessing continuation.
- Failing tests first: reflected issued secrets cannot reach follow-up models/errors/captures; swapped IAM IDs send no mutation or cleanup; a concurrently recreated registry name is never deletion authority; missing-ID create keeps the secret once; absent/null/conflicting replacement fields send no PUT; malformed zero-total registry lists cannot prove NotFound; accepted PATCH confirmation failure keeps partial output and settlement precedence; TXT `old` versus `OLD` never confirms; paging can complete and canceled/late waits fail.
- Checks: `go test -count=1 ./iam ./containerregistry ./dns ./tagging`; `go vet ./iam ./containerregistry ./dns ./tagging`; `make check`.

## Monitor orders and privacy

- [x] Risk: high secret disclosure and duplicate or unauthorized paid orders.
- Owned paths: `monitor/`.
- Closes: request credential reflection at `monitor/channels_write.go:87` and `monitor/writes.go:197`; replayed log orders at `monitor/logprojects_write.go:142`; negative create quotes at `monitor/logprojects_write.go:137` and `monitor/logprojects.go:548`; incomplete duplicate searches at `monitor/logprojects_write.go:220` and `monitor/logalarms_body.go:408`.
- Closes: late waits at `monitor/waits.go:35`; cancellation-blind check locks at `monitor/toggle.go:129` and `monitor/update.go:109`; DNS-owned monitor settlement identities at `monitor/quotas_write.go:125` through Shared settlement names.
- Change: register all channel/check secrets for transport withholding, sanitize codes as well as messages, send paid orders once, reject nonfinite/negative create quotes, and require complete name searches. Use deadline-bound waits and a context-aware per-check gate so unrelated checks do not block each other.
- Breaking: no SDK shape or CLI flag removal; unsafe responses and incomplete searches newly fail; settlement names remain compatible through aliases.
- Decision: fail closed; name-filter matching evidence stays open.
- Failing tests first: header echoes in error code/message/causes are withheld; 307/308/401/503 cause one order dispatch; negative/nonfinite create price cannot pass a zero cap while valid signed resize credits remain valid; a page-two duplicate or exhausted search budget blocks create; canceled same-check waiters do not dispatch and unrelated IDs progress; late confirmation cannot succeed.
- Checks: `go test -count=1 ./monitor`; `go vet ./monitor`; `make check`.

## Load balancer and CDN write state

- [x] Risk: high stale-list data loss and wrong-resource writes.
- Owned paths: `loadbalancer/`; `globalloadbalancer/`; `cdn/`.
- Closes: frozen busy-resend members at `loadbalancer/pool_members_write.go:157`; missing/mismatched CDN state at `cdn/webaccelerator.go:75` and `cdn/update.go:159`; late load balancer waits at `loadbalancer/waits.go:68`; inconsistent accepted snapshots at `loadbalancer/policies_write.go:480`, `loadbalancer/pools_write.go:547`, and `loadbalancer/listeners_write.go:502`.
- Change: after an explicit busy refusal, reread and re-merge the caller's member delta with current members before another validated attempt; never resend an old replacement list after an ambiguous result. Require explicit CDN status and matching identity before toggles/updates; apply real wait deadlines and produce consistent submitted-state `NoWait` snapshots including rules, booleans, certificates, and headers.
- Breaking: no SDK shape/flag change; malformed CDN detail newly fails and corrected `NoWait` values change outputs. Keep resource identity field names unchanged.
- Decision: read and merge again only after an evidenced busy refusal, otherwise return uncertainty; shared settlement aliases follow their own slice.
- Failing tests first: member B added between busy attempts survives the second body; ambiguous failures never resend; missing CDN status cannot imply disabled and requested A cannot write returned B; stalled/late waits fail; `NoWait` returns every submitted mutable field consistently.
- Checks: `go test -count=1 ./loadbalancer ./globalloadbalancer ./cdn`; `go vet ./loadbalancer ./globalloadbalancer ./cdn`; `make check`.

## Billing and pricing response integrity

- [x] Risk: high credential disclosure and misleading financial data; medium numeric and error consistency.
- Owned paths: `billing/`; `pricing/`; `internal/billingresources/`.
- Closes: credential echoes at `billing/billing.go:129` and `internal/billingresources/resources.go:96`; unbounded budget responses and the preserving test at `billing/budgets.go:232` and `billing/billing_test.go:168`; operation redirects at `pricing/pricing.go:135` and `billing/budgets.go:232`; false zero cost at `billing/billing.go:152`.
- Closes: nonfinite balances at `billing/models.go:306`; inexact/overflowing budget amounts at `billing/models.go:69`; stale alert recipients at `billing/models.go:155`; lost observed status at `billing/billing.go:131` and `pricing/pricing.go:203`; deadline wording at `billing/history.go:20` through Core boundaries and Documentation and evidence.
- Change: validate all successful typed/raw fields against every sent/refreshed credential, bound all six budget write responses, refuse quote and budget redirects, and require the operation's success structure while preserving evidenced empty arrays and empty write responses. Apply send-once to nonreplayable budget mutations and retain uncertainty after possible dispatch.
- Change: reject nonfinite balance values; parse budget amounts exactly into bounded int64 without float rounding; decode alerts into fresh state; return sanitized `InvalidResponse` with the observed status and safe causes. Retain both cost-series representations until evidence justifies removing one.
- Breaking: no required SDK fields or CLI flags change; oversized/malformed replies newly fail; SDK error fields and CLI invalid-response classifications change.
- Decision: report the observed HTTP status with `InvalidResponse` and keep only safe causes.
- Failing tests first: budget names/resource names/raw statuses containing initial or refreshed synthetic tokens fail without disclosure; all budget responses test cap boundaries and ambiguous-write output; 307/308 never replay quotes/budgets; empty/missing/null read data fails while explicit empty lists pass; NaN/infinities, int64 overflow, and fractional precision fail; recipient decoding clears stale fields in both directions; malformed HTTP 200 keeps status 200 and `InvalidResponse`.
- Checks: `go test -count=1 ./billing ./pricing ./internal/billingresources`; `go vet ./billing ./pricing ./internal/billingresources`; `make check`.

## SDK input and price contracts

- [x] Risk: medium accidental encryption disablement and misleading quote units; public compatibility changes.
- Owned paths: `storage/bucket_encryption.go`; `storage/bucket_encryption*_test.go`; `volume/resize_volume.go`; `volume/resize_volume*_test.go`; `compute/resize_server.go`; `compute/resize_server*_test.go`.
- Closes: omission disables encryption at `storage/bucket_encryption.go:66`; signed remaining-term adjustment called monthly price at `volume/resize_volume.go:96` and `compute/resize_server.go:81`. CLI adapter duplication at `internal/cli/svc_storage.go:151` is closed by its CLI owner.
- Change: keep `BucketName` and add a required `Enabled *bool` for encryption; rename resize `MonthlyPrice` to `QuotedAdjustment` in compute and volume, preserving signed values and their actual remaining-term meaning. Do not reinterpret a credit estimate as a confirmed refund.
- Breaking: the encryption SDK field type, resize output JSON, and resize CLI query keys change; encryption flags and CLI input JSON keys stay stable.
- Decision: clean pre-v1 rename with migration notes; the manager revises the accepted exceptions in their designs before this slice starts.
- Failing tests first: omitted/null encryption setting sends nothing; explicit false disables and true enables; all resize success, `NoWait`, partial-output, and credit paths expose the signed adjustment consistently; compile representative SDK callers against the new fields and test CLI JSON/query migration through the CLI owner.
- Checks: `go test -count=1 ./storage ./volume ./compute`; `go vet ./storage ./volume ./compute`; `make check` after coordination with the domain and CLI owners.

## Shared settlement names

- [ ] Risk: low simplification with error-identity compatibility risk.
- Owned paths: `internal/core/errors.go`; `vngcloud.go`; new `settlement_test.go` at the repository root.
- Closes: monitor importing DNS solely for settlement identity at `monitor/quotas_write.go:125`, and common handlers accumulating service-specific identities as described by the load balancer/CDN/monitor report.
- Change: define `ErrNotSettled` and `ErrFailed` in shared core and expose root names; domain owners replace their existing definitions with aliases and the CLI owner uses the common identities. Preserve existing service exports and keep uncertainty classification ahead of nested causes.
- Breaking: avoidable if every existing service sentinel becomes an alias in the same integrated change; do not remove public names or silently change equality before all consumers migrate.
- Decision: defer shared settlement aliases until the security slices land, then adopt them only if every affected package migrates in one change.
- Failing tests first: `errors.Is` and direct equality preserve each old service export; monitor callers need no DNS import to inspect an unsettled result; cancellation plus settlement retains CLI uncertainty precedence. Domain/CLI owners add their alias and exit tests within their owned paths.
- Checks: `go test -count=1 . ./internal/core ./dns ./monitor ./loadbalancer ./cdn ./internal/cli`; `go vet . ./internal/core`; `make check` after atomic alias integration.

## Example checker integrity

- [x] Risk: medium false verification; tooling only.
- Owned paths: `examples/basic/check.go`; new `examples/basic/check_test.go`.
- Closes: decode-error false success at `examples/basic/check.go:96` and the SDK-only artifact flag that is never populated.
- Change: walk both artifact trees recursively and fail a resource when its SDK artifact records any error, when either artifact is not valid JSON, or when a raw capture has no SDK artifact. Report SDK-only artifacts without failing, because many SDK operations have no one-to-one raw capture. Leave key and row comparison as it is; a broader comparison produced endless false failures. Do not read existing example output or run the basic example.
- Breaking: no public SDK/CLI break; false-positive smoke checks become failures.
- Manager decision: narrowed scope; use synthetic temporary artifacts only.
- Failing tests first: successful raw capture plus SDK decode error fails; missing SDK artifact fails; malformed JSON in either artifact fails; a nested SDK artifact with an error fails; an SDK-only artifact is reported and passes; matched successful artifacts with explicit empty lists still pass.
- Checks: `go test -count=1 ./examples/basic`; `go vet ./examples/basic`; `make check` in the future authorized implementation workspace, without running the example or inspecting `examples/basic/output/`.

## Documentation and evidence

- [ ] Risk: low documentation and release consistency; evidence gates remain open.
- Owned paths: `docs/`; `instructions/security.md`; `CONTRIBUTING.md`; `RELEASE_NOTES.md`; `testdata/`. Own only relevant changes within these directories, never unrelated document cleanup.
- Closes: nonexistent config fields at `docs/wiki/Configuration.md:276` and `docs/wiki/Configuration.md:281`; unsafe repeated-write recovery at `docs/wiki/Compute-Servers.md:422`; stale bucket refusal guidance at `docs/wiki/Storage.md:163`; console-only private DNS guidance at `docs/wiki/DNS.md:69`; negative downsize cap example at `docs/wiki/LoadBalancer.md:141`.
- Closes: misleading 401/retry guidance at `docs/wiki/Authentication.md:27` and `docs/wiki/Errors.md:65`; undeclared Python setup at `CONTRIBUTING.md:7`; all-pin freshness policy at `instructions/security.md:24` if the manager approves; synthetic auth fixture evidence limitations at `testdata/auth/service-account-provenance.md:3` and `internal/core/service_account_auth_test.go:30`.
- Change: update accepted authorities before dependent contract work, including route replay, bucket deletion, shared waits, ownership, encryption, paging, JSON-only flags, and settlement names where approved. Use actual config options/accessors, read-based write reconciliation, attributed cleanup, existing private-DNS operations and consent, and nonnegative resize caps with signed-credit explanation.
- Change: document young-IAM-token reuse from `internal/core/auth.go:189` through `internal/core/auth.go:206`, distinguish retry classification from automatic replay, and state the approved history deadline contract. Document isolated Python setup and any approved scheduled-freshness policy.
- Change: coordinate matching wiki, generated references, fixtures, and breaking release notes with every affected source slice. Keep synthetic auth fixture provenance explicit; obtaining complete authorized sanitized raw responses is a separate evidence gate, not a fabricated fixture or a live-call instruction in this plan.
- Breaking: documentation itself is nonbreaking; it must name each approved SDK field, CLI input/output/query, diagnostic, or error-class migration.
- Decision: keep the provenance limitation; no new capture.
- Failing tests first: source owners add behavior regressions; documentation owner supplies matching sanitized/synthetic fixtures and checks examples against the approved surface. Do not add tests that merely assert prose text.
- Checks: `make check`; `make wiki-preview`; `go run ./cmd/vngcloud gen-docs /tmp/vngcloud-v1-generated` followed by source-to-generated comparison for all CLI reference pages; local Markdown link/anchor and length inspection. Run no live example. Repository-required `git diff --check` and `bash scripts/check-lengths.sh` belong to a later authorized Git-backed implementation workspace, not this consolidation directory.

## Dropped or deferred proposals

- Duplicate response-limit findings across core, billing, compute/network, storage, IAM/misc, and load balancer/CDN/monitor collapse into Core boundaries plus operation-specific regression cases; do not create six competing transport fixes.
- Duplicate registry SDK/CLI secret leaks and cleanup ownership reports collapse into one operation-wide privacy/attribution rule, with SDK and CLI changes assigned to their distinct owners; the name-recreation race remains a code inference, not a verified live race.
- Duplicate shared-wait and polling findings collapse into one deadline rule; accepted shared-wait behavior is a manager decision, not an unapproved bug fix. Do not add a separate billing-only timeout workaround.
- `internal/jsonresponse/strict.go:116`: defer typed-map strict-decoder work because no production security consumer was established; keep the narrower production decoder fixes above.
- `vngcloud.go:93`: defer replacing writable helper variables with forwarding functions; process-wide reassignment is caller-controlled and no concrete library failure justifies broad pre-v1 churn.
- `internal/core/client.go:90` and `docs/design/cdn.md:221`: defer credential-free public/API-key-only configuration; the restriction is accepted, and no caller need was established to justify changing validation timing during hardening.
- `billing/cost.go:45`: drop removal of flat cost series for now; lack of recorded flat-response evidence does not prove the compatibility shape is wrong. Removing exported fields needs representative evidence and a separate compatibility decision.
- `loadbalancer/loadbalancer.go:479`, `globalloadbalancer/globalloadbalancer.go:406`, and `cdn/webaccelerator_models.go:101`: drop wholesale resource-ID renaming; SDK/JSON/query breakage and model churn outweigh cosmetic uniformity.
- Correct stale citations rather than discard valid issues: TOTP seed is `internal/core/totp.go:32`, not line 40; the young-token policy is `internal/core/auth.go:189`, not `internal/transport/transport.go:334`; registry follow-up behavior is not established by the unrelated helper at `containerregistry/users_write.go:203`; registry absence validation is at `containerregistry/users_write.go:423`. Expand shortened paths to `internal/cli/errors_exit.go:121`, `loadbalancer/pools_write.go:547`, and `loadbalancer/listeners_write.go:502`.

## Open gates

- Undocumented API paging parameters, production response-size ceilings, registry creation attribution, complete sanitized auth fixtures, and actual monitor name-filter behavior stay open until evidence exists.
