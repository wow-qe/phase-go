#!/bin/sh
# Copyright 2026 The Phase Contributors
# SPDX-License-Identifier: MIT

set -eu

if [ "$#" -ne 2 ]; then
	echo "usage: $0 <base-revision> <head-revision>" >&2
	exit 2
fi

phase_dco_base=$1
phase_dco_head=$2
phase_dco_failed=0

for phase_dco_commit in $(git rev-list --reverse "${phase_dco_base}..${phase_dco_head}"); do
	if ! git show -s --format=%B "$phase_dco_commit" |
		grep -Eq '^Signed-off-by: .+ <[^>]+>$'; then
		echo "missing Signed-off-by trailer: $phase_dco_commit" >&2
		phase_dco_failed=1
	fi
done

exit "$phase_dco_failed"
