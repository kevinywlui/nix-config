# Nightlight

Display colour-temperature management for the Sway/Wayland desktop. Owns **all**
gamma/redness on the screen, through a single writer, with two layers:

1. **Baseline curve** — a gentle dawn/dusk colour-temperature ramp applied
   continuously. This is the "always on" night light (think GNOME/Android *Night
   Light*): warm in the evening, neutral by day.
2. **Bedtime profile** — an aggressive red shift layered on top on a schedule,
   with manual on/off, pause, and skip-tonight controls surfaced in the bar.

If you are looking for *"why is my screen red / not red"*, it is one of these
two layers. Nothing else touches display gamma.

## Quick reference

```sh
nightlight.sh on            # bedtime red now
nightlight.sh off           # back to the baseline curve
nightlight.sh toggle        # flip on/off regardless of schedule
nightlight.sh pause [min]   # suspend bedtime, auto-resume (default PAUSE_MINUTES)
nightlight.sh disable       # skip bedtime tonight; re-enable next bedtime
nightlight.sh status        # print current state
nightlight.sh status-eww    # JSON for the bar widget
```

Controls: **left-click** the bar pill to pause/resume, **right-click** to
toggle on/off, and the keyboard chords `$mod+n` (toggle), `$mod+Shift+p`
(toggle-pause), `$mod+Shift+e` (pause 30m), `$mod+Shift+d` (skip tonight).
The pill itself carries a tooltip documenting the click bindings, so no
muscle memory is required. The rest of this README explains the design that
makes the above robust under suspend, docking, and session restarts.

---

## Why a single writer

`wl-gammarelay-rs` holds the compositor's **exclusive** `wlr-gamma-control`
lease for the whole session. Only one client may hold it at a time. The earlier
design ran `gammastep` as a daemon and killed/restarted it to change the
temperature — that races on the exclusive lease and fails silently on NixOS
(where the binary is wrapped). The current design keeps that lease in one
long-lived relay and changes gamma with cheap D-Bus `set-property` calls:

```
                         ┌────────────────────────────┐
  baseline curve  ──┐    │  wl-gammarelay-rs           │
  (nightlight-      │    │  (holds wlr-gamma-control,  │ ──▶  your displays
   curve.timer)     ├──▶ │   the single writer)        │
  bedtime profile ──┘    └────────────────────────────┘
  (nightlight.sh)            ▲ busctl set-property
                             │ Temperature / Brightness
```

`gammastep` is still installed but is **never run as a daemon**. It is used only
as a pure calculator (`gammastep -m dummy`, no Wayland connection) to compute
the current dawn/dusk temperature for the baseline curve.

---

## Components

### Scripts — `base/desktop/.local/bin/`

Stow-managed and symlinked (`mkOutOfStoreSymlink`), so the **exact same logic**
runs on non-NixOS installs. This is why the behaviour lives here and not in Nix.

| File | Role |
| --- | --- |
| `nightlight.sh` | The engine. State machine, gamma fades, schedule math, all subcommands. |

### Nix glue — `nix/modules/home/services/nightlight/default.nix`

Wires the scripts into systemd and keeps the schedule in lockstep with the timer
fire times. Defines:

| Unit | Kind | Purpose |
| --- | --- | --- |
| `wl-gammarelay-rs` | service | The single writer; runs all session. |
| `nightlight-curve` | service + timer | Recompute & apply the dawn/dusk curve every 3 min (and immediately on restart). |
| `nightlight-bedtime-on` | service + timer | Fire the bedtime wind-down at `bedtimeHour` (9pm). |
| `nightlight-bedtime-off` | service + timer | Lift the bedtime profile at `wakeHour` (7am). |

`nightlight-warn` / `nightlight-resume` are **transient** units created on demand
by `systemd-run` for the pause countdown; they are not declared in Nix.

### Widget — `base/desktop/.config/eww/{eww.yuck,eww.scss}`

The `nightlight-widget` bar pill. Polls `nightlight.sh status-eww` every 1s and
renders one of four states (the fast tick keeps the `PAUSED 28m` countdown live). Left-click runs `toggle-pause`, right-click runs
`toggle`; the pill's tooltip documents both. The pill lives inside the shared
eww config because the bar is one config; the `.nightlight-*` classes and the
`nightlight` poll are the only nightlight-specific parts.

### Compositor — `base/desktop/.config/sway/config`

- On session start: bring up `wl-gammarelay-rs`, then kick `nightlight-curve`
  once so the correct temperature is applied immediately.
- `swayidle … after-resume 'nightlight.sh --reapply'` re-pushes the current
  values after a DPMS/suspend wake (no fade, to avoid a visible flash).

### Packages — `nix/modules/nixos/profiles/desktop.nix`

`wl-gammarelay-rs` and `gammastep` (calculator only).

---

## State machine

State lives in `$XDG_RUNTIME_DIR/nightlight.state` (tmpfs — cleared on logout).
The bedtime profile has four states; the baseline curve runs whenever the state
is **not** `on`.

```
            manual toggle / 9pm schedule
   off ─────────────────────────────────────▶ on
    ▲  ◀───────────────────────────────────────┤  manual toggle / 7am schedule
    │           pause                            │
    │   on ───────────▶ paused ──(timer)──▶ on   │   paused auto-resumes after
    │                     │                       │   PAUSE_MINUTES; "extend"
    │                     └── turn off ──▶ off     │   resets that window
    │
    └── disabled ("Skip Tonight"): curve only until next bedtime, then re-enables
```

- **off** — baseline curve only (dawn/dusk ramp).
- **on** — bedtime red (`BEDTIME_KELVIN`); curve timer stopped so it can't fight.
  The curve is suppressed both ways: the timer is stopped *and* `--gamma-update`
  guards on `state == on`, so a stray tick can't override the red.
- **paused** — bedtime suspended for `PAUSE_MINUTES`, curve restored meanwhile,
  auto-resumes; the widget shows a live countdown (`PAUSED 28m`).
- **disabled** — bedtime skipped for tonight; re-enables at the next `bedtimeHour`.

The bar widget renders these as: `off`→**DAY**, `on`→**NIGHT**,
`paused`→**PAUSED 28m**, `disabled`→**OFF TONIGHT**.

---

## Configuration — single source of truth

The schedule is defined **once**, in the `let` block of `default.nix`:

```nix
bedtimeHour     = 21;  # bedtime profile activates (9pm)
wakeHour        = 7;   # bedtime profile deactivates (7am)
pauseMinutes    = 30;  # default pause length
fadeSeconds     = 5;   # gamma fade for manual toggles
windDownSeconds = 60;  # gentle fade for the scheduled 9pm activation
```

These values generate **both** the systemd timer `OnCalendar` entries **and**
`~/.config/nightlight/config` (which the scripts read), so on NixOS the timer
fire time and the script's schedule math can never drift apart. To change the
schedule, edit the `let` block and run `nh os switch`.

`nightlight.sh` also hard-codes the same schedule values as **fallbacks**, used
only on non-NixOS installs where the generated config is absent (on NixOS the
sourced config always wins). Keep the two in sync if you edit the `let` block
and care about the non-NixOS path.

The curve endpoints (`DAY_TEMP`, `NIGHT_TEMP`) and `LOCATION` are **script-only**
constants in `nightlight.sh` — intentionally *not* in the generated config.
They aren't schedule-coupled and rarely change, so there is nothing to keep in
lockstep; edit them in the script.

---

## Failure modes & design notes

- **Last-writer-wins fades.** A fade claims a token in `nightlight.ramp`; any
  older in-flight fade bails on its next step. A rapid re-click can't leave two
  fades fighting.
- **Race-free on/off.** `cmd_on` writes state *before* touching gamma, so the
  curve timer's guard (`skip when state == on`) sees the new state and can't
  override the ramp.
- **Missed schedules.** Both bedtime timers are `Persistent=true`: if the laptop
  was asleep or logged out at 9pm/7am, the timer fires on the next session start.
  `cmd_schedule_activate` only activates inside the night window, so a missed-9pm
  catch-up that fires at, say, 8am won't redden the screen in the morning.
- **Pause survives suspend.** The auto-resume timer is a monotonic transient
  unit that a long suspend can outlast; `--reapply` (run by swayidle on wake)
  re-checks `pause_until` against the wall clock and completes the resume if the
  window already elapsed, so a pause can't get stuck on forever.
- **Detached onclicks.** The eww bar re-renders every second (the `mode`/`time`
  polls) and SIGKILLs any `:onclick` child still alive. The pill's handlers run
  `nightlight.sh` under `setsid -f` so they survive that kill: `toggle`/off does
  a `write_state` followed by a `systemctl restart nightlight-curve.timer`, and a
  busy user bus can push that restart past the 1s window — a mid-flight kill
  between the two would leave `state=off` with the curve timer dead (screen stuck
  red, pill says DAY, and the poll can't repair an `off` state). Detaching closes
  that window. **Any** eww onclick added in the future must likewise `setsid -f`.
- **Relay readiness.** `wl-gammarelay-rs` is `Type=dbus` with
  `BusName=rs.wl-gammarelay`, so systemd treats it as started only once it owns
  the bus name. The curve and bedtime services `Wants`/`After` it, so a tick
  can't fire before the relay exists and silently drop its write — but `Wants`
  (not `Requires`) means a relay that can't start never *fails* a bedtime
  activation; the script no-ops gracefully against an absent relay.
- **Service environment.** The scheduled/oneshot units run outside the
  interactive shell, so they set `PATH` explicitly. `WAYLAND_DISPLAY` is
  imported into the user manager at session start (see `sway/config`); the D-Bus
  calls reach the session bus via the default `$XDG_RUNTIME_DIR/bus` socket.

---

## Recovery & known limitations

- **If gamma seems stuck or wrong**, the writer is the thing to bounce:
  `systemctl --user restart wl-gammarelay-rs` (the curve tick then re-applies on
  its next fire, or run `nightlight.sh --reapply`). There is deliberately **no**
  in-bar "relay is dead" indicator: when bedtime is on and the relay dies the
  screen snaps back to full blue-white — a louder signal than any pill — and
  when it's off there is no tint to lose, so a health probe on every poll would
  be cost without benefit for a single user.
- **Docking/undocking while bedtime is on.** The relay re-applies its current
  colour to outputs as they bind, and the curve tick re-asserts every 3 min when
  bedtime is *off*. While bedtime is *on* the curve timer is stopped, so a newly
  re-enabled output could miss the red until the next toggle/`--reapply`. Rare on
  a single-output laptop; toggle `$mod+n` twice if it happens.

---

## Non-NixOS installs

`make install` (GNU Stow) symlinks `nightlight.sh` into `~/.local/bin`. The
script then runs with its built-in defaults (the `let` values mirrored at the
top of `nightlight.sh`). What you **don't** get without Nix: the systemd timers
(schedule/curve), the relay service, and the generated config. Start
`wl-gammarelay-rs` yourself and call the script's subcommands, or port the
units to your init system.

---

## Subcommand reference

User-facing subcommands appear in the [Quick reference](#quick-reference) at
the top. Internal subcommands (called by systemd timers / swayidle, not by
hand): `--gamma-update`, `--schedule-activate`, `--schedule-deactivate`,
`--reapply`, `toggle-pause`.
