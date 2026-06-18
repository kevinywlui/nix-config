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
- **CI equivalence:** GitHub Actions (`.github/workflows/ci.yaml`) builds the same host toplevels on every PR and main push, and runs the pre-commit hooks with the toolchain pinned by `flake.lock` via the flake devShell — `nix develop -c pre-commit run --all-files` reproduces CI's lint pass locally. In an environment without Nix (e.g. a remote agent container), a green CI build of the branch is the equivalent gate. Standing CI invariants: the workflow holds no secrets, its token is read-only (`permissions: contents: read`), and fork PRs require approval before workflows run.

## Hosts
This repository manages two NixOS hosts:

- **fw13** — Framework 13 laptop (`nix/hosts/fw13/`)
- **t480** — ThinkPad T480 (`nix/hosts/t480/`)

See **`nix/hosts/README.md`** for how a host is composed and how to add a new one.

## Cross-Platform Configuration Strategy
Home Manager modules in this repository (`nix/modules/home/`) prefer **`mkOutOfStoreSymlink`** for application settings so the same files work under GNU Stow on non-NixOS Linux. The full rationale, layout, and benefits live in **`base/README.md`**.

- **Agent guideline:** Do not migrate these configurations into declarative Nix attributes (e.g., `programs.zsh.shellAliases`) unless they are intended to be NixOS-exclusive.

## `dotfilesPath` points at the live working tree
`dotfilesPath` (passed as a `specialArgs` value from `flake.nix`) is the literal absolute path `/home/klui/Code/nix-config` — not `self.outPath`. Two failure modes motivate the literal:

1. **`mkOutOfStoreSymlink` would freeze.** Home Manager symlinks under `base/` are meant to track the live tree (see `base/README.md`). A `self.outPath` value bakes a `/nix/store` snapshot per generation, so edits to `base/` would only surface after a rebuild — defeating the entire cross-platform Stow strategy.
2. **`NH_FLAKE` would go stale across shells.** `programs.nh.flake = "path:${dotfilesPath}"` is exported into every shell's environment. A per-eval `/nix/store/<hash>-source` value strands already-open shells on the prior generation's snapshot (cf. home-manager#8927). A byte-stable literal makes `NH_FLAKE` generation-invariant, so stale shells still hold the correct value.

**Constraints for contributors:**
- Do **not** revert `dotfilesPath` to `self.outPath`, `toString ./.`, or any expression whose value varies between evals.
- Keep the `path:` prefix on `programs.nh.flake = "path:${dotfilesPath}"` — Nix auto-promotes bare absolute paths inside git repos to `git+file:`, which only sees committed HEAD and breaks the build-before-commit workflow.
- If you relocate the working tree, update the literal in `flake.nix` *and* the `cloneTarget` literal in `nix/modules/nixos/profiles/core.nix`.
- For helpers that need to *write* into the working tree at activation time (the `setup-dotfiles` bootstrap is the only one today), use a `$HOME`-relative shell literal — Nix string interpolation can't produce `$HOME`.

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
- **Tier 2 — Medium trust, skim before committing:** `nixpkgs-unstable`, `home-manager`, `disko`, `sops-nix`, `nixos-hardware`. Mostly reputable projects under the `NixOS/` or `nix-community/` GitHub orgs, but they move faster with less review. The one exception is `sops-nix` (`github:Mic92/sops-nix`): it lives under a personal account rather than an org, but Mic92 is a long-standing core nixpkgs/NixOS maintainer and the project is widely depended on, so it earns Tier 2 trust despite the location. Skim the commit list for unexpected changes.
- **Tier 3 — Low trust, mandatory diff review:** Any input not under `NixOS/` or `nix-community/` on GitHub (the established-maintainer exception above, `sops-nix`, aside). Few watchers, high compromise risk. Read the actual code diff before every update — do not skip this. **There are currently no Tier 3 inputs.** (The former Tier 3 input `claude-desktop`, `github:aaddrick/claude-desktop-debian`, was removed along with its transitive `flake-parts` node.) If a Tier 3 input is reintroduced — especially a single-maintainer repo or one pinned to an explicit commit for a deliberate-bump workflow — the diff review on every bump must focus on the build/packaging scripts (`*.sh`), any `app.asar`/JS patching and native-binding shims, and the `fetchurl` **URL + hash** in the Nix package — a changed URL host is the highest-risk signal.

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

   **Exception — official `nixos/nixpkgs` channels.** A channel advance is thousands of commits, so its compare diff is always API-truncated and a source-diff review is neither complete nor the right tool (nixpkgs' defense is its reviewer base + Hydra CI). For nixpkgs the `/update-flake` skill instead asserts **provenance** (clean fast-forward + reachable from the Hydra-gated channel branch, via `nix/scripts/flake-prescreen.sh`) and reviews the bounded **nvd closure delta** at build time against a security watchlist, drilling into the trust surface with `nix/scripts/flake-diff-paths.sh` (path-scoped, no 300-file cap). See the skill's Steps 3 and 6.
5. **For Tier 3 inputs:** If anything looks suspicious, stop and raise it with the user before proceeding.
6. **Build gate still applies:** After review passes, run `nh os build` before committing.

### Mandate
Agents MUST NOT commit a `flake.lock` update without completing the diff review above for all changed Tier 2 and Tier 3 inputs. A passing build does not substitute for a security review.
