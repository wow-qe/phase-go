# Releasing

Phase Go follows Semantic Versioning and uses annotated Git tags
(signed once a maintainer release key is provisioned).

This is a **multi-module repository**. Three library modules are published,
and they release in **lockstep** — the same version, tagged on the same
commit:

| Module | Tag |
|---|---|
| `github.com/wow-qe/phase-go` | `vX.Y.Z` |
| `github.com/wow-qe/phase-go/x/config` | `x/config/vX.Y.Z` |
| `github.com/wow-qe/phase-go/x/comparators` | `x/comparators/vX.Y.Z` |

The `examples/` modules are in-repo consumers only; they are never tagged
or published.

## Why lockstep is mandatory, not stylistic

The extension modules declare a real dependency on the root
(`require github.com/wow-qe/phase-go vX.Y.Z`). Their local
`replace => ../..` directives serve in-repo development ONLY — **a
downstream consumer never inherits a replace from a dependency**. If the
root version named in their `go.mod` files is not actually tagged, every
`go get` of an extension module fails. Therefore:

1. The `require` lines in `x/config/go.mod` and `x/comparators/go.mod`
   must name the version being released, **in the release commit itself**.
2. All three tags must point at that same commit.

The release workflow enforces #2 mechanically (it refuses to publish if
any lockstep tag is missing or points elsewhere) and requires annotated
tags. Signature verification is a manual runbook step until signer keys
are provisioned in CI.

## Runbook

1. Ensure `main` is green and the working tree is clean.
2. Update `CHANGELOG.md`: stamp the release heading, verify the entries
   and known-limits sections describe the code being tagged.
3. Confirm `x/config/go.mod` and `x/comparators/go.mod` `require` the
   version you are about to tag. Commit if they need bumping.
4. Run `make ci` on the supported Go versions.
5. Create the three annotated tags on the release commit:

   ```sh
   git tag -a vX.Y.Z              -m "vX.Y.Z"
   git tag -a x/config/vX.Y.Z     -m "x/config/vX.Y.Z"
   git tag -a x/comparators/vX.Y.Z -m "x/comparators/vX.Y.Z"
   ```

6. Push `main`, then all three tags together:

   ```sh
   git push origin vX.Y.Z x/config/vX.Y.Z x/comparators/vX.Y.Z
   ```

7. The release workflow validates each tag (SemVer, annotated, on `main`,
   lockstep-complete) and publishes release notes for the root tag.
8. Verify from a **clean consumer without any replace directives**:

   ```sh
   cd "$(mktemp -d)" && go mod init smoke
   go get github.com/wow-qe/phase-go@vX.Y.Z
   go get github.com/wow-qe/phase-go/x/config@vX.Y.Z
   go get github.com/wow-qe/phase-go/x/comparators@vX.Y.Z
   go build ./...
   ```

   This is the resolution test that catches a dangling root requirement —
   run it every release, not just the first.

Do not retag a published version. Correct mistakes with a new patch
release across all three modules.

## Tag immutability

Once a version is served by the Go module proxy and recorded by the
checksum database it is immutable: moving a published tag makes caches
that captured an intermediate payload fail checksum verification with a
security error. Never move a published tag; publish corrections as a new
lockstep patch release. Repository administrators must protect `v*` and
`x/*/v*` tag patterns against deletion and force-update (GitHub rulesets:
Settings → Rules → New tag ruleset → block deletions and non-fast-forward
updates), and before publishing, verify the candidate version is absent
from GitHub Releases, proxy.golang.org and sum.golang.org.

Incident record (2026-08-28): during the v0.1.0 release the three tags
were moved twice before stabilizing while pre-publication defects were
fixed. The final artifacts are consistent with sum.golang.org, and fresh
consumers resolve correctly; a cache that fetched an intermediate payload
must be cleared (`go clean -modcache`, or remove the module's entries).
The workflow and this runbook now treat any published tag as frozen.
