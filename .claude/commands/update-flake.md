Update flake inputs with security pre-screening and parallel review.

## Step 1 — Capture pre-update state

Parse `flake.lock` (at repo root) and record for every Tier 2 and Tier 3 input:
- `rev` (current locked commit)
- `owner` and `repo` (from the `locked` section — used to build the `gh` API path)

Trust tiers:
- **Tier 2:** `nixpkgs-unstable`, `home-manager`, `disko`, `sops-nix`, `nixos-hardware`
- **Tier 3:** any input whose GitHub org is not `NixOS` or `nix-community` (currently: `claude-desktop` from `aaddrick/claude-desktop-debian`, pinned to an explicit rev in `flake.nix`)

## Step 2 — Run the update

```
nix flake update
```

If the user passed an argument (e.g. `/update-flake home-manager`), update only that input:

```
nix flake update <input-name>
```

## Step 3 — Identify what changed and pre-screen

Run `git diff flake.lock`. For each Tier 2/3 input whose `rev` changed:

- **Tier 3 inputs:** skip pre-screening — always add to the review queue. Every change to a low-visibility repo warrants a full LLM review.
  - **Rev-pinned Tier 3 inputs** (e.g. `claude-desktop`, pinned to an explicit SHA in `flake.nix`) do **not** move on `nix flake update`, so they produce no `flake.lock` diff. Detect a bump from `git diff flake.nix` instead — old rev = the pre-edit SHA, new rev = the post-edit SHA — and always queue it. Never assume an unchanged lock means an unchanged Tier 3 input.
- **Tier 2 inputs:** run the pre-screening script (exit 0 = SKIP, exit 1 = REVIEW):
  ```
  nix/scripts/flake-prescreen.sh <owner>/<repo> <old-rev> <new-rev>
  ```
  Log SKIP results. Add REVIEW results to the review queue with the prescreen output as context.

If the review queue is empty after pre-screening, skip to Step 5.

## Step 4 — Parallel security review

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

## Step 6 — Commit

Commit `flake.lock` immediately with a message listing each updated input and its new rev. Do not wait for confirmation. Remind the user to run `nh os switch`.
