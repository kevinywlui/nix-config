#!/usr/bin/env bash
dev=$(upower -e 2>/dev/null | grep -i battery | head -1)

if [ -z "$dev" ]; then
	echo '{"text":"? --%","class":"battery unknown"}'
	exit 0
fi

info=$(upower -i "$dev" 2>/dev/null)

state=$(echo "$info" | awk '/state:/{print $2}')
pct=$(echo "$info" | awk '/percentage:/{gsub(/%/,"",$2); printf "%d", $2}')
t_full_val=$(echo "$info" | awk '/time to full:/{print $4}')
t_full_unit=$(echo "$info" | awk '/time to full:/{print $5}')
t_empty_val=$(echo "$info" | awk '/time to empty:/{print $4}')
t_empty_unit=$(echo "$info" | awk '/time to empty:/{print $5}')

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

printf '{"text":"%s","class":"%s"}\n' "$text" "$class"
