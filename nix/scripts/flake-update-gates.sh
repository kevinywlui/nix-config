#!/usr/bin/env bash
# Thin SEQUENCER for /update-flake. Runs the deterministic, fail-closed gates
# end to end and hands the human a punch-list. It DECIDES NOTHING about content:
# it enumerates every changed flake.lock node, classifies each by node TYPE then
# owner, reuses flake-prescreen.sh for the deterministic gate, builds, and
# SURFACES the nvd watchlist for a human to judge (see update-flake.md Step 4/6).
# Every trust judgment still lives in flake-prescreen.sh (deterministic) or the
# human (content) — this script sequences, it is not a second security engine.
#
# Usage:
#   flake-update-gates.sh [input-name]   # update all (or one input), gate, build
#   flake-update-gates.sh --classify     # diagnostic: classify CURRENT nodes,
#                                         # no update, no build, no side effects
#
# Verdict (last line + exit code):
#   PASS        (0)   gates clean, build green, no queue, no watchlist movement
#   NEEDS-HUMAN (10)  queued inputs need source review and/or a watchlist glance
#   ABORT       (20)  build failed, or a changed node could not be classified
#
# Why enumerate by TYPE not a hardcoded tier list: this flake's lock carries
# transitively-bumped nodes the static list misses — flake-parts (hercules-ci →
# Tier 3), nixpkgs-lib (nix-community), and a `tarball`-type nixpkgs from
# releases.nixos.org (no owner/repo). A node we cannot classify is an ABORT, not
# a silent skip.

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PRESCREEN="$SCRIPT_DIR/flake-prescreen.sh"
REPO_ROOT="$(git -C "$SCRIPT_DIR" rev-parse --show-toplevel)"

# Security watchlist (same set as update-flake.md Step 6): anything handling
# untrusted input, crypto/transport, privilege, or the build/boot path.
WATCHLIST='curl|openssl|gnutls|openssh|sudo|polkit|\bpam\b|systemd|linux|glibc|\bnix\b|dbus|sops|cups|unbound|bind|\bnss\b|ca-certificates|kernel|initrd'

# --- node metadata helpers --------------------------------------------------
# Emit one TAB-separated line per locked node: name type owner repo ref rev url
nodes_tsv() {
	nix flake metadata "$REPO_ROOT" --json 2>/dev/null | jq -r '
		.locks.nodes | to_entries[] | select(.value.locked) |
		[ .key,
		  .value.locked.type,
		  (.value.locked.owner // "-"),
		  (.value.locked.repo  // "-"),
		  (.value.original.ref // "-"),
		  (.value.locked.rev   // .value.locked.narHash // "-"),
		  (.value.locked.url   // "-")
		] | @tsv'
}

# classify <type> <owner> <repo> <ref> <url>
# Prints:  TIER2 <repo> <branch-or-->  |  TIER3 <owner/repo>  |  UNCLASSIFIABLE <why>
# TIER2 means "deterministically prescreenable" (incl. nixpkgs provenance mode).
classify() {
	local type="$1" owner="$2" repo="$3" ref="$4" url="$5"
	local lowner="${owner,,}"

	if [ "$type" = "github" ]; then
		# nixpkgs (either channel) → provenance mode needs the branch (ref).
		if [ "$lowner" = "nixos" ] && [ "$repo" = "nixpkgs" ]; then
			echo "TIER2 nixos/nixpkgs $ref"
			return
		fi
		# Trusted orgs + the Mic92/sops-nix exception → Tier 2 prescreen.
		if [ "$lowner" = "nixos" ] || [ "$lowner" = "nix-community" ] ||
			{ [ "$lowner" = "mic92" ] && [ "$repo" = "sops-nix" ]; }; then
			echo "TIER2 $owner/$repo ${ref:--}"
			return
		fi
		# Any other GitHub org → Tier 3, always source-reviewed.
		echo "TIER3 $owner/$repo"
		return
	fi

	if [ "$type" = "tarball" ]; then
		# Only the official nixpkgs channel hosts are classifiable, by mapping
		# the URL path back to a channel branch and using provenance mode.
		local host path branch
		host="$(printf '%s' "$url" | sed -E 's#^https?://([^/]+)/.*#\1#')"
		path="$(printf '%s' "$url" | sed -E 's#^https?://[^/]+/##')"
		case "$host" in
		releases.nixos.org)
			# releases.nixos.org/<product>/<channel>/... → "<product>-<channel>"
			branch="$(printf '%s' "$path" | sed -E 's#^([^/]+)/([^/]+)/.*#\1-\2#')"
			;;
		channels.nixos.org)
			# channels.nixos.org/<branch>/... → "<branch>"
			branch="$(printf '%s' "$path" | sed -E 's#^([^/]+)/.*#\1#')"
			;;
		*)
			echo "UNCLASSIFIABLE tarball from untrusted host '$host'"
			return
			;;
		esac
		if [ -z "$branch" ] || [ "$branch" = "$path" ]; then
			echo "UNCLASSIFIABLE could not parse channel branch from '$url'"
			return
		fi
		echo "TIER2 nixos/nixpkgs $branch"
		return
	fi

	# path / git / indirect / anything else → fail closed.
	echo "UNCLASSIFIABLE unsupported node type '$type'"
}

# --- diagnostic: classify current nodes, no side effects --------------------
if [ "${1:-}" = "--classify" ]; then
	echo "==> Classifying CURRENT lock nodes (diagnostic — no update, no build)"
	printf '    %-16s %-8s %-26s %s\n' node type owner/repo routing
	while IFS=$'\t' read -r name type owner repo ref rev url; do
		routing="$(classify "$type" "$owner" "$repo" "$ref" "$url")"
		printf '    %-16s %-8s %-26s %s\n' \
			"$name" "$type" "$owner/$repo" "$routing"
	done < <(nodes_tsv | sort)
	exit 0
fi

# --- 1. snapshot, update, snapshot ------------------------------------------
echo "==> Snapshotting lock state"
declare -A OLD_REV
while IFS=$'\t' read -r name _ _ _ _ rev _; do
	OLD_REV["$name"]="$rev"
done < <(nodes_tsv)

echo "==> nix flake update ${1:+$1}  (flake.lock only)"
if [ -n "${1:-}" ]; then
	nix flake update --flake "$REPO_ROOT" "$1"
else
	nix flake update --flake "$REPO_ROOT"
fi

# --- 2. enumerate changed nodes, classify, gate -----------------------------
echo
echo "==> Changed nodes  (type -> owner -> routing -> gate)"
QUEUE=()      # human source-review queue: "name  owner/repo  reason"
ABORTS=()     # unclassifiable nodes
SKIPPED=0
CHANGED=0

while IFS=$'\t' read -r name type owner repo ref rev url; do
	old="${OLD_REV[$name]:-}"
	[ "$old" = "$rev" ] && continue   # unchanged
	CHANGED=$((CHANGED + 1))

	routing="$(classify "$type" "$owner" "$repo" "$ref" "$url")"
	read -r verdict a1 a2 <<<"$routing"

	case "$verdict" in
	UNCLASSIFIABLE)
		printf '    %-16s %-8s ABORT: %s\n' "$name" "$type" "${routing#UNCLASSIFIABLE }"
		ABORTS+=("$name: ${routing#UNCLASSIFIABLE }")
		;;
	TIER3)
		printf '    %-16s %-8s %-26s T3  -> source review (always)\n' \
			"$name" "$type" "$a1"
		QUEUE+=("$name  $a1  Tier-3 (low-visibility repo), full source review")
		;;
	TIER2)
		# a1 = owner/repo for the gh API, a2 = branch ("-" or empty if none)
		local_repo="$a1"
		branch="$a2"
		[ "$branch" = "-" ] && branch=""
		# Provenance mode only engages for nixos/nixpkgs (see flake-prescreen.sh);
		# elsewhere the branch arg is passed but harmlessly ignored, so don't
		# claim provenance for it — that gate runs the normal content scan.
		gate="prescreen"
		[ -n "$branch" ] && [ "${local_repo,,}" = "nixos/nixpkgs" ] &&
			gate="prescreen (provenance: $branch)"
		printf '    %-16s %-8s %-26s T2  -> %s\n' \
			"$name" "$type" "$local_repo" "$gate"
		# flake-prescreen.sh exits 0 = SKIP, 1 = REVIEW (fail-closed).
		if out="$("$PRESCREEN" "$local_repo" "$old" "$rev" "$branch" 2>&1)"; then
			SKIPPED=$((SKIPPED + 1))
			printf '%s\n' "$out" | sed 's/^/        /'
		else
			printf '%s\n' "$out" | sed 's/^/        /'
			QUEUE+=("$name  $local_repo  prescreen REVIEW (see output above)")
		fi
		;;
	esac
done < <(nodes_tsv)

if [ "$CHANGED" -eq 0 ]; then
	echo "    (no nodes changed)"
	echo
	echo "VERDICT: PASS"
	echo "    Nothing changed. Nothing to do."
	exit 0
fi

# Fail closed: never build an update containing a node we could not vet.
if [ "${#ABORTS[@]}" -gt 0 ]; then
	echo
	echo "VERDICT: ABORT"
	echo "    Unclassifiable changed node(s) — not building:"
	printf '      %s\n' "${ABORTS[@]}"
	echo "    flake.lock left updated but uncommitted; investigate or:  git restore flake.lock"
	exit 20
fi

# --- 3. build ---------------------------------------------------------------
echo
echo "==> nh os build"
BUILD_LOG="$(mktemp)"
trap 'rm -f "$BUILD_LOG"' EXIT
if ! nh os build 2>&1 | tee "$BUILD_LOG"; then
	echo
	echo "VERDICT: ABORT"
	echo "    nh os build failed. flake.lock left updated but uncommitted."
	echo "    Fix the build or:  git restore flake.lock"
	exit 20
fi

# --- 4. surface the nvd watchlist (for the human, never auto-cleared) --------
echo
echo "==> nvd closure delta — security watchlist"
# nvd change lines start with [U.]/[C.]/[A.]/[R.]; keep those touching the list.
WATCH_HITS="$(grep -E '^\[[UCAR]' "$BUILD_LOG" | grep -iE "$WATCHLIST" || true)"
if [ -n "$WATCH_HITS" ]; then
	printf '%s\n' "$WATCH_HITS" | sed 's/^/    /'
	echo
	echo "    Glance before applying (update-flake.md Step 6). Drill a package with:"
	echo "      nix/scripts/flake-diff-paths.sh nixos/nixpkgs <old> <new> <pkg-path>"
else
	echo "    (no watchlist packages changed)"
fi

# --- 5. verdict -------------------------------------------------------------
echo
REASONS=()
[ "${#QUEUE[@]}" -gt 0 ] && REASONS+=("${#QUEUE[@]} input(s) need source review")
[ -n "$WATCH_HITS" ] && REASONS+=("nixpkgs watchlist moved")

if [ "${#REASONS[@]}" -eq 0 ]; then
	echo "VERDICT: PASS"
	echo "    $CHANGED node(s) changed, $SKIPPED prescreened SKIP, build green,"
	echo "    nothing queued and no watchlist movement. Commit when ready:"
	echo "      git -C $REPO_ROOT add flake.lock && git -C $REPO_ROOT commit"
	exit 0
fi

echo "VERDICT: NEEDS-HUMAN"
printf '    %s.\n' "$(IFS='; '; echo "${REASONS[*]}")"
if [ "${#QUEUE[@]}" -gt 0 ]; then
	echo "    Review queue:"
	printf '      - %s\n' "${QUEUE[@]}"
fi
echo "    Build is green; nothing committed. Clear the queue + glance the"
echo "    watchlist (Step 4/6), then commit flake.lock and run 'nh os switch'."
exit 10
