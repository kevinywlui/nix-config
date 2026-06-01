#!/usr/bin/env bash
get_ws() {
	swaymsg -t get_workspaces |
		jq -c 'sort_by(.num // 9999) | map({num,name,focused,visible,urgent,output})'
}
get_ws
swaymsg -t subscribe -m '["workspace"]' | while IFS= read -r _; do
	get_ws
done
