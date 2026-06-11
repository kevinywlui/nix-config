# Infrastructure & Personal Computing Environment

This repository contains the declarative infrastructure and configuration for my personal computing environment. It utilizes a hybrid management strategy: **NixOS** for system-level state and reproducibility, and **GNU Stow** for user-level dotfiles to ensure cross-platform compatibility and rapid iteration.

## Architectural Overview

### 1. System Management (NixOS)
The flake at the repo root composes a modular configuration for two hosts.
*   **Storage Strategy:** Unified `disko` implementation featuring LUKS2 encryption and BTRFS with subvolumes (`@`, `@home`, `@nix`) and Zstd compression.
*   **Host Topology:**
    *   `fw13`: Framework 13 (Intel Core Ultra) — Primary workstation with full desktop suite.
    *   `t480`: ThinkPad T480 — Headless home server running auxiliary services.
*   **Core Stack:** Systemd-boot, Tailscale for networking, and `nh` as the primary CLI helper for system operations.

### 2. User Environment (Dotfiles)
The `base/` directory contains platform-agnostic configurations managed via GNU Stow. This "OutOfStoreSymlink" approach allows these settings to be used on non-Nix systems and enables instant updates without a full system rebuild.
*   **Shell:** Zsh + zplug + Powerlevel10k.
*   **Editor:** Neovim configured via LazyVim.
*   **Desktop:** Sway (Wayland), Kitty, an eww status bar, and custom utility scripts in `~/.local/bin`.

### 3. Secret Management (Sops-Nix)
Security is handled via `sops-nix`, deriving age keys from the host's SSH ED25519 host key. The full workflow lives in `AGENTS.md`.

---

## Prerequisites

*   **NixOS:** Required for system-level configurations.
*   **GNU Stow:** Required for user-level dotfile symlinking.
*   **sops:** Required for managing encrypted secrets.

---

## Deployment & Lifecycle

### Fresh System Installation
For new hardware, use `nixos-anywhere` to provision from a controller machine:
```bash
nix run github:nix-community/nixos-anywhere -- --flake .#<host> root@<target-ip>
```

### Daily Operations
The workflow utilizes `nh` for optimized Nix operations and `make` for user-level changes.

| Task | Command |
| :--- | :--- |
| **Verify Configuration** | `nh os build` |
| **Apply System Update** | `nh os switch` |
| **Install Dotfiles** | `make install` |
| **Install Headless** | `make install-headless` |
| **Sync Dotfiles** | `make adopt` |
| **Update Dependencies** | `/update-flake` (see `AGENTS.md` for the security review) |

### Applying Userspace Updates Remotely
`nh os switch` restarts most services, but deliberately skips some (dbus,
logind) and never touches already-running user sessions. To fully apply a
userspace update — important on the headless, LUKS-encrypted `t480`, where a
real reboot would stall at the passphrase prompt — follow the switch with:
```bash
sudo systemctl soft-reboot
```
This restarts all of userspace under the new generation without going through
the bootloader or initrd, so the disk stays unlocked. Kernel, initrd, and
microcode changes still require a real reboot; if
`/run/booted-system/kernel` and `/run/current-system/kernel` differ, a
soft-reboot is not enough.

> Requires `soft-reboot.target`, which nixpkgs only ships since 2026-05
> ([NixOS/nixpkgs#514100](https://github.com/NixOS/nixpkgs/pull/514100)). On an
> older pin, opt in with
> `systemd.additionalUpstreamSystemUnits = [ "soft-reboot.target" "systemd-soft-reboot.service" ];`.

### Secrets Management
Workflow, key policy, and supply-chain rules live in `AGENTS.md`.

---

## Development Standards

### CI & Quality Gates
This repository uses `pre-commit` to enforce high standards before code reaches the repository.
*   **Security:** `gitleaks` scans for accidental secret exposure.
*   **Linting:** `shellcheck` for scripts and `nixpkgs-fmt` for Nix expressions.
*   **Validation:** `nix flake check` ensures the Flake graph remains valid.

GitHub Actions then re-runs the hooks (with tool versions pinned by `flake.lock`
via the flake's devShell, so CI and local results never drift) and builds the
full system closure for every host — the same toplevel `nh os build` produces.
On pull requests it also posts an `nvd` closure diff against the merge-base to
the job summary, warning on any package removals, mirroring the local
`nh os build` review contract described in `AGENTS.md`.

### AI-Augmented Engineering
Agent context for the installed AI tools (`claude-code`, `gemini-cli`) lives in `AGENTS.md` (symlinked to `CLAUDE.md` and `GEMINI.md`).

---

## Project Structure

```text
.
├── flake.nix               # NixOS flake entrypoint (mkHost, modules, overlays wired here)
├── flake.lock
├── .sops.yaml              # sops key policy: who decrypts each host's secrets
├── Makefile                # GNU Stow orchestration (install / install-headless / adopt)
├── AGENTS.md               # operational contract for changing this repo
├── base/                   # GNU Stow source — portable user dotfiles
│   ├── core/               #   runs without a graphical session
│   └── desktop/            #   wlroots Wayland + PipeWire + eww
└── nix/                    # NixOS flake body
    ├── hosts/              #   per-machine entrypoints (fw13, t480) + shared disko
    ├── modules/            #   reusable {nixos,home}/{profiles,services}
    ├── overlays/           #   nixpkgs overlays, wired by flake.nix
    └── scripts/            #   flake-adjacent helpers (e.g. flake-prescreen.sh)
```

Each subdirectory carries its own `README.md` explaining the rule for what
belongs there. Start with:

*   **`nix/README.md`** — flake body layout, the placement rule for new
    `.nix` files, the deploy/install flow.
*   **`nix/hosts/README.md`** — how a host is composed and how to add a new
    one (including the `common/disko.nix` convention).
*   **`base/README.md`** — the portability boundary (`core/` headless,
    `desktop/` graphical) and what is NixOS-only.
*   **`AGENTS.md`** — the build gate, secrets workflow, and supply-chain
    rules an agent must follow.
