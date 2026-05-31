#!/usr/bin/env bash
get_ws() {
	swaymsg -t get_workspaces | jq -c 'map({name:.name,focused:.focused,urgent:.urgent})'
}
get_ws
swaymsg -t subscribe -m '["workspace"]' | while IFS= read -r _; do
	get_ws
done
