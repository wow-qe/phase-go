# Releasing

Phase Go follows Semantic Versioning and uses signed annotated Git tags.

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
5. Create the three signed annotated tags on the release commit:

   ```sh
   git tag -s vX.Y.Z              -m "vX.Y.Z"
   git tag -s x/config/vX.Y.Z     -m "x/config/vX.Y.Z"
   git tag -s x/comparators/vX.Y.Z -m "x/comparators/vX.Y.Z"
   ```

6. Push all three tags together:

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
