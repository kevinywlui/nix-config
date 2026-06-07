#!/usr/bin/env bash
# Prescreens a flake input update for changes that warrant LLM security review.
# Usage: flake-prescreen.sh <owner/repo> <old-rev> <new-rev>
# Exit 0 = SKIP, Exit 1 = REVIEW (with reasons printed to stdout)
#
# Design: FAIL CLOSED. This is a coarse pre-filter, NOT a complete guard — a
# regex cannot catch every hostile change. So any uncertainty (gh/jq failure,
# the 300-file API cap, a binary/omitted patch) forces REVIEW, and the match
# sets are deliberately broad: a needless REVIEW is cheap, a missed one is not.
# It only lets through changes that touch no build/fetch/exec surface at all.

set -euo pipefail

REPO="$1"
OLD_REV="$2"
NEW_REV="$3"

echo "--- $REPO (${OLD_REV:0:10} → ${NEW_REV:0:10}) ---"

COMPARE=$(gh api "repos/$REPO/compare/${OLD_REV}...${NEW_REV}" 2>/dev/null) || {
	echo "REVIEW: gh api failed — cannot prescreen, manual review required"
	exit 1
}

COMMITS=$(echo "$COMPARE" | jq '.ahead_by // "?"') || {
	echo "REVIEW: could not parse compare response — manual review required"
	exit 1
}
TOTAL=$(echo "$COMPARE" | jq '.files | length') || {
	echo "REVIEW: could not parse file list — manual review required"
	exit 1
}
echo "$COMMITS commits, $TOTAL files shown (API cap: 300)"

# --- Fail-closed guards -----------------------------------------------------
# The compare API caps `.files` at 300; a capped response is truncated, so we
# cannot have inspected every change.
if [ "$TOTAL" -ge 300 ]; then
	echo "REVIEW: file list hit the 300-file API cap — diff truncated, manual review required"
	exit 1
fi
# A changed file with no textual patch (binary blob, too-large, or GitHub
# omitted it) is unscreenable by content. A *removed* file legitimately has no
# patch and is not a code-injection vector, so it is exempt.
BLIND_FILES=$(echo "$COMPARE" | jq -r '
	.files[] | select((.patch == null) and (.status != "removed")) | .filename') || {
	echo "REVIEW: could not evaluate patch presence — manual review required"
	exit 1
}
if [ -n "$BLIND_FILES" ]; then
	echo "REVIEW: changed files with no inspectable patch (binary/large/omitted):"
	while IFS= read -r line; do echo "  $line"; done <<<"$BLIND_FILES"
	exit 1
fi

# --- Sensitive filenames ----------------------------------------------------
# Build/packaging code in any language, fetchers, hooks, and build-support
# infrastructure. (.nix-specific *content* risk is handled by the patch scan
# below so that a trivial version/option bump in a .nix file can still SKIP.)
SENSITIVE_FILES=$(echo "$COMPARE" | jq -r '.files[].filename' |
	grep -E '(\.sh$|\.bash$|\.py$|\.pl$|\.rb$|/builder\.|/fetcher\.|setup-hook|(^|/)Makefile$|pkgs/build-support/|/build-support/|\.nix\.in$)' ||
	true)

# --- Suspicious patch content (added lines only) ----------------------------
# Match groups (broad on purpose — fail-closed), in order: network egress;
# decoders / obfuscation; python exec & net; shell-from-string; Nix fetchers;
# content-addressing (hash/url) changes; impurity escapes; build-phase hooks
# that run code. The single-quoted fragments are REGEX literals; any $(, {, or
# quote inside them is part of the pattern, not a shell expansion.
PATTERN='^[+].*('
PATTERN+='curl |wget |\bnc \b|\bncat\b|\bsocat\b|telnet |/dev/(tcp|udp)|mkfifo'
PATTERN+='|base64 -?-?d| xxd |fromCharCode|atob\(|gpg -d| openssl enc'
PATTERN+='|python[0-9]? -c|python[0-9]? -m (http|socket|ftplib)|import socket'
# shellcheck disable=SC2016
PATTERN+='|bash -c|sh -c|eval [\"'"'"'$({]'
PATTERN+='|fetchurl|fetchzip|fetchgit|fetchFromGitHub|fetchFromGitLab|fetchTarball|fetchsvn|requireFile'
PATTERN+='|sha256 ?=|sha512 ?=|outputHash|hash ?= ?\"|srcs? ?=.*://'
PATTERN+='|builtins\.(exec|fetch|getEnv|currentTime)|__noChroot|__impure|allowSubstitutes ?= ?false|impureEnvVars'
PATTERN+='|postPatch|preBuild|postBuild|installPhase|buildPhase|unpackPhase|configurePhase|preInstall|postInstall|preConfigure|preFixup|postFixup|fixupPhase|installCheckPhase'
PATTERN+=')'

SUSPICIOUS_PATCHES=$(echo "$COMPARE" |
	jq -r --arg re "$PATTERN" '
      .files[] | . as $f |
      (.patch // "") | split("\n")[] |
      select(test($re)) |
      "[\($f.filename)] " + .
    ') || {
	echo "REVIEW: pattern scan failed — manual review required"
	exit 1
}

if [ -z "$SENSITIVE_FILES" ] && [ -z "$SUSPICIOUS_PATCHES" ]; then
	echo "SKIP — no sensitive file changes or suspicious patterns"
	exit 0
fi

echo "REVIEW"
if [ -n "$SENSITIVE_FILES" ]; then
	echo "Sensitive files changed:"
	while IFS= read -r line; do echo "  $line"; done <<<"$SENSITIVE_FILES"
fi
if [ -n "$SUSPICIOUS_PATCHES" ]; then
	echo "Suspicious patterns in added lines:"
	while IFS= read -r line; do echo "  $line"; done < <(echo "$SUSPICIOUS_PATCHES" | head -20)
fi
exit 1
