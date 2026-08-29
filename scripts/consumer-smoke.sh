#!/bin/sh
# Copyright 2026 The Phase Contributors
# SPDX-License-Identifier: MIT
#
# consumer-smoke.sh MODE VERSION
#
# Exercises the published modules exactly as an external consumer does: a
# fresh module, no workspace, no replace directives, resolution through
# GOPROXY (the public proxy by default; a file:// proxy for pre-release
# rehearsal). Verifies the selected module graph so an extension-only
# consumer cannot silently resolve an older root release.
#
#   MODE:    root | config | comparators | all
#   VERSION: the release version under test, e.g. v0.1.2
#
# Environment: GOPROXY/GOSUMDB/GONOSUMDB may be overridden by the caller
# (a file:// proxy for unpublished versions needs GONOSUMDB for the
# rehearsed module paths). Caller GOFLAGS are preserved.
set -eu

MODE="${1:?usage: consumer-smoke.sh root|config|comparators|all vX.Y.Z}"
VERSION="${2:?usage: consumer-smoke.sh root|config|comparators|all vX.Y.Z}"
ROOT_MOD="github.com/wow-qe/phase-go"

dir="$(mktemp -d)"
trap 'rm -rf "$dir"' EXIT
cd "$dir"
export GOPATH="$dir/gopath"
go mod init consumersmoke >/dev/null
export GOFLAGS="${GOFLAGS:-} -modcacherw"

want_root="$VERSION"
case "$MODE" in
root)
  go get "$ROOT_MOD@$VERSION"
  cat > main.go <<EOF
package main

import (
	"fmt"

	phase "github.com/wow-qe/phase-go"
)

func main() { fmt.Println("root:", phase.SchemaVersion) }
EOF
  ;;
config)
  go get "$ROOT_MOD/x/config@$VERSION"
  cat > main.go <<EOF
package main

import (
	"fmt"

	config "github.com/wow-qe/phase-go/x/config"
)

func main() {
	specs, err := config.ParseCases([]byte("cases:\n  - id: smoke\n"))
	fmt.Println("config:", len(specs), err)
}
EOF
  ;;
comparators)
  go get "$ROOT_MOD/x/comparators@$VERSION"
  cat > main.go <<EOF
package main

import (
	"fmt"

	cmp "github.com/wow-qe/phase-go/x/comparators"
)

func main() { fmt.Println("comparators:", cmp.ValueMatch("smoke", 1, 1).Passed()) }
EOF
  ;;
all)
  go get "$ROOT_MOD@$VERSION" "$ROOT_MOD/x/config@$VERSION" "$ROOT_MOD/x/comparators@$VERSION"
  cat > main.go <<EOF
package main

import (
	"fmt"

	phase "github.com/wow-qe/phase-go"
	cmp "github.com/wow-qe/phase-go/x/comparators"
	config "github.com/wow-qe/phase-go/x/config"
)

func main() {
	_, _ = config.ParseCases([]byte("cases:\n  - id: smoke\n"))
	fmt.Println("all:", phase.SchemaVersion, cmp.ValueMatch("smoke", 1, 1).Passed())
}
EOF
  ;;
*)
  echo "unknown mode: $MODE" >&2
  exit 2
  ;;
esac

if grep -q '^replace' go.mod; then
  echo "FAIL: replace directive appeared in the consumer module" >&2
  exit 1
fi

# The module-graph assertion: whatever mode selected, the ROOT module the
# build actually uses must be the release under test — an extension whose
# go.mod still names an older root would surface right here.
resolved_root="$(go list -m -f '{{.Version}}' "$ROOT_MOD")"
if [ "$resolved_root" != "$want_root" ]; then
  echo "FAIL: mode=$MODE resolved root $ROOT_MOD@$resolved_root, want $want_root" >&2
  echo "      (an extension module's go.mod likely names an older root release)" >&2
  go list -m all | grep "$ROOT_MOD" >&2 || true
  exit 1
fi

go build ./...
go run .
echo "OK: mode=$MODE version=$VERSION root-resolved=$resolved_root"
