#!/usr/bin/env bash
# Sunrise/sunset tooltip for the eww clock widget.
#
# Emits {"tooltip":"↑ 05:47  ·  ↓ 20:30  ·  sunset in 6h39m"} — today's local-day
# rise/set plus a live countdown to the next horizon crossing. Pure computation
# (NOAA sunrise equation) from the coordinates below; no network, no GPS.
#
# Coordinates are duplicated from nightlight.sh:60 (LOCATION=(-l 37.32:-122.03)),
# which holds them as a gammastep `-l lat:lon` arg array rather than plain
# numbers — not reusable here without re-parsing, so a back-referenced copy is
# the lower-risk choice over coupling both scripts to a shared location file.
#
# Convention notes (cf. eww-battery.sh): argless, no `set -e`, always exit 0 with
# valid JSON, and jq owns serialization so the UTF-8 arrows can't corrupt output.

LAT=37.32
LON=-122.03 # east-positive (122.03°W => negative); see nightlight.sh:60

# python3 is the calculator. Under bare Stow on a non-NixOS box it may be absent
# (it comes from dev.nix, not core.nix), so degrade to a valid placeholder JSON.
if ! command -v python3 >/dev/null 2>&1; then
	jq -cn '{tooltip: "sun: unavailable"}'
	exit 0
fi

# python prints ONLY the tooltip text (one UTF-8 line); jq wraps it below. A
# compute failure (polar clamp, bad locale) yields empty stdout -> placeholder.
tip=$(LAT="$LAT" LON="$LON" python3 <<'PY' 2>/dev/null || true
import math, os, time

LAT = float(os.environ["LAT"])
LON = float(os.environ["LON"])
ALT = -0.833  # standard sunrise/sunset: upper limb + 34' refraction + 16' radius

jc = lambda u: u / 86400.0 + 2440587.5
uj = lambda j: (j - 2440587.5) * 86400.0


def events(now):
    """(sunrise, sunset) as unix times for the local civil day containing now.
    None for a value the sun never reaches that day (polar night / midnight sun)."""
    lt = time.localtime(now)
    # Anchor on local noon so rise/set (within ~8h of transit) stay on this day;
    # tm_isdst=-1 lets mktime resolve PST/PDT itself.
    t_noon = time.mktime((lt.tm_year, lt.tm_mon, lt.tm_mday, 12, 0, 0, 0, 0, -1))
    n = round(jc(t_noon) - 2451545.0)          # day number, J2000 epoch
    Js = n - LON / 360.0                        # mean solar time (east-positive lon)
    M = math.radians((357.5291 + 0.98560028 * Js) % 360)
    C = 1.9148 * math.sin(M) + 0.0200 * math.sin(2 * M) + 0.0003 * math.sin(3 * M)
    lam = math.radians((math.degrees(M) + C + 282.9372) % 360)
    Jt = 2451545.0 + Js + 0.0053 * math.sin(M) - 0.0069 * math.sin(2 * lam)
    sd = math.sin(lam) * math.sin(math.radians(23.4397))
    dec = math.asin(sd)
    la = math.radians(LAT)
    cw = (math.sin(math.radians(ALT)) - math.sin(la) * sd) / (math.cos(la) * math.cos(dec))
    if cw > 1:
        return None, None        # sun never rises
    if cw < -1:
        return uj(Jt), None      # sun never sets
    w = math.degrees(math.acos(cw))
    return uj(Jt - w / 360.0), uj(Jt + w / 360.0)


now = time.time()
rise, sset = events(now)
rise_tmrw, _ = events(now + 86400)

hm = lambda u: time.strftime("%H:%M", time.localtime(u)) if u else "--"

# Next horizon crossing: today's rise, today's set, or tomorrow's rise —
# whichever is the soonest still ahead of now (covers the pre-dawn case where
# today's sunrise is next, the daytime case (set), and the evening case (rise)).
cands = [(rise, "sunrise"), (sset, "sunset"), (rise_tmrw, "sunrise")]
cands = sorted((t, lbl) for t, lbl in cands if t and t > now)

parts = [f"↑ {hm(rise)}", f"↓ {hm(sset)}"]
if cands:
    t, label = cands[0]
    mins = int((t - now) // 60)
    h, m = divmod(mins, 60)
    eta = f"{h}h{m:02d}m" if h else f"{m}m"
    parts.append(f"{label} in {eta}")

print("  ·  ".join(parts))
PY
)

[ -z "$tip" ] && tip="sun: --"
jq -cn --arg tooltip "$tip" '{tooltip: $tooltip}'
