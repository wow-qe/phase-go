#!/usr/bin/env bash
# Copyright 2026 The Phase Contributors
# SPDX-License-Identifier: MIT
#
# build-local-proxy.sh VERSION OUTDIR
#
# Renders the CURRENT WORKTREE as Go module-proxy artifacts for the three
# published modules at VERSION, so consumer-smoke.sh can rehearse a
# release against a file:// proxy before any tag exists. The zips follow
# module-zip layout (module@version/ prefix; nested modules excluded from
# the root zip). Bash is required (read -d '' is not POSIX).
set -euo pipefail

VERSION="${1:?usage: build-local-proxy.sh vX.Y.Z outdir}"
OUT="${2:?usage: build-local-proxy.sh vX.Y.Z outdir}"
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
TS="$(date -u +%Y-%m-%dT%H:%M:%SZ)"

emit() { # emit <module-path> <src-subdir> <exclude-globs...>
  mod="$1"; src="$2"; shift 2
  vdir="$OUT/$mod/@v"
  mkdir -p "$vdir"
  printf '%s\n' "$VERSION" > "$vdir/list"
  printf '{"Version":"%s","Time":"%s"}\n' "$VERSION" "$TS" > "$vdir/$VERSION.info"
  cp "$ROOT/$src/go.mod" "$vdir/$VERSION.mod"
  stage="$(mktemp -d)"
  mkdir -p "$stage/$mod@$VERSION"
  (cd "$ROOT/$src" && git ls-files -z .) | while IFS= read -r -d '' f; do
    case "$f" in
    x/*|examples/*) [ "$src" = "." ] && continue ;; # nested modules stay out of the root zip
    esac
    mkdir -p "$stage/$mod@$VERSION/$(dirname "$f")"
    cp "$ROOT/$src/$f" "$stage/$mod@$VERSION/$f"
  done
  (cd "$stage" && zip -q -r "$vdir/$VERSION.zip" "$mod@$VERSION")
  rm -rf "$stage"
}

emit github.com/wow-qe/phase-go .
emit github.com/wow-qe/phase-go/x/config x/config
emit github.com/wow-qe/phase-go/x/comparators x/comparators
echo "local proxy for $VERSION at: $OUT"
