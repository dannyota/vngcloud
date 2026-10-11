# Contributing

Bug reports, fixes, and new API coverage are welcome.

## Setup

Use the Go and tool versions in [`.tool-versions`](.tool-versions). Install the
pre-commit hook once per clone. It needs
[gitleaks](https://github.com/gitleaks/gitleaks) and blocks commits that contain
secrets or oversize files.

```bash
make hooks-install
```

## Making a change

1. Write a failing test first. Tests use `httptest` servers and sanitized
   fixtures in `testdata/`, never the real API.
2. Make the smallest change that passes it.
3. Update the matching page in [`docs/wiki/`](docs/wiki/Home.md). The wiki is
   published from that folder.
4. Run `make check` (tests, vet, lint, and file-length limits), following the
   [testing instructions](instructions/testing.md).

Design choices live in [`docs/design/`](docs/design/README.md) and
[`docs/adr/`](docs/adr/README.md). If code and docs disagree, fix both in the
same change.

## Live API data

Live tests and their helpers live in `livetest/`. Compile them without API calls:

```bash
go test -tags live -run '^$' ./livetest/
go test -tags livewrite -run '^$' ./livetest/
```

`make live` and `go run ./examples/basic` call the real API with your
credentials. Their output is git-ignored because it holds account data. Before a
response becomes a test fixture, replace IDs, names, emails, hostnames, IP
addresses, and tokens with placeholders such as `<project-id>` and `<ip>`.

## Commits

Use a plain imperative subject and a body explaining why, following the
[Git workflow](instructions/workflow.md#git). Do not use Conventional Commit
prefixes. By contributing, you agree that your contribution is licensed
under [Apache 2.0](LICENSE).

## Developer Certificate of Origin

Every commit must carry a `Signed-off-by` line certifying the
[Developer Certificate of Origin 1.1](DCO). Add the sign-off with:

```bash
git commit -s
```

The sign-off must use your real name and email and match the commit author:

```text
Signed-off-by: Your Name <you@example.com>
```

Commits without a sign-off are not merged. The SSH signature required by the
[Git workflow](instructions/workflow.md#git) is a separate requirement;
`git commit -s` does not replace that signature.
