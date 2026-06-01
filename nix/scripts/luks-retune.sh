#!/usr/bin/env bash
# Retune Argon2id KDF on the host's LUKS root encryption to cut unlock from
# ~5s to ~1s by reducing memory cost from 1 GiB to 256 MiB. All operations
# are ONLINE — the root filesystem stays mounted throughout.
#
# The new parameters take effect on the next boot. Use the `test` subcommand
# to verify the new slot's unlock time in the current session without rebooting.
#
# Run each subcommand IN ORDER:
#
#   sudo nix/scripts/luks-retune.sh backup <off-disk-path>
#   sudo nix/scripts/luks-retune.sh status              # note current slot numbers
#   sudo nix/scripts/luks-retune.sh add                 # adds new slot, REUSE same passphrase
#   sudo nix/scripts/luks-retune.sh status              # confirm new slot exists
#   sudo nix/scripts/luks-retune.sh test <new-slot>     # confirm new slot opens, ~1s
#   sudo nix/scripts/luks-retune.sh kill <old-slot>     # remove the slow slot
#   sudo nix/scripts/luks-retune.sh status              # confirm only new slot remains
#
# Identify slots in `status` output by the `Memory:` field: the old slot has
# ~1048576 KiB (1 GiB), the new one has 262144 KiB (256 MiB).
#
# === ROLLBACK if you can't boot afterwards ===
#
# Boot from a NixOS live USB (or any Linux with cryptsetup) and run:
#
#   sudo cryptsetup luksHeaderRestore /dev/disk/by-partlabel/disk-main-luks \
#     --header-backup-file <path-to-your-backup>.img
#
# This atomically restores the original keyslot exactly as it was before the
# retune. The backup file is what step 1 above created.
#
# Why is this not a Nix change? Argon2id parameters live in the LUKS header
# (on-disk metadata), not in disko.nix. Nix only knows "this device is LUKS";
# parameters are read from the header at unlock time.

set -euo pipefail

DEVICE=/dev/disk/by-partlabel/disk-main-luks
NEW_MEMORY_KIB=262144 # 256 MiB
ITER_TIME_MS=1000     # ~1s wall-clock per unlock

count_slots() {
	cryptsetup luksDump "$DEVICE" | grep -cE '^[[:space:]]+[0-9]+: luks2'
}

list_slots() {
	cryptsetup luksDump "$DEVICE" |
		awk '/^[[:space:]]+[0-9]+: luks2$/ { sub(/:/, "", $1); print $1 }' |
		sort
}

# Print "memory=NNNNNN KiB (NNN MiB), time-cost=N iters" for the given slot,
# parsed from luksDump. Used for both the post-add summary and the test verdict.
slot_params() {
	cryptsetup luksDump "$DEVICE" | awk -v want="$1" '
		/^[[:space:]]+[0-9]+: luks2$/ { current = $1; sub(/:/, "", current) }
		current == want && /^[[:space:]]+Memory:/ { mem = $2 }
		current == want && /^[[:space:]]+Time cost:/ { tc = $3 }
		END {
			if (mem == "") { print "(slot " want " not found)"; exit 1 }
			printf "memory=%s KiB (%.0f MiB), time-cost=%s iters\n", mem, mem/1024, tc
		}
	'
}

slot_memory_kib() {
	cryptsetup luksDump "$DEVICE" | awk -v want="$1" '
		/^[[:space:]]+[0-9]+: luks2$/ { current = $1; sub(/:/, "", current) }
		current == want && /^[[:space:]]+Memory:/ { print $2; exit }
	'
}

cmd=${1:-}
shift || true

if [[ -z "$cmd" || "$cmd" == "-h" || "$cmd" == "--help" || "$cmd" == "help" ]]; then
	sed -n '2,/^set -/p' "$0" | sed 's/^# \?//;/^set -/d'
	exit 0
fi

[[ $EUID -eq 0 ]] || {
	echo "must run as root (use sudo)" >&2
	exit 1
}
[[ -e "$DEVICE" ]] || {
	echo "$DEVICE not found" >&2
	exit 1
}

case "$cmd" in
backup)
	out=${1:-}
	[[ -n "$out" ]] || {
		echo "usage: $0 backup <off-disk-path>" >&2
		exit 1
	}
	[[ ! -e "$out" ]] || {
		echo "$out exists, refusing to overwrite" >&2
		exit 1
	}
	cryptsetup luksHeaderBackup "$DEVICE" --header-backup-file "$out"
	chmod 600 "$out"
	echo "saved $(stat -c%s "$out") bytes to $out"
	echo "KEEP THIS BACKUP OFF THIS DISK — it is the only rollback path"
	;;
status)
	cryptsetup luksDump "$DEVICE"
	;;
add)
	echo "adding new slot — you'll be prompted for your CURRENT passphrase"
	echo "first (to authorize), then twice for the new passphrase."
	echo "USE THE SAME PASSPHRASE — no reason to memorize a new one."
	echo
	before=$(list_slots)
	cryptsetup luksAddKey "$DEVICE" \
		--pbkdf argon2id \
		--pbkdf-memory "$NEW_MEMORY_KIB" \
		--iter-time "$ITER_TIME_MS"
	after=$(list_slots)
	new_slot=$(comm -13 <(echo "$before") <(echo "$after"))
	if [[ -n "$new_slot" ]]; then
		echo
		echo "new slot $new_slot: $(slot_params "$new_slot")"
		echo "time it with: $0 test $new_slot"
	fi
	;;
test)
	slot=${1:-}
	[[ -n "$slot" ]] || {
		echo "usage: $0 test <slot-number>" >&2
		exit 1
	}
	params=$(slot_params "$slot")
	mem=$(slot_memory_kib "$slot")
	[[ -n "$mem" ]] || {
		echo "slot $slot not found in $DEVICE" >&2
		exit 1
	}
	echo "slot $slot: $params"
	# Read passphrase up-front and pipe it via stdin so the timing window
	# captures only the KDF derivation, not the user's typing.
	read -rsp "passphrase (input is hidden): " pass
	echo
	start_ns=$(date +%s%N)
	printf '%s' "$pass" | cryptsetup open --test-passphrase --key-slot "$slot" --key-file=- "$DEVICE"
	rc=$?
	end_ns=$(date +%s%N)
	unset pass
	if [[ "$rc" -ne 0 ]]; then
		echo "unlock FAILED for slot $slot — check passphrase or slot number" >&2
		exit "$rc"
	fi
	elapsed_ms=$(((end_ns - start_ns) / 1000000))
	if [[ "$mem" == "$NEW_MEMORY_KIB" ]]; then
		if ((elapsed_ms >= 700 && elapsed_ms <= 1500)); then
			verdict="✓ within target (700-1500ms for ~1s KDF)"
		else
			verdict="⚠ outside target (700-1500ms for ~1s KDF)"
		fi
	else
		verdict="(no target for this memory cost — baseline measurement)"
	fi
	echo "slot $slot: unlock=${elapsed_ms}ms $verdict"
	;;
kill)
	slot=${1:-}
	[[ -n "$slot" ]] || {
		echo "usage: $0 kill <slot-number>" >&2
		exit 1
	}
	n=$(count_slots)
	[[ "$n" -ge 2 ]] || {
		echo "only $n slot(s) — killing would lock you out" >&2
		exit 1
	}
	echo "killing slot $slot — type any remaining passphrase to authorize"
	cryptsetup luksKillSlot "$DEVICE" "$slot"
	;;
restore)
	in=${1:-}
	[[ -n "$in" ]] || {
		echo "usage: $0 restore <backup-path>" >&2
		exit 1
	}
	[[ -e "$in" ]] || {
		echo "$in not found" >&2
		exit 1
	}
	echo "DESTRUCTIVE: this overwrites the current LUKS header with $in"
	echo "type 'YES' to confirm:"
	read -r confirm
	[[ "$confirm" == "YES" ]] || {
		echo "aborted"
		exit 1
	}
	cryptsetup luksHeaderRestore "$DEVICE" --header-backup-file "$in"
	echo "header restored. all slots from the backup are now active."
	;;
*)
	echo "unknown subcommand: $cmd" >&2
	echo "run '$0 help' for usage" >&2
	exit 1
	;;
esac
