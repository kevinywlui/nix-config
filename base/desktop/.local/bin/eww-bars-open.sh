#!/usr/bin/env bash
# Open eww bar+clock on every currently-active sway output.
#
# Sway runs this once at session start and again on every `swaymsg reload`.
# Hot-plug mid-session (plug/unplug without reload) is intentionally not
# handled — see commit message. Trigger a sway reload to refresh.

set -u

eww kill 2>/dev/null || true
eww daemon >/dev/null 2>&1 || true
for _ in $(seq 1 50); do
	eww ping >/dev/null 2>&1 && break
	sleep 0.1
done

swaymsg -t get_outputs |
	jq -r '.[] | select(.active and .dpms) | .name' |
	while IFS= read -r name; do
		eww open bar --id "bar:$name" --arg "monitor=$name"
		eww open clock --id "clock:$name" --arg "monitor=$name"
	done
