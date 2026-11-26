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

Add `.github/workflows/release.yml` for `v*` tag pushes. Gate all jobs on a
new tag (`push`, `created`, `!deleted`, and tag ref type) in
`dannyota/vngcloud` with `github.ref_protected` true. Pass the ref name via
an environment variable and validate stable SemVer (`vMAJOR.MINOR.PATCH`,
no leading zeroes) before use. Keep the manager's
[release gates][release-gates] and [tag rulesets][tag-rules] for manager-only
creation and protection from updates and deletion.

Run three jobs on GitHub-hosted runners with job-scoped permissions.

1. **Build** with `contents: read`. Check out the event commit with
   credential persistence disabled. Require an annotated tag with an SSH
   signature. Use `git verify-tag` with `gpg.minTrustLevel=fully` and an
   [allowed signers file][git-ssh] containing only the pinned manager public
   key from the reviewed workflow. Match the signed tag name to the ref name
   and the peeled commit to the event and checkout SHA. Require the commit
   to be on `master`. Build and check the version here, then upload the six
   binaries and pass the artifact ID and verified tag object ID downstream.
2. **Attest** with `id-token: write` and `attestations: write`. Download
   the six files by artifact ID from this run. Treat them as data and
   execute none. Check the expected filenames and write `SHA256SUMS` from
   the six basenames in byte-sorted order using standard `sha256sum` output.
   Pass the seven explicit paths to [`actions/attest` v4.2.2][action] at
   `1e69f48acb82d1966a394da916b4c1698aa569d6` for build provenance. Disable
   registry pushes and storage records. Upload the seven files as a new
   artifact and output its ID and the SHA-256 digest of `SHA256SUMS`.
3. **Publish** with `contents: write` after both jobs pass. Download the
   attested artifact by ID from this run. Require the exact seven files,
   match `SHA256SUMS` to the attest job's digest, and check each binary
   against the manifest. Use `gh release create --draft --verify-tag` with
   the seven files and matching release notes. Use the [release assets
   API][release-assets] to check the exact names and all seven SHA-256 digests.
   Recheck that the remote tag still points to the verified tag object before
   `gh release edit --draft=false`. Serialize by tag. Fail on any mismatch
   or existing release and leave any draft for the manager.
   [GitHub CLI release docs][gh-release].

Use only `GITHUB_TOKEN` and GitHub's short-lived OIDC identity. Expose
`GH_TOKEN` only to the release CLI steps. A single job would avoid artifact
transfers and grant both kinds of write access to the build.

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
    --source-ref "refs/tags/$tag" --deny-self-hosted-runners
done
checksum=$(sha256sum -- "$asset")
grep -Fxq -- "$checksum" SHA256SUMS
```

A failed download, provenance check, or checksum stops installation. Use
`--source-digest` when CI also pins the reviewed commit. See the
[GitHub CLI verifier reference][gh-verify].

## Before implementation

Approve the release contract and manager public key pin. Verify the tag
rulesets. Approve three narrow [allowlist][security] entries for
`actions/attest`, `actions/upload-artifact`, and `actions/download-artifact`.
Pin each `uses` to a full commit SHA with a version comment and check
current releases at landing.
Extend the reviewed workflow inventory and regressions in
`scripts/check-workflow-pins.py` and `scripts/test-ci-workflows.py`. Record
the tag-only publisher exception in the workflow credential and testing
rules. Follow [independent review and release gates][release-gates].

[ci-install]: cli.md#unattended-ci
[go-rebuild]: https://go.dev/blog/rebuild
[go-linker]: https://pkg.go.dev/cmd/link
[go-build-info]: https://go.dev/src/cmd/go/internal/load/pkg.go
[tag-rules]: https://docs.github.com/en/repositories/configuring-branches-and-merges-in-your-repository/managing-rulesets/available-rules-for-rulesets
[git-ssh]: https://raw.githubusercontent.com/git/git/master/Documentation/config/gpg.adoc
[action]: https://github.com/actions/attest/blob/1e69f48acb82d1966a394da916b4c1698aa569d6/README.md
[gh-release]: https://cli.github.com/manual/gh_release_create
[release-assets]: https://docs.github.com/en/rest/releases/assets
[gh-verify]: https://cli.github.com/manual/gh_attestation_verify
[security]: ../../instructions/security.md#tools-and-scanners
[release-gates]: ../../instructions/release.md#release-gates
