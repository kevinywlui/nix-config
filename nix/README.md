# NixOS Configuration

Flake-based NixOS configuration for two hosts:

- **fw13** — Framework 13 (Intel Core Ultra) laptop; full Sway/Wayland desktop.
- **t480** — ThinkPad T480; headless home server (Paperless, status page).

Both share an encrypted Btrfs-on-LUKS layout and a common base of system modules;
each adds its own hardware tuning and services.

The flake entrypoint lives at the **repo root** (`../flake.nix`). Everything
under `nix/` is the body of that flake.

## Structure

Each directory has one responsibility:

- **`hosts/<name>/`** — a machine. Its `default.nix` `imports` block is the
  source of truth for what that host runs. Add per-host config here when no
  other host shares it.
- **`hosts/common/`** — host-shared bootstrap imported verbatim by every host,
  but not a NixOS module per se (today: just `disko.nix`, the LUKS+Btrfs disk
  layout `nixos-anywhere` consumes).
- **`modules/nixos/profiles/`** — coherent roles a host imports as a whole
  (`core`, `desktop`, `dev`, `laptop-hardware`). Profiles consume options;
  they don't declare them.
- **`modules/nixos/services/`** — opt-in services that toggle on via an
  option (`services.<x>.enable`). One file each unless the service grows
  scripts, schedules, or non-obvious state — then promote to a subdir (see
  `nightlight/` as the model).
- **`modules/nixos/<name>.nix`** at the modules root — option-only modules
  (no `config` body, just `options.<ns>.*`). Today: `ports.nix` registers
  every listening port in one place.
- **`modules/home/{profiles,services}/`** — same shape, Home Manager layer.
- **`overlays/`** — nixpkgs overlays. Wired by `flake.nix`, not per-module.
- **`scripts/`** — flake-adjacent shell helpers (`flake-prescreen.sh` backs
  the `/update-flake` security-review workflow).

### Where new files go

> **Profile** if every host that wants the role imports it wholesale and it
> doesn't expose a toggle. **Service** if it gates on `services.<x>.enable`
> (or similar). **Option-only module at the modules root** if it's just an
> options registry. **Overlay** if it changes how a package is built.

### When to escalate (triggers, deferred today)

- **Promote a service to a subdir** when it grows a second `.nix` file or
  non-Nix files (scripts/templates/state). *Example:* nightlight (Nix +
  shell scripts + generated config). Status-page hasn't crossed this yet.
- **Feature-split `profiles/desktop.nix`** when a host wants the same role
  with a different leaf — *e.g.* `profiles/desktop-sway.nix` +
  `profiles/desktop-hyprland.nix`. Both hosts are Sway today.
- **Extract `profiles/server.nix`** when host #2 server arrives. Today's
  serverness (paperless, status-page, lid-ignore, charge thresholds,
  hc-ping, ssh authorized_keys) lives inline in `hosts/t480/default.nix`.
- **Extract `lib/mkHost.nix`** when `mkHost` takes more than two parameters
  or branches on host kind.
- **Push `hosts/common/disko.nix` down to per-host** when a host's disk
  layout diverges. `common/` means "every host imports this verbatim"; the
  moment one host doesn't, copy the config to each host's `disko.nix` and
  delete `common/disko.nix`.

## Install (fresh hardware)

Provisioning is done with [`nixos-anywhere`](https://github.com/nix-community/nixos-anywhere),
run from the repo root against a target booted into any Linux/NixOS installer:

```bash
nixos-anywhere --flake .#fw13 root@<target-ip>
```

> **Warning:** this wipes `/dev/nvme0n1` on the target.

To partition/format an already-booted target by hand (escape hatch, no full install):

```bash
nix run github:nix-community/disko/latest -- --mode destroy,format,mount --flake .#fw13
```

After first boot, bootstrap the dotfiles (clones this repo to `~/Code/nix-config` and
installs zplug):

```bash
setup-dotfiles
make install            # or: make install-headless   (t480)
```

## Daily operations

| Task | Command |
| :--- | :--- |
| Verify a change builds (required before commit) | `nh os build` |
| Apply a built configuration | `nh os switch` |
| Update flake inputs (with security review) | `/update-flake` |
| Format Nix files | `nix fmt` |
| Test a host in a VM | `nix run .#nixosConfigurations.<host>.config.system.build.vm` |

`nh os build` runs an `nvd diff` against the running system, surfacing every package
add/remove/version change — the mechanism that catches silent regressions. It
works from any cwd because `programs.nh.flake = dotfilesPath` points at the
flake source.

## Secrets

Each host's secrets are encrypted with `sops-nix` in `hosts/<host>/secrets.yaml` and
decrypted at activation from the host's SSH key. The full workflow, key policy, and
supply-chain rules live in **`../AGENTS.md`** (Secrets Management, Security & Supply Chain).
