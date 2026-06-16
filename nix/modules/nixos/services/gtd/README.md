# services/gtd — guided GTD over todo.txt

A self-hosted, mobile-friendly **Getting Things Done** web app + CLI built on the
plain todo.txt format. Runs on the **t480** only; the data file is a single
canonical copy in `/var/lib/gtd` (no syncthing replication — the phone and fw13
are *clients* of this host over the tailnet).

The application itself (Go, stdlib-only) lives at **`apps/gtd/`** and is packaged
as `pkgs.gtd` by `nix/overlays/gtd.nix`. See `apps/gtd/README.md` for the GTD
conventions, the HTTP/JSON API, and how to run the tests.

## What this module wires

- A hardened `gtd.service` running `gtd-server` as the `gtd` system user, bound
  to `127.0.0.1:${my.ports.gtd}` (default 8730), state in `/var/lib/gtd`.
- A best-effort `gtd-tailscale-serve.service` that publishes it on the tailnet
  as the **`gtd` Tailscale Service** over HTTPS (see the caveat below).
- The `gtd` CLI is on every host's PATH (added to `cliTools` in
  `profiles/core.nix`).

## Reaching it

It is published as a **Tailscale Service** (`svc:gtd`), which gives it a distinct
hostname of its own rather than living under the t480 node's name:

- **Phone / browser:** `https://gtd.<your-tailnet>.ts.net/` once the service is
  served. Add it to your home screen — it ships a PWA manifest.
- **CLI on the t480:** works out of the box (`gtd add "…"`, `gtd next`, …) against
  `http://127.0.0.1:8730`.
- **CLI on fw13:** set `GTD_ENDPOINT` to the service URL, e.g. in your shell env:
  `export GTD_ENDPOINT=https://gtd.<your-tailnet>.ts.net`.

## tailscale serve is NOT declarative

NixOS has no declarative option for `tailscale serve`, so `gtd-tailscale-serve`
runs the imperative command in a oneshot after `tailscaled`. It publishes the
loopback server as the `gtd` Tailscale Service (`--service=svc:gtd`; `--bg`
defaults to true when `--service` is set). It only succeeds once:

1. the node is logged in **and** HTTPS certs are enabled for the tailnet
   (Tailscale admin → DNS → enable MagicDNS + HTTPS Certificates), and
2. the `gtd` service exists and **this node is approved as its proxy** in the
   Tailscale admin console — a one-time setup the oneshot cannot do for you.

If it shows failed in `systemctl status gtd-tailscale-serve`, complete the steps
above and re-run `nh os switch`, or set `services.gtd.tailscaleServe = false` and
run once by hand (this is the exact command the unit runs, minus `sudo`):

```
sudo tailscale serve --service=svc:gtd http://127.0.0.1:8730
```

(The exact flags vary across tailscale versions; adjust if your CLI differs.
`tailscale serve status` shows the active config.)

## Data & safety

`/var/lib/gtd` holds `todo.txt` (active), `done.txt`, `someday.txt`,
`reference.txt`, and `backups/` (the server snapshots the prior file before each
write and keeps the last 50 — your undo). It is plaintext; rely on the host's
disk encryption for at-rest protection. Auth boundary is **tailnet membership**
(same model as this host's SSH and syncthing); the app additionally rejects
cross-origin writes (CSRF) but has no per-user login.
