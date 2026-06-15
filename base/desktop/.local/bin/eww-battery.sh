#!/usr/bin/env bash
dev=$(upower -e 2>/dev/null | grep -i battery | head -1)

if [ -z "$dev" ]; then
	echo '{"text":"󰂑 --%","class":"battery unknown","tooltip":"No battery detected"}'
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
bat_sysfs=$(find /sys/class/power_supply -maxdepth 1 -name 'BAT*' 2>/dev/null | sort | head -1)
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

# --- Nerd Font (Material Design) battery glyphs ---
# All confirmed present in MesloLGS Nerd Font. The discharge ramp is a 10-step
# set indexed by pct/10; charging/full/alert/unknown get dedicated glyphs.
# Codepoints in $'\U........' 8-hex-digit form (bash needs the full width for
# the >U+FFFF MDI range).
ramp=($'\U000f008e' $'\U000f007a' $'\U000f007b' $'\U000f007c' $'\U000f007d' \
	$'\U000f007e' $'\U000f007f' $'\U000f0080' $'\U000f0081' $'\U000f0082')
g_full=$'\U000f0079'     # battery (full)
g_alert=$'\U000f0083'    # battery-alert (critical)
g_chg=$'\U000f0084'      # battery-charging
g_chg_full=$'\U000f0085' # battery-charging-100 (full on AC)
g_unknown=$'\U000f0091'  # battery-unknown

ramp_glyph() { # arg: pct -> discharge level glyph
	local p=${1:-0}
	if [ "$p" -ge 100 ]; then
		printf '%s' "$g_full"
	else
		local idx=$((p / 10))
		[ "$idx" -gt 9 ] && idx=9
		printf '%s' "${ramp[$idx]}"
	fi
}

case "$state" in
charging)
	time_str=$(fmt_time "$t_full_val" "$t_full_unit")
	icon="$g_chg"
	# Plugged in but still nearly empty: keep the low charge visible (yellow)
	# instead of letting "charging green" mask a critical level.
	if [ "${pct:-100}" -lt 20 ]; then
		class="battery low-charging"
	else
		class="battery charging"
	fi
	;;
fully-charged)
	time_str=""
	icon="$g_chg_full"
	class="battery charging"
	;;
discharging)
	time_str=$(fmt_time "$t_empty_val" "$t_empty_unit")
	if [ "${pct:-100}" -lt 15 ]; then
		icon="$g_alert"
		class="battery critical"
	elif [ "${pct:-100}" -lt 30 ]; then
		icon=$(ramp_glyph "${pct:-0}")
		class="battery warning"
	else
		icon=$(ramp_glyph "${pct:-0}")
		class="battery ok"
	fi
	;;
*)
	time_str=""
	icon="$g_unknown"
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
