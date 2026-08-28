# Contributing

Thank you for helping build Phase Go.

## Development setup

1. Install the Go version declared in `go.mod` (or a newer supported version).
2. Run `make deps` to download and verify dependencies.
3. Run `make check` before opening a pull request.
4. Run `make ci` for the same core gates used by GitHub Actions.

## Changes

- Keep changes focused and include tests for new behavior and bug fixes.
- Preserve backward compatibility unless a breaking change is explicitly
  approved and documented.
- Use standard Go conventions: `gofmt`, small packages, explicit errors,
  context propagation for blocking operations, and no hidden global state.
- Prefer the standard library. Add a dependency only when its maintenance,
  license, security history, and lifecycle cost are justified.
- Never commit credentials, tokens, private keys, or real customer data.
- Update user-facing documentation and `CHANGELOG.md` with behavior changes.

## Commit and review policy

Use clear, imperative commit subjects. Every commit must include a Developer
Certificate of Origin sign-off:

```sh
git commit -s -m "feat: describe the change"
```

Pull requests require passing CI and review by a maintainer who did not author
the change. Security-sensitive changes should receive two-person review.

By contributing, you agree to follow [CODE_OF_CONDUCT.md](CODE_OF_CONDUCT.md).
