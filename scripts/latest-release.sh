#!/bin/sh
# Copyright 2026 The Phase Contributors
# SPDX-License-Identifier: MIT
#
# latest-release.sh MODULE
#
# Prints the module's @latest version as the proxy resolves it: the
# highest RELEASE version, preferring stable releases over prereleases —
# the property the release-health canary depends on (the last entry of
# `go list -m -versions` can be a prerelease sorted above stable).
set -eu
MODULE="${1:?usage: latest-release.sh <module-path>}"
go list -m -f '{{.Version}}' "$MODULE@latest"
