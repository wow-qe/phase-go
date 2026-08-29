#!/bin/sh
# Copyright 2026 The Phase Contributors
# SPDX-License-Identifier: MIT
#
# verify-release-tag.sh TAG KEYRING
#
# Verifies that TAG carries a signature GnuPG itself accepts (git
# verify-tag must exit 0 — an expired, revoked or otherwise rejected
# signature fails here even when signer identity is readable) AND that
# the signature's PRIMARY key is one of the approved release keys in the
# committed KEYRING. GnuPG's VALIDSIG status line carries the primary
# fingerprint as its final field, so a signature made by a signing subkey
# maps correctly to its primary key.
set -eu

TAG="${1:?usage: verify-release-tag.sh <tag> <keyring.asc>}"
KEYRING="${2:?usage: verify-release-tag.sh <tag> <keyring.asc>}"

home="$(mktemp -d)"
trap 'rm -rf "$home"' EXIT
export GNUPGHOME="$home"
gpg --quiet --import "$KEYRING" 2>/dev/null

out="$home/verify.out"
if ! git verify-tag --raw "$TAG" >"$out" 2>&1; then
  echo "tag $TAG failed signature verification:" >&2
  cat "$out" >&2
  exit 1
fi
sig_primary="$(awk '/VALIDSIG/{print $NF; exit}' "$out")"
if [ -z "$sig_primary" ]; then
  echo "tag $TAG verified without a VALIDSIG status line:" >&2
  cat "$out" >&2
  exit 1
fi

# Approved set: the PRIMARY fingerprint of every key in the keyring (the
# fpr record immediately following each pub record; subkey fprs are not
# approval anchors).
approved="$(gpg --with-colons --list-keys | awk -F: '
  /^pub/ { expect = 1; next }
  /^fpr/ { if (expect) { print $10; expect = 0 } next }
  { expect = 0 }')"
for fpr in $approved; do
  if [ "$fpr" = "$sig_primary" ]; then
    echo "tag $TAG signed by approved release key $sig_primary"
    exit 0
  fi
done
echo "tag $TAG is signed by primary key $sig_primary, which is not in the approved keyring" >&2
exit 1
