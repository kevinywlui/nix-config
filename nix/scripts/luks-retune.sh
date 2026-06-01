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
	cryptsetup luksAddKey "$DEVICE" \
		--pbkdf argon2id \
		--pbkdf-memory "$NEW_MEMORY_KIB" \
		--iter-time "$ITER_TIME_MS"
	;;
test)
	slot=${1:-}
	[[ -n "$slot" ]] || {
		echo "usage: $0 test <slot-number>" >&2
		exit 1
	}
	echo "timing slot $slot — type your passphrase when prompted:"
	time cryptsetup open --test-passphrase --key-slot "$slot" -v "$DEVICE"
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
