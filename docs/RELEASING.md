# Releasing

Phase Go follows Semantic Versioning and uses annotated Git tags signed
with the maintainer release key (Ed25519; committed public half at
`.github/release-signing-key.asc`). v0.1.0 and v0.1.1 predate the key
and remain annotated-unsigned, grandfathered and immutable.

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
any lockstep tag is missing or points elsewhere) and verifies each tag's
signature against the committed keyring
(`scripts/verify-release-tag.sh`); a signature is accepted when GnuPG verifies it
successfully and its primary key is present in
`.github/release-signing-key.asc` (signing subkeys map to their
primary), which is what
makes rotation work.

## Runbook

1. Ensure `main` is green and the working tree is clean.
2. Update `CHANGELOG.md`: stamp the release heading, verify the entries
   and known-limits sections describe the code being tagged. The
   changelog must be finalized BEFORE the candidate SHA is declared —
   the stamped entry is part of the release commit, never a follow-up
   (a post-declaration stamp would move the SHA or ship without its
   record).
3. Confirm `x/config/go.mod` and `x/comparators/go.mod` `require` the
   version you are about to tag. Commit if they need bumping.
4. Run `make ci` on the supported Go versions.
5. Create the three annotated tags on the release commit:

   ```sh
   git tag -s vX.Y.Z              -m "vX.Y.Z"
   git tag -s x/config/vX.Y.Z     -m "x/config/vX.Y.Z"
   git tag -s x/comparators/vX.Y.Z -m "x/comparators/vX.Y.Z"
   ```

   (repository-local `user.signingkey`/`tag.gpgsign` select the release
   key; the workflow verifies each tag's signature against the committed
   public key)

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

## Release-key management

- The release key is a dedicated Ed25519 signing-only key held by the
  maintainer; the private half and any passphrase never enter the
  repository. The public half is committed at
  `.github/release-signing-key.asc` and registered on the maintainer's
  GitHub account so tags render as Verified.
- Backup: export the secret key (`gpg --export-secret-keys`) to an
  encrypted offline location together with the revocation certificate
  GnuPG generated at key creation.
- Rotation: generate a successor key, commit its public half, sign the
  next release with it, and keep the retiring public key in the file
  until every tag it signed is superseded; the workflow accepts a
  successfully verified signature whose primary key is present in the
  committed file (`scripts/verify-release-tag.sh`); signing subkeys map
  to their primary key.
- Revocation or loss: publish the revocation certificate, rotate as
  above, and note the affected tag range in the changelog. Published
  tags are never re-signed or moved.
- Emergency release with the key unavailable: tag annotated-unsigned,
  add the version to the workflow's grandfather list in the same commit,
  and record the exception in the changelog; resume signing from the
  next release.
