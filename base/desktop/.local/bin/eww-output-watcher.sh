#!/usr/bin/env bash
# Reconcile eww bar+clock windows against sway's active outputs.
#
# Runs once at startup, then re-runs on every sway "output" event
# (hot-plug, dock/undock, lid open/close, monitor power cycle).
# Outer loop respawns the IPC subscription so a sway reload doesn't
# orphan the watcher.
#
# Window IDs are keyed on output name (e.g. "bar:DP-2"), not GDK index
# or sway list position — those renumber on hot-plug and would orphan
# bars. Output names are stable for the lifetime of a connection.

set -u

# Names eww currently has open for us, keyed by output. Persists across
# reconciles so we know what to close when an output goes away.
declare -A opened=()

reconcile() {
	local -a want_names=()
	local name
	mapfile -t want_names < <(
		swaymsg -t get_outputs |
			jq -r '.[] | select(.active and .dpms) | .name'
	)

	declare -A want=()
	for name in "${want_names[@]}"; do
		want["$name"]=1
		# eww open is idempotent for a given --id; safe to call every reconcile.
		eww open bar --id "bar:$name" --arg "monitor=$name" >/dev/null 2>&1 || true
		eww open clock --id "clock:$name" --arg "monitor=$name" >/dev/null 2>&1 || true
		opened["$name"]=1
	done

	for name in "${!opened[@]}"; do
		if [[ -z ${want[$name]:-} ]]; then
			eww close "bar:$name" >/dev/null 2>&1 || true
			eww close "clock:$name" >/dev/null 2>&1 || true
			unset 'opened[$name]'
		fi
	done
}

cleanup() {
	local name
	for name in "${!opened[@]}"; do
		eww close "bar:$name" >/dev/null 2>&1 || true
		eww close "clock:$name" >/dev/null 2>&1 || true
	done
}
trap cleanup EXIT

while true; do
	reconcile
	# Process substitution (not a pipe) keeps `opened` updates in this shell.
	while IFS= read -r _event; do
		reconcile
	done < <(swaymsg -t subscribe -m '["output"]' 2>/dev/null)
	# Reached on sway IPC drop (reload, restart). Brief backoff then retry.
	sleep 1
done
