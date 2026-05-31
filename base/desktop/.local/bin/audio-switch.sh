#!/usr/bin/env bash
set -uo pipefail

get_sinks() { pactl list sinks short | awk '{print $2}'; }
get_sources() { pactl list sources short | awk '{print $2}' | grep -v '\.monitor$'; }
find_sink() { get_sinks | grep -Em1 "$1" || true; }
find_source() { get_sources | grep -Em1 "$1" || true; }

# Analog-only patterns to avoid picking HDMI/IEC958 outputs
laptop_sink=$(get_sinks | grep 'pci-' | grep -Ev 'hdmi|iec958|spdif' | head -1 || true)
builtin_src=$(get_sources | grep 'pci-' | grep -Ev 'hdmi|iec958|spdif' | head -1 || true)

declare -a labels=()
declare -A sink_of source_of

# 1. Bluetooth headphones (highest priority; fall back to built-in mic if no BT input)
bt_sink=$(find_sink '^bluez_output\.')
if [ -n "$bt_sink" ]; then
	bt_src=$(find_source '^bluez_input\.')
	labels+=("Bluetooth headphones")
	sink_of["Bluetooth headphones"]="$bt_sink"
	source_of["Bluetooth headphones"]="${bt_src:-$builtin_src}"
fi

# 2. Wired headphones + Samson Go Mic
samson_sink=$(find_sink 'Samson_Go_Mic')
samson_src=$(find_source 'Samson_Go_Mic')
if [ -n "$samson_sink" ] && [ -n "$samson_src" ]; then
	labels+=("Wired headphones + Samson mic")
	sink_of["Wired headphones + Samson mic"]="$samson_sink"
	source_of["Wired headphones + Samson mic"]="$samson_src"
fi

# 3. Desktop speakers (KT USB Audio) + built-in mic
kt_sink=$(find_sink 'KTMicro|KT_USB')
if [ -n "$kt_sink" ]; then
	labels+=("Desktop speakers")
	sink_of["Desktop speakers"]="$kt_sink"
	source_of["Desktop speakers"]="$builtin_src"
fi

# 3b. Unknown USB audio devices (dock DAC, USB interface, etc.)
while IFS= read -r usb_sink; do
	vendor="${usb_sink#alsa_output.usb-}"
	vendor="${vendor%%_*}"
	label="USB: ${vendor}"
	labels+=("$label")
	sink_of["$label"]="$usb_sink"
	source_of["$label"]="${builtin_src}"
done < <(get_sinks | grep -E 'alsa_output\.usb-' | grep -Ev 'Samson_Go_Mic|KTMicro|KT_USB')

# 4. Laptop speakers + built-in mic (always present after WirePlumber config)
if [ -n "$laptop_sink" ]; then
	labels+=("Laptop speakers")
	sink_of["Laptop speakers"]="$laptop_sink"
	source_of["Laptop speakers"]="$builtin_src"
fi

[ ${#labels[@]} -eq 0 ] && {
	notify-send "Audio" "No audio devices found"
	exit 1
}

chosen=$(printf '%s\n' "${labels[@]}" | rofi -dmenu -p "Audio" -i -no-custom)
[ -z "$chosen" ] && exit 0

new_sink="${sink_of[$chosen]}"
new_src="${source_of[$chosen]:-}"

pactl set-default-sink "$new_sink"
[ -n "$new_src" ] && pactl set-default-source "$new_src"

# Move all active streams to the new sink
pactl list sink-inputs short | awk '{print $1}' | xargs -r -I{} pactl move-sink-input {} "$new_sink"

notify-send "Audio" "→ $chosen" -t 2000
pkill -RTMIN+1 waybar
