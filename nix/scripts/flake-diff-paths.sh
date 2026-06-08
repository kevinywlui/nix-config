#!/usr/bin/env bash
# Path-scoped diff of a flake input bump over the trust-sensitive surface only.
# Usage: flake-diff-paths.sh <owner/repo> <old-rev> <new-rev> [path...]
#   FULL=1   also print full patches (-p), not just the commit list + diffstat
#
# Why this exists: GitHub's compare API caps `.files` at 300 and cannot filter
# by path, so a multi-thousand-commit nixpkgs bump is always truncated — you
# can never see every change to the paths that actually matter. A local
# `git log old..new -- <paths>` has neither limit: it gives COMPLETE coverage
# of the trust surface (fetchers, stdenv, the Nix daemon, security modules,
# crypto/transport libraries) at the cost of a one-time blobless mirror.
#
# Cost: the first run blobless-clones the repo's commit+tree history (no file
# blobs) into a reused cache; for nixpkgs that is a few hundred MB and a minute
# or two. Subsequent runs only fetch the two requested revs. Blobs are fetched
# lazily, only for the paths you actually diff — so a path-scoped log stays
# cheap even across thousands of commits.

set -euo pipefail

REPO="$1"
OLD_REV="$2"
NEW_REV="$3"
shift 3

# Default trust surface. Override by passing explicit paths. Tuned for nixpkgs;
# a path that does not exist in the repo is simply empty in the log output.
if [ "$#" -gt 0 ]; then
	PATHS=("$@")
else
	PATHS=(
		pkgs/build-support
		pkgs/stdenv
		pkgs/tools/package-management/nix
		pkgs/os-specific/linux/systemd
		nixos/modules/security
		pkgs/development/libraries/openssl
		pkgs/tools/networking/curl
		pkgs/tools/networking/openssh
	)
fi

CACHE_ROOT="${XDG_CACHE_HOME:-$HOME/.cache}/nix-config-review"
MIRROR="$CACHE_ROOT/${REPO//\//-}.git"

mkdir -p "$CACHE_ROOT"

if [ ! -d "$MIRROR" ]; then
	echo ">> first run: blobless-mirroring https://github.com/$REPO (commit+tree history only)…" >&2
	git clone --filter=blob:none --bare --no-tags "https://github.com/$REPO" "$MIRROR" >&2
fi

# Fetch the two specific revs (blobless). GitHub serves arbitrary reachable
# SHAs, so we do not need them to be ref tips.
echo ">> fetching $OLD_REV and $NEW_REV…" >&2
git -C "$MIRROR" fetch --filter=blob:none --no-tags origin "$OLD_REV" "$NEW_REV" >&2

echo "=== $REPO  ${OLD_REV:0:10}..${NEW_REV:0:10}  (trust-sensitive paths) ==="
echo "paths: ${PATHS[*]}"
echo

LOG_ARGS=(--no-color --stat)
if [ "${FULL:-0}" = "1" ]; then
	LOG_ARGS=(--no-color -p)
fi

COUNT=$(git -C "$MIRROR" rev-list --count "${OLD_REV}..${NEW_REV}" -- "${PATHS[@]}")
echo "$COUNT commit(s) touch the trust surface in this range."
echo

if [ "$COUNT" = "0" ]; then
	echo "(no changes to any trust-sensitive path — nothing to review here)"
	exit 0
fi

git -C "$MIRROR" log "${LOG_ARGS[@]}" "${OLD_REV}..${NEW_REV}" -- "${PATHS[@]}"
