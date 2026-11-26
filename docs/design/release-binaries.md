# CLI Release Binaries

Status: Proposed.

Publish verified CLI binaries for pinned CI installs. The supported path
until this ships is [pinned `go install`][ci-install] with the Go checksum
database. Homebrew, new signing keys, installers, archives, and other
packaging are out of scope.

## Build and version

Build `./cmd/vngcloud` for Darwin, Linux, and Windows, each on `amd64` and
`arm64`. Name the raw assets `vngcloud_<tag>_<goos>_<goarch>`, with `.exe`
on Windows. Use one GitHub-hosted Linux runner to cross-build all six.

Use the exact Go pin from [`.tool-versions`](../../.tool-versions), assert
it matches `go.mod` and the running toolchain, and use `GOTOOLCHAIN=local`.
Build the clean tagged checkout with `CGO_ENABLED=0`, `GOWORK=off`,
`-mod=readonly`, `-trimpath`, and `-buildvcs=false`. Keep Go module
checksum verification enabled. Fix target settings, including `GOAMD64=v1`
and `GOARM64=v8.0`, and use no restored build cache or host-dependent flags.
The attestation records the source revision. Check reproducibility by
comparing all six SHA-256 digests across clean builds in different paths.
[Go reproducibility guidance][go-rebuild].

Use `-ldflags "-X danny.vn/vngcloud/internal/cli.releaseVersion=$tag"`
for each build. A nonempty `releaseVersion` overrides the module version in
`vngcloud version` and `vngcloud --version`. Keep the existing output format,
Go build-info fallback for `go install`, and local `(devel)` fallback.
Before attesting, run both forms on the Linux `amd64` asset and compare
stdout with `vngcloud <tag> go<Go pin> linux/amd64`. Check the Go version,
platform, and build settings of all six with `go version -m`. The native
run checks the linker value because `-trimpath` omits `-ldflags` from Go
build info. [Go linker][go-linker], [Go build-info code][go-build-info].

## Tag workflow

Add `.github/workflows/release.yml` for new `v*` tag pushes in the canonical
repository. Accept only stable `vMAJOR.MINOR.PATCH` tags. Keep the manager's
[signed-tag and exact-commit release gates][release-gates]. Use [GitHub tag
rulesets][tag-rules] for manager-only creation and a separate rule for updates
and deletion without a routine bypass. Check out the event commit
without persisted credentials. Check that the annotated tag resolves to
that commit, that the commit belongs to `master`, and that the remote tag
still points to the expected object immediately before publishing.

1. **Build and attest** with `contents: read`, `attestations: write`, and
   `id-token: write` at job scope. Write `SHA256SUMS` from exactly the six
   binary basenames in byte-sorted order, with standard `sha256sum` output.
   Pass the six binaries and `SHA256SUMS` as seven explicit `subject-path`
   entries to `actions/attest-build-provenance`. Each file is separately
   verifiable by digest. Disable registry pushes and storage records.
   Transfer the seven files via an Actions artifact from this run.
   [GitHub attestation permissions][github-attest], [action metadata][action].
2. **Publish** after the build and attestation job succeeds. Grant
   `contents: write` to this job only. Download the artifact by ID from this
   run and validate the exact inventory and SHA-256 digests. Use
   `gh release create --draft --verify-tag` for the existing tag with the
   seven files and the matching release notes. Check the uploaded names and
   digests through the [release assets API][release-assets] before publishing
   with `gh release edit --draft=false`. Serialize by tag. On failure,
   leave the draft for the manager and fail the run. Stop on an existing
   release without replacing assets. [GitHub CLI release docs][gh-release].

Use only `GITHUB_TOKEN` and GitHub's short-lived OIDC identity. Expose
`GH_TOKEN` only to the release CLI steps. The job split keeps release
write authority out of build and attestation steps. A single job would
avoid the artifact transfer and give every step both write permissions.

## Verify before installing

Use an authenticated `gh` and run in an empty directory. Replace the sample
tag and target with the reviewed release and the runner's platform.

```sh
set -eu
repo=dannyota/vngcloud
tag=v1.2.3
asset="vngcloud_${tag}_linux_amd64"
gh release download "$tag" -R "$repo" -p "$asset" -p SHA256SUMS
for file in SHA256SUMS "$asset"; do
  gh attestation verify "$file" -R "$repo" \
    --signer-workflow "$repo/.github/workflows/release.yml" \
    --source-ref "refs/tags/$tag"
done
checksum=$(sha256sum -- "$asset")
grep -Fxq -- "$checksum" SHA256SUMS
```

A failed download, provenance check, or checksum stops installation. Use
`--source-digest` when CI also pins the reviewed commit. See the
[GitHub CLI verifier reference][gh-verify].

## Before implementation

Approve the release contract and verify the tag ruleset. Approve narrow
[allowlist][security] entries for `actions/attest-build-provenance`, its
nested `actions/attest` v4 dependency, and `actions/upload-artifact` and
`actions/download-artifact` for the job handoff. Pin every `uses` to a full
commit SHA with a version comment and check current releases at landing.
Extend the reviewed workflow inventory and regressions in
`scripts/check-workflow-pins.py` and `scripts/test-ci-workflows.py`. Record
the tag-only publisher exception in the workflow credential and testing
rules. Follow [independent review and release gates][release-gates].

[ci-install]: cli.md#unattended-ci
[go-rebuild]: https://go.dev/blog/rebuild
[go-linker]: https://pkg.go.dev/cmd/link
[go-build-info]: https://go.dev/src/cmd/go/internal/load/pkg.go
[tag-rules]: https://docs.github.com/en/repositories/configuring-branches-and-merges-in-your-repository/managing-rulesets/available-rules-for-rulesets
[github-attest]: https://docs.github.com/en/actions/how-tos/secure-your-work/use-artifact-attestations/use-artifact-attestations
[action]: https://github.com/actions/attest-build-provenance/blob/4d101475d8b20a2381f78447822ac1eab6504dd8/action.yml
[gh-release]: https://cli.github.com/manual/gh_release_create
[release-assets]: https://docs.github.com/en/rest/releases/assets
[gh-verify]: https://cli.github.com/manual/gh_attestation_verify
[security]: ../../instructions/security.md#tools-and-scanners
[release-gates]: ../../instructions/release.md#release-gates
