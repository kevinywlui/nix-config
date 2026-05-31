# AI Agent Context: Architecture & Intent

This document is the operational contract for changing this repo. Architectural rules
about *where things go* live in the per-subdirectory READMEs (`base/README.md`,
`nix/README.md`, `nix/hosts/README.md`); this file covers *workflow* — the build gate,
secrets, supply chain, and constraints on what an agent may do.

## Verified Development Loop
This project uses **`nh os build`** as the primary verification tool during development and refactoring.

- **Purpose:** To prevent silent regressions, such as the accidental removal of packages or services.
- **Workflow:** `nh` automatically runs `nvd diff` between the currently active system and the newly built derivation. This provides a human-readable summary of every package addition, removal, or version change.
- **Mandatory gate:** Agents **must** run `nh os build` and confirm it succeeds **before staging or committing** any changes that touch `.nix` files. Do not commit first and build later. A clean build is a required pre-condition for every commit in this repository. Once the build is clean, **commit immediately without asking the user** — do not wait for confirmation.
- **What to check in the output:** No `error:` lines; no unexpected `- [package]` removals in the nvd diff. Version bumps and new packages are expected — silently removed packages must be investigated and resolved before proceeding.
- **CWD:** `nh os build` works from any directory because `programs.nh.flake = dotfilesPath` is wired in `nix/modules/nixos/profiles/core.nix`. No `cd` required.

## Hosts
This repository manages two NixOS hosts:

- **fw13** — Framework 13 laptop (`nix/hosts/fw13/`)
- **t480** — ThinkPad T480 (`nix/hosts/t480/`)

See **`nix/hosts/README.md`** for how a host is composed and how to add a new one.

## Cross-Platform Configuration Strategy
Home Manager modules in this repository (`nix/modules/home/`) prefer **`mkOutOfStoreSymlink`** for application settings so the same files work under GNU Stow on non-NixOS Linux. The full rationale, layout, and benefits live in **`base/README.md`**.

- **Agent guideline:** Do not migrate these configurations into declarative Nix attributes (e.g., `programs.zsh.shellAliases`) unless they are intended to be NixOS-exclusive.

## `dotfilesPath` is read-only
`dotfilesPath` (passed as a `specialArgs` value from `flake.nix`) equals `self.outPath` — i.e., the flake source in `/nix/store/...` at evaluation time. **It is read-only.** Never write to it from a Nix module or service. If a helper needs a *writable* working tree (the `setup-dotfiles` bootstrap script is the only one today), it must use a `$HOME`-relative literal instead. See the `cloneTarget` binding in `nix/modules/nixos/profiles/core.nix` for the pattern.

## Agent Constraints
- **Authentication:** AI agents cannot execute **`nh os switch`** or **`sudo nixos-rebuild switch`** because these commands require user authentication (sudo).
- **Workflow:** Agents should use **`nh os build`** to verify configurations. Once the build succeeds and changes are committed, the user must manually run the switch command to apply the changes to the system.

## Secrets Management
This repository uses **sops-nix** for all secrets. Never hardcode credentials, tokens, URLs containing secrets, or any sensitive values directly in `.nix` files.

- **Mechanism:** Secrets are stored encrypted in `nix/hosts/<host>/secrets.yaml` and decrypted at activation time using the host's SSH host key (`/etc/ssh/ssh_host_ed25519_key`), which sops-nix converts to an age key automatically.
- **Key policy:** `.sops.yaml` (at the repo root) defines which keys can decrypt which secrets file. Each host's SSH-derived age key can only decrypt its own secrets file. A personal `klui` age key is also listed for both hosts and can decrypt either host's secrets (useful for manual access and recovery).
- **Adding a secret:**
    1. Run `sops nix/hosts/<host>/secrets.yaml` from the repo root to open and edit the encrypted file.
    2. Declare the secret in the host config: `sops.secrets.<name> = {};`
    3. Reference it in services via `config.sops.secrets.<name>.path`, which resolves to `/run/secrets/<name>` at runtime.
- **Guideline:** If asked to add any credential or sensitive value to a host config, add it as a sops secret instead. The age public keys for each host are in `.sops.yaml`.

## Security & Supply Chain Integrity
This Nix repository relies on various external sources (e.g., flake inputs) that may change over time.

### Input Trust Tiers
Apply scrutiny proportional to the trust level of each input:

- **Tier 1 — High trust, update freely:** `nixpkgs` (stable channel). The large contributor base and multi-stage CI pipeline (`staging` → `staging-next` → `release`) make silent compromise unlikely. Security fixes here should be applied promptly.
- **Tier 2 — Medium trust, skim before committing:** `nixpkgs-unstable`, `home-manager`, `disko`, `sops-nix`, `nixos-hardware`. Reputable projects under the `NixOS/` or `nix-community/` GitHub orgs, but they move faster with less review. Skim the commit list for unexpected changes.
- **Tier 3 — Low trust, mandatory diff review:** Any input not under `NixOS/` or `nix-community/` on GitHub. Few watchers, high compromise risk. Read the actual code diff before every update — do not skip this. (There are currently no Tier 3 inputs; this tier applies if any are added in future.)

### Update Workflow
When running `nix flake update` or bumping individual inputs:

1. **Before updating**, note the current locked `rev` for each input being updated (from `flake.lock`).
2. Run the update: `nix flake update` or `nix flake update <input-name>`.
3. **Review the lock diff:** `git diff flake.lock` — for each changed input, identify the old and new `rev` values.
4. **For Tier 2/3 inputs**, delegate a subagent (`general-purpose` or `Explore`) to fetch and review the commit diff between old and new revs (e.g., `https://github.com/<org>/<repo>/compare/<old-rev>...<new-rev>`). The subagent must flag:
   - New network calls, `curl`/`wget`, or outbound connections added to build scripts
   - New binary blobs or base64-encoded payloads
   - Changes to fetch logic or hash verification that could bypass content-addressing
   - Commits from unfamiliar contributors, especially to `flake.nix` or build scripts
   - Backdated timestamps or commits pushed at unusual hours
5. **For Tier 3 inputs:** If anything looks suspicious, stop and raise it with the user before proceeding.
6. **Build gate still applies:** After review passes, run `nh os build` before committing.

### Mandate
Agents MUST NOT commit a `flake.lock` update without completing the diff review above for all changed Tier 2 and Tier 3 inputs. A passing build does not substitute for a security review.
