#!/usr/bin/env bash
# Prescreens a flake input update for changes that warrant LLM security review.
# Usage: flake-prescreen.sh <owner/repo> <old-rev> <new-rev>
# Exit 0 = SKIP, Exit 1 = REVIEW (with reasons printed to stdout)

set -euo pipefail

REPO="$1"
OLD_REV="$2"
NEW_REV="$3"

echo "--- $REPO (${OLD_REV:0:10} → ${NEW_REV:0:10}) ---"

COMPARE=$(gh api "repos/$REPO/compare/${OLD_REV}...${NEW_REV}" 2>/dev/null) ||
	{
		echo "REVIEW: gh api failed — cannot prescreen, manual review required"
		exit 1
	}

COMMITS=$(echo "$COMPARE" | jq '.ahead_by')
TOTAL=$(echo "$COMPARE" | jq '.files | length')
echo "$COMMITS commits, $TOTAL files shown (API cap: 300)"

# Sensitive filenames: top-level flake, shell scripts, build infrastructure
SENSITIVE_FILES=$(echo "$COMPARE" | jq -r '.files[].filename' |
	grep -E '(^flake\.nix$|\.sh$|/builder\.|/fetcher\.|setup-hook|pkgs/build-support/)' ||
	true)

# Suspicious patch content: added lines (+) with network/exec patterns.
# Split each file's patch into individual lines before grepping so multi-line
# diffs are checked correctly.
SUSPICIOUS_PATCHES=$(echo "$COMPARE" |
	jq -r '
      .files[] | . as $f |
      (.patch // "") | split("\n")[] |
      select(test("^\\+.*(curl |wget |base64 --?d|/dev/tcp|eval \"\\$|python[23]? -c)")) |
      "[\($f.filename)] " + .
    ' ||
	true)

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
	while IFS= read -r line; do echo "  $line"; done < <(echo "$SUSPICIOUS_PATCHES" | head -10)
fi
exit 1
