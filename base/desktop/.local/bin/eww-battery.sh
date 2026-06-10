#!/usr/bin/env bash
dev=$(upower -e 2>/dev/null | grep -i battery | head -1)

if [ -z "$dev" ]; then
	echo '{"text":"? --%","class":"battery unknown","tooltip":"No battery detected"}'
	exit 0
fi

info=$(upower -i "$dev" 2>/dev/null)

state=$(echo "$info" | awk '/state:/{print $2}')
pct=$(echo "$info" | awk '/percentage:/{gsub(/%/,"",$2); printf "%d", $2}')
# energy-rate is upower's whole-system draw in W (current_now * voltage_now).
# It is a TOTAL — no hardware attributes power to individual processes — so the
# per-process list below is CPU%, a proxy for what's spending that wattage.
rate=$(echo "$info" | awk '/energy-rate:/{print $2}')
t_full_val=$(echo "$info" | awk '/time to full:/{print $4}')
t_full_unit=$(echo "$info" | awk '/time to full:/{print $5}')
t_empty_val=$(echo "$info" | awk '/time to empty:/{print $4}')
t_empty_unit=$(echo "$info" | awk '/time to empty:/{print $5}')

# Battery name + cycle count come from sysfs (upower doesn't always expose cycles).
bat_sysfs=$(ls -d /sys/class/power_supply/BAT* 2>/dev/null | head -1)
batname=$(basename "$bat_sysfs" 2>/dev/null)
cycles=""
[ -r "$bat_sysfs/cycle_count" ] && cycles=$(cat "$bat_sysfs/cycle_count")

fmt_time() {
	local val=$1 unit=$2
	[ -z "$val" ] && {
		echo ""
		return
	}
	local mins
	case "$unit" in
	minute*) mins=$(awk "BEGIN{printf \"%.0f\", $val}") ;;
	hour*) mins=$(awk "BEGIN{printf \"%.0f\", $val * 60}") ;;
	*)
		echo ""
		return
		;;
	esac
	if [ "$mins" -ge 60 ]; then
		printf "%dh %dm" $((mins / 60)) $((mins % 60))
	else
		printf "%dm" "$mins"
	fi
}

case "$state" in
charging)
	time_str=$(fmt_time "$t_full_val" "$t_full_unit")
	icon="⚡"
	class="battery charging"
	;;
fully-charged)
	time_str=""
	icon=""
	class="battery ok"
	;;
discharging)
	time_str=$(fmt_time "$t_empty_val" "$t_empty_unit")
	icon=""
	if [ "${pct:-100}" -lt 15 ]; then
		class="battery critical"
	elif [ "${pct:-100}" -lt 30 ]; then
		class="battery warning"
	else
		class="battery ok"
	fi
	;;
*)
	time_str=""
	icon="?"
	class="battery unknown"
	;;
esac

if [ -n "$time_str" ]; then
	text="${icon} ${pct}% (${time_str})"
else
	text="${icon} ${pct}%"
fi

# --- Tooltip: mirrors nightlight/volume; surfaces detail the pill can't fit. ---
rate_fmt=""
[ -n "$rate" ] && rate_fmt=$(awk "BEGIN{printf \"%.1f\", $rate}")

tooltip="${batname:-Battery} ${pct}% · ${state}"

if [ -n "$rate_fmt" ] && [ "$rate_fmt" != "0.0" ]; then
	if [ "$state" = "charging" ]; then
		draw_line="Charging: ${rate_fmt} W"
		[ -n "$time_str" ] && draw_line="${draw_line} · full in ${time_str}"
	else
		draw_line="Draw: ${rate_fmt} W"
		[ -n "$time_str" ] && draw_line="${draw_line} · ~${time_str} left"
	fi
	tooltip="${tooltip}"$'\n'"${draw_line}"
fi

[ -n "$cycles" ] && tooltip="${tooltip}"$'\n'"Cycles: ${cycles}"

# Top processes by CPU% — proxy for power draw, not measured watts (see note above).
# Drop our own `ps` (it self-reports ~100% over its sub-second lifetime).
top_procs=$(ps -eo comm,pcpu --sort=-pcpu --no-headers 2>/dev/null |
	awk '$1!="ps"' | head -3 | awk '{printf "  %-13s %s%%\n", $1, $2}')
if [ -n "$top_procs" ]; then
	tooltip="${tooltip}"$'\n\n'"Top CPU (≈ power):"$'\n'"${top_procs%$'\n'}"
fi

jq -cn --arg text "$text" --arg class "$class" --arg tooltip "$tooltip" \
	'{text: $text, class: $class, tooltip: $tooltip}'
