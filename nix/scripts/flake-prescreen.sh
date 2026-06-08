#!/usr/bin/env bash
# Prescreens a flake input update for changes that warrant LLM security review.
# Usage: flake-prescreen.sh <owner/repo> <old-rev> <new-rev> [tracked-branch]
# Exit 0 = SKIP, Exit 1 = REVIEW (with reasons printed to stdout)
#
# Design: FAIL CLOSED. This is a coarse pre-filter, NOT a complete guard — a
# regex cannot catch every hostile change. So any uncertainty (gh/jq failure,
# the 300-file API cap, a binary/omitted patch) forces REVIEW, and the match
# sets are deliberately broad: a needless REVIEW is cheap, a missed one is not.
# It only lets through changes that touch no build/fetch/exec surface at all.
#
# Exception — official nixpkgs channels (provenance mode): a normal
# nixos-unstable / nixos-25.11 advance is thousands of commits, so `.files`
# ALWAYS hits the 300-file cap and a content diff is fundamentally truncated.
# Fail-closing there emits a REVIEW no reviewer can satisfy (it trains
# rubber-stamping) and audits the wrong thing: nixpkgs' defense is its reviewer
# base + Hydra CI, not a human reading 3000 commits. So for `nixos/nixpkgs`
# WITH a tracked-branch arg, we swap truncated-content review for a COMPLETE
# provenance check — the new rev must be a true fast-forward of the old AND
# reachable from the official channel branch (i.e. a Hydra-gated commit, not a
# substituted/forked rev). The broad content scan below is skipped in this mode:
# on a diff spanning thousands of packages its patterns (fetchers, hashes,
# install phases) match constantly and are noise, not signal. Complete content
# review of the trust surface is delegated to flake-diff-paths.sh + the nvd
# closure delta at build time.

set -euo pipefail

REPO="$1"
OLD_REV="$2"
NEW_REV="$3"
BRANCH="${4:-}" # tracked channel branch (from flake.lock original.ref); enables provenance mode

# Channel-tracked repos get provenance mode instead of fail-closed truncation.
# Gated on an explicit allowlist + a branch arg so an unknown repo never
# silently bypasses the content guards.
CHANNEL_MODE=0
if [ "$REPO" = "nixos/nixpkgs" ] && [ -n "$BRANCH" ]; then
	CHANNEL_MODE=1
fi

echo "--- $REPO (${OLD_REV:0:10} → ${NEW_REV:0:10}) ---"

COMPARE=$(gh api "repos/$REPO/compare/${OLD_REV}...${NEW_REV}" 2>/dev/null) || {
	echo "REVIEW: gh api failed — cannot prescreen, manual review required"
	exit 1
}

COMMITS=$(echo "$COMPARE" | jq '.ahead_by // "?"') || {
	echo "REVIEW: could not parse compare response — manual review required"
	exit 1
}
BEHIND=$(echo "$COMPARE" | jq '.behind_by // "?"')
STATUS=$(echo "$COMPARE" | jq -r '.status // "?"')
TOTAL=$(echo "$COMPARE" | jq '.files | length') || {
	echo "REVIEW: could not parse file list — manual review required"
	exit 1
}
echo "$COMMITS commits ahead / $BEHIND behind, status=$STATUS, $TOTAL files shown (API cap: 300)"

# --- Provenance mode (official nixpkgs channel) -----------------------------
# Handled BEFORE the content scan and instead of it. The broad fail-closed
# patterns below (fetchers, hashes, install phases, `curl` in tests) are
# pervasive normal content in a diff that spans thousands of packages, so on a
# truncated 300-file nixpkgs sample they are pure noise, not signal — and the
# sample is an arbitrary slice a backdoor need not land in. So for the official
# channel we DON'T pretend to content-review the truncated diff; we assert
# COMPLETE provenance instead. Deep, complete content review of the trust
# surface is delegated to flake-diff-paths.sh (path-scoped, no 300-cap) and to
# the nvd closure delta at build time.
if [ "$CHANNEL_MODE" -eq 1 ]; then
	# (a) True fast-forward: new rev strictly descends from old, no divergence.
	if [ "$STATUS" != "ahead" ] || [ "$BEHIND" != "0" ]; then
		echo "REVIEW: not a clean fast-forward (status=$STATUS, behind_by=$BEHIND) —"
		echo "        the new rev diverges from the old; possible rebase/force-push/substitution."
		exit 1
	fi
	# (b) Reachability: new rev is an ancestor of the official channel branch
	# head, i.e. a Hydra-gated commit and not a forked/substituted SHA.
	# compare/<branch>...<new>: "identical" (new IS head) or "behind" (new is an
	# ancestor) => reachable. "ahead"/"diverged" => NOT on the channel.
	REACH=$(gh api "repos/$REPO/compare/${BRANCH}...${NEW_REV}" --jq '.status' 2>/dev/null) || {
		echo "REVIEW: could not verify reachability from branch '$BRANCH' — manual review required"
		exit 1
	}
	if [ "$REACH" != "identical" ] && [ "$REACH" != "behind" ]; then
		echo "REVIEW: new rev is NOT reachable from official branch '$BRANCH' (status=$REACH) —"
		echo "        the locked commit is not on the Hydra-gated channel; possible rev substitution."
		exit 1
	fi
	echo "SKIP — provenance verified: clean fast-forward ($COMMITS commits) and"
	echo "       reachable from official channel branch '$BRANCH' (compare=$REACH)."
	echo "       NOTE: source diff is API-truncated by design. Review the nvd closure"
	echo "       delta at build time, and run flake-diff-paths.sh for a complete,"
	echo "       path-scoped diff of the trust surface (see update-flake Step 6)."
	exit 0
fi

# --- Suspicious patch content (added lines only) ----------------------------
# Match groups (broad on purpose — fail-closed), in order: network egress;
# decoders / obfuscation; python exec & net; shell-from-string; Nix fetchers;
# content-addressing (hash/url) changes; impurity escapes; build-phase hooks
# that run code. The single-quoted fragments are REGEX literals; any $(, {, or
# quote inside them is part of the pattern.
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

if [ -n "$SUSPICIOUS_PATCHES" ]; then
	echo "REVIEW"
	echo "Suspicious patterns in added lines:"
	while IFS= read -r line; do echo "  $line"; done < <(echo "$SUSPICIOUS_PATCHES" | head -20)
	exit 1
fi

# --- Fail-closed guards (non-channel repos) ---------------------------------
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
# above so that a trivial version/option bump in a .nix file can still SKIP.)
SENSITIVE_FILES=$(echo "$COMPARE" | jq -r '.files[].filename' |
	grep -E '(\.sh$|\.bash$|\.py$|\.pl$|\.rb$|/builder\.|/fetcher\.|setup-hook|(^|/)Makefile$|pkgs/build-support/|/build-support/|\.nix\.in$)' ||
	true)

if [ -z "$SENSITIVE_FILES" ]; then
	echo "SKIP — no sensitive file changes or suspicious patterns"
	exit 0
fi

echo "REVIEW"
echo "Sensitive files changed:"
while IFS= read -r line; do echo "  $line"; done <<<"$SENSITIVE_FILES"
exit 1
