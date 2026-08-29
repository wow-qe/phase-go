#!/bin/sh
# Copyright 2026 The Phase Contributors
# SPDX-License-Identifier: MIT
#
# verify-release-tag.sh TAG KEYRING
#
# Verifies that TAG carries a good OpenPGP signature from ANY key in the
# committed KEYRING file — every primary-key fingerprint present in the
# keyring is an approved release signer, which is what makes rotation
# (two keys in the file during handover) work without reordering hazards.
set -eu

TAG="${1:?usage: verify-release-tag.sh <tag> <keyring.asc>}"
KEYRING="${2:?usage: verify-release-tag.sh <tag> <keyring.asc>}"

home="$(mktemp -d)"
trap 'rm -rf "$home"' EXIT
export GNUPGHOME="$home"
gpg --quiet --import "$KEYRING"

raw="$(git verify-tag --raw "$TAG" 2>&1 || true)"
sig_fpr="$(printf '%s\n' "$raw" | awk '/VALIDSIG/{print $3; exit}')"
if [ -z "$sig_fpr" ]; then
  echo "tag $TAG has no valid signature" >&2
  exit 1
fi
for fpr in $(gpg --with-colons --list-keys | awk -F: '/^fpr/{print $10}'); do
  if [ "$fpr" = "$sig_fpr" ]; then
    echo "tag $TAG signed by approved release key $sig_fpr"
    exit 0
  fi
done
echo "tag $TAG is signed by $sig_fpr, which is not in the approved keyring" >&2
exit 1
