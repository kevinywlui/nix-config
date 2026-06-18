Update flake inputs with security pre-screening and parallel review.

## Step 1 — Run the gates (mechanized), and how every changed node is classified

The deterministic spine — update, enumerate, classify, prescreen, build, and surface the nvd watchlist — is mechanized in one script:

```
nix/scripts/flake-update-gates.sh            # all inputs; or: ... <input-name>
```

It prints exactly one verdict:
- **PASS** — gates clean, build green, nothing queued, no watchlist movement → go to **Step 7**.
- **NEEDS-HUMAN** — it lists a source-review queue and/or watchlist movement → do **Step 4** and **Step 6** on what it lists, then Step 7.
- **ABORT** — `nh os build` failed, or a changed node could not be classified → stop and investigate; nothing is committed.

`nix/scripts/flake-update-gates.sh --classify` is a no-side-effect dry run of the routing below. Steps 2, 3, and 5 are the detail of what the sequencer does (and the manual fallback); Steps 4, 6, 7 are the human parts.

**Classification — enumerate EVERY node in `flake.lock` and dispatch on node `type` first, then owner. Never a hardcoded input list: transitive nodes bump too.** (`nix flake metadata --json | jq '.locks.nodes'` exposes `.locked` type/owner/repo/rev/url and `.original.ref`, the tracked branch.)
- **`github`, owner `NixOS`/`nix-community` (case-insensitive), or `Mic92/sops-nix`** → **Tier 2**, deterministic prescreen. `sops-nix` is a personal account but Mic92 is a core nixpkgs/NixOS maintainer — a trusted Tier 2 exception (AGENTS.md "Input Trust Tiers"). `nixos/nixpkgs` additionally runs **provenance mode** using its tracked branch.
- **`github`, any other org** → **Tier 3**, full source review. This lock currently has **no** Tier 3 inputs (the former `claude-desktop` (`aaddrick`) and its transitive `flake-parts` (`hercules-ci`) were removed). Keep dispatching on `type`/owner anyway — a hardcoded tier list would silently skip a reintroduced or transitive Tier 3 node; type-based enumeration is the fix.
- **`tarball` from `releases.nixos.org`/`channels.nixos.org`** → the nixpkgs channel delivered as a tarball (this lock carries one named `nixpkgs`, pulled transitively). Its rev is in `.locked.rev`; the URL path maps to a channel branch (`releases.nixos.org/nixos/unstable/…` → `nixos-unstable`) and it is gated as `nixos/nixpkgs` provenance.
- **Any other type (`path`/`git`/`indirect`) or an unrecognized tarball host** → **unclassifiable: ABORT**, never a silent skip.

## Step 2 — Run the update

```
nix flake update
```

If the user passed an argument (e.g. `/update-flake home-manager`), update only that input:

```
nix flake update <input-name>
```

## Step 3 — Identify what changed and pre-screen

*(The sequencer automates this. The following is what it does internally and the manual fallback.)* Enumerate every changed node (Step 1) — `git diff flake.lock` plus, for rev-pinned inputs, `git diff flake.nix`. For each changed node, by its classification:

- **Tier 3 inputs:** skip pre-screening — always add to the review queue. Every change to a low-visibility repo warrants a full LLM review.
  - **Rev-pinned Tier 3 inputs** (any input pinned to an explicit SHA in `flake.nix`; none exist in this lock today) do **not** move on `nix flake update`, so they produce no `flake.lock` diff. Detect a bump from `git diff flake.nix` instead — old rev = the pre-edit SHA, new rev = the post-edit SHA — and always queue it. Never assume an unchanged lock means an unchanged Tier 3 input.
- **Tier 2 inputs:** run the pre-screening script (exit 0 = SKIP, exit 1 = REVIEW). Pass the tracked branch as a 4th arg whenever the input has an `original.ref` — it is required for `nixos/nixpkgs` (enables provenance mode) and harmless elsewhere:
  ```
  nix/scripts/flake-prescreen.sh <owner>/<repo> <old-rev> <new-rev> [tracked-branch]
  ```
  Log SKIP results. Add REVIEW results to the review queue with the prescreen output as context.

  **Provenance mode (official `nixos/nixpkgs` only).** A normal channel advance is thousands of commits, so the compare API's `.files` list is *always* truncated at 300 — a content diff of it is fundamentally incomplete, and reviewing 3000 commits audits the wrong thing (nixpkgs' defense is its reviewer base + Hydra CI, not a human reading the diff). So for `nixos/nixpkgs` with a branch arg, the prescreen swaps truncated-content review for a **complete provenance assertion**: the new rev must be a clean fast-forward of the old (`status=ahead`, `behind_by=0`) *and* reachable from the official channel branch (i.e. a Hydra-gated commit, not a substituted or forked SHA). A `REVIEW` here means provenance is *broken* (divergence / unreachable rev) — that is a strong signal: stop and investigate, do not treat it as routine. A `SKIP` defers the real content scrutiny to the closure-delta review in Step 6.

If the review queue is empty after pre-screening, skip to Step 5.

## Step 4 — Parallel security review

This step reviews the *source diff* of queued inputs — appropriate for small/medium repos where the full diff fits under the 300-file cap. Official `nixos/nixpkgs` is **not** reviewed here: it is gated by provenance in Step 3 and by the closure delta in Step 6, because its source diff is always truncated (see "Provenance mode" above). So the queue at this point is normal-sized Tier 2/3 inputs (home-manager, disko, sops-nix, and any reintroduced Tier 3 input) — exactly the items the sequencer listed under NEEDS-HUMAN.

Spawn **all** queued inputs as separate subagents simultaneously (in parallel — do not wait for one before starting the next). For each, use this brief:

> Run this command and analyse the output:
> ```
> gh api repos/<owner>/<repo>/compare/<old-rev>...<new-rev> \
>   --jq '{
>     ahead_by: .ahead_by,
>     commits: [.commits[].commit | {message: .message, author: .author.name}],
>     files: [.files[] | {filename, status, additions, deletions, patch: (.patch // "(binary or too large)")}]
>   }'
> ```
> The pre-screening script flagged this input for: <paste prescreen output>
>
> Report:
> 1. A plain-English summary of what changed (focus on the flagged files/patterns)
> 2. Whether any of these red flags are present:
>    - New outbound network calls (curl, wget, /dev/tcp) in build scripts or fetchers
>    - New binary blobs or base64-decoded content being executed
>    - Changes to fetch URLs or hash verification that could bypass content-addressing
>    - Commits from contributors not previously seen in this repo's history
>    - Unusual commit metadata (backdated timestamps, squashed/force-pushed history)
> 3. Conclude with **CLEAR** or **SUSPICIOUS** and your reasoning.

Wait for all subagents to return before proceeding.

**If any input is SUSPICIOUS:** stop immediately. Do not build. Report all findings to the user and ask how to proceed.

## Step 5 — Build

```
nh os build
```

Check: no `error:` lines, no unexpected package removals in the nvd diff.

## Step 6 — Closure-delta review

This is where the real content scrutiny for large inputs (nixpkgs especially) happens. Unlike the source diff, the **nvd closure delta** from Step 5 is bounded and high-signal — it is exactly the set of derivations that will enter *your* systems (~tens of packages, not thousands of commits), so it is both tractable and the code you actually run.

Scan the nvd diff for any package on the **security watchlist** — anything that handles untrusted input, crypto/transport, privilege, or the build/boot path:

> `curl`, `openssl`, `gnutls`, `openssh`, `sudo`, `polkit`, `pam`, `systemd`, `linux`/kernel, `glibc`, `nix` (the daemon), `dbus`, `sops`/`sops-nix`, `cups`, `unbound`/`bind`, `nss`, `ca-certificates`, anything in the boot/initrd path.

For each watchlist package that changed — and for any **unexpected package removal** or any version bump that looks anomalous (a downgrade, a non-upstream version string, a new source host) — get a **complete, path-scoped** view of the relevant source change (no 300-file cap):

```
nix/scripts/flake-diff-paths.sh nixos/nixpkgs <old-rev> <new-rev> [path...]
```

With no path args it covers the default trust surface (fetchers, stdenv, the Nix daemon, security modules, openssl/openssh/curl). Pass explicit paths to drill into a specific package (e.g. `pkgs/tools/networking/curl`). Set `FULL=1` for full patches instead of the commit list + diffstat. The first run blobless-mirrors the repo into `~/.cache/nix-config-review/` (a few hundred MB for nixpkgs, then reused).

If anything on the watchlist shows a suspicious change (changed source URL/host, weakened hash, new network/exec in a builder), treat it as **SUSPICIOUS**: stop, do not commit, report to the user. Routine version bumps with upstream-matching sources are fine — note them and proceed.

## Step 7 — Re-run hooks against the new lock, then commit

Before committing, run the pre-commit hooks through the updated lock rather than the activated system's PATH:

```
nix develop -c pre-commit run --all-files
```

This runs the hooks with the toolchain pinned by the working tree's `flake.lock` — exactly what CI uses — instead of the activated system's older tools, so any formatter churn from the bump surfaces here rather than failing CI (rationale: the devShell comment in `flake.nix`). Fold any resulting reformatting into the same commit.

Commit `flake.lock` (plus any hook-driven reformatting) immediately with a message listing each updated input and its new rev. Do not wait for confirmation. Remind the user to run `nh os switch`.
