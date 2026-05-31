#!/usr/bin/env bash
# nightlight-menu.sh — left-click popup menu for the nightlight bar widget.
#
# Opens a rofi --dmenu with labeled, state-aware actions.  Only items
# relevant to the current state are shown; no hidden click-targets to
# memorise.
#
# States and their menus:
#
#   off (DAY)        Start Bedtime Now
#                    [no pause/disable — nothing is active]
#
#   on (NIGHT)       Turn Off
#                    Pause 30m
#                    Pause 1h
#                    Skip Tonight
#
#   paused           Resume Now
#                    Extend +30m
#                    Turn Off
#                    Skip Tonight
#
#   disabled         Re-enable Now
#                    Turn Off
#
# Launched detached (setsid) from the Eww widget so the bar's 1s re-render
# cannot SIGKILL it mid-selection — see nix/modules/home/services/nightlight/README.md.
#
# Requires: rofi (dmenu mode), nightlight.sh alongside this script.

set -euo pipefail

SELF_DIR="$(dirname "$(realpath "$0")")"
NIGHTLIGHT="$SELF_DIR/nightlight.sh"

state="$("$NIGHTLIGHT" status)"

# Build the menu entries based on current state.
entries=()
case "$state" in
off)
	entries+=(
		"Start Bedtime Now"
	)
	;;
on)
	entries+=(
		"Turn Off"
		"Pause 30m"
		"Pause 1h"
		"Skip Tonight"
	)
	;;
paused)
	entries+=(
		"Resume Now"
		"Extend +30m"
		"Turn Off"
		"Skip Tonight"
	)
	;;
disabled)
	entries+=(
		"Re-enable Now"
		"Turn Off"
	)
	;;
esac

# Present the menu via rofi --dmenu.
# -lines matches the item count so the popup is exactly as tall as needed.
chosen=$(printf '%s\n' "${entries[@]}" |
	rofi -dmenu \
		-lines "${#entries[@]}" \
		-p "Bedtime" \
		2>/dev/null) || exit 0

case "$chosen" in
"Start Bedtime Now")
	"$NIGHTLIGHT" on
	;;
"Turn Off")
	"$NIGHTLIGHT" off
	;;
"Pause 30m")
	"$NIGHTLIGHT" pause
	;;
"Pause 1h")
	"$NIGHTLIGHT" pause 60
	;;
"Skip Tonight")
	"$NIGHTLIGHT" disable
	;;
"Resume Now")
	"$NIGHTLIGHT" on
	;;
"Extend +30m")
	# Reuses the pause command: when state=paused it resets the 30m window.
	"$NIGHTLIGHT" pause
	;;
"Re-enable Now")
	# Re-enable then immediately activate — the user wants it on now.
	"$NIGHTLIGHT" on
	;;
esac
