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

### Secrets Management
Workflow, key policy, and supply-chain rules live in `AGENTS.md`.

### Maintenance Scripts

| Task | Script |
| :--- | :--- |
| **Retune LUKS Argon2id** | `nix/scripts/luks-retune.sh help` — trades KDF memory hardness for faster boot unlock. Header backup + rollback included. |

If a LUKS retune ever leaves a host unable to unlock at boot, boot from a NixOS
live USB and restore the header backup created in step 1 of the procedure:

```bash
sudo cryptsetup luksHeaderRestore /dev/disk/by-partlabel/disk-main-luks \
  --header-backup-file <path-to-your-backup>.img
```

The full rollback rationale lives in the script's header comment.

---

## Development Standards

### CI & Quality Gates
This repository uses `pre-commit` to enforce high standards before code reaches the repository.
*   **Security:** `gitleaks` scans for accidental secret exposure.
*   **Linting:** `shellcheck` for scripts and `nixpkgs-fmt` for Nix expressions.
*   **Validation:** `nix flake check` ensures the Flake graph remains valid.

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
