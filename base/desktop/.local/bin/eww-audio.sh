#!/usr/bin/env bash
sink=$(pactl get-default-sink 2>/dev/null || true)

case "$sink" in
bluez_output.*) profile="BT" ;;
*Samson_Go_Mic*) profile="Wire" ;;
*KTMicro* | *KT_USB*) profile="Desk" ;;
*pci-*) profile="Lap" ;;
*) profile="${sink:-?}" ;;
esac

vol_pct=$(pactl get-sink-volume @DEFAULT_SINK@ 2>/dev/null | grep -oE '[0-9]+%' | head -1)
mic_pct=$(pactl get-source-volume @DEFAULT_SOURCE@ 2>/dev/null | grep -oE '[0-9]+%' | head -1)
out_muted=$(pactl get-sink-mute @DEFAULT_SINK@ 2>/dev/null | grep -c yes || true)
mic_muted=$(pactl get-source-mute @DEFAULT_SOURCE@ 2>/dev/null | grep -c yes || true)

vol_pct="${vol_pct:---%}"
mic_pct="${mic_pct:---%}"

if [ "${out_muted:-0}" -gt 0 ]; then
	vol_icon="󰖁"
	vol_muted=true
else
	vol_icon="󰕾"
	vol_muted=false
fi

if [ "${mic_muted:-0}" -gt 0 ]; then
	mic_icon="󰍭"
	mic_muted=true
else
	mic_icon="󰍬"
	mic_muted=false
fi

jq -n \
	--arg profile "$profile" \
	--arg vol_icon "$vol_icon" \
	--arg vol_pct "$vol_pct" \
	--argjson vol_muted "$vol_muted" \
	--arg mic_icon "$mic_icon" \
	--arg mic_pct "$mic_pct" \
	--argjson mic_muted "$mic_muted" \
	'{profile: $profile, vol_icon: $vol_icon, vol_pct: $vol_pct, vol_muted: $vol_muted, mic_icon: $mic_icon, mic_pct: $mic_pct, mic_muted: $mic_muted}'
