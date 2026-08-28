# Quality and tooling

The `Makefile` is the local command contract and GitHub Actions is the hosted
enforcement layer.

## Required gates

- `gofmt -s` formatting
- `go vet` static analysis
- unit tests with shuffle enabled
- the Go race detector on supported native platforms
- module tidiness and checksum verification
- `golangci-lint` when installed
- CodeQL, Govulncheck, Gitleaks, and OpenSSF Scorecard in GitHub Actions
- Dependabot for Go modules and GitHub Actions

Run `make check` for a fast edit loop and `make ci` before pushing.

## Testing policy

Test externally observable behavior, edge cases, errors, and concurrency rather
than implementation details. Every bug fix needs a regression test unless a
maintainer documents why one is technically infeasible. Use fuzz tests for
parsers and other input-heavy boundaries once those exist. Do not chase a
coverage number by testing trivial code; introduce a blocking coverage floor
after enough domain code exists for the number to be meaningful.

## Dependency policy

Prefer the standard library. Pin all module and workflow dependencies, review
licenses and transitive dependencies, and remove unused dependencies promptly.
`go mod tidy` and `go mod verify` must remain clean.

## Deferred controls

Binary reproducibility, GoReleaser, SBOM generation, provenance attestations,
API-diff gates, documentation deployment, and branch-coverage tooling should be
added when Phase actually ships binaries or a stable public API. Enabling them
before relevant artifacts exist would provide false assurance.
