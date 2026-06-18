# Design: Versioning & Checkpointing GTD Notes

**Status:** Draft (pre-approval)
**Author:** Kevin Lui
**Reviewers:** Staff-engineer pass + technical-writer pass (complete)
**Component:** `apps/gtd` + `nix/modules/nixos/services/gtd`
**Last updated:** 2026-06-18

---

## TL;DR

> **Problem.** The GTD ("Getting Things Done") app keeps its todo.txt files on a
> single machine (t480) with only an in-memory undo and a small local backup
> ring. Lose that disk and everything is gone; there is no real history.
>
> **Proposal.** Make `/var/lib/gtd` a **git working tree** — the app records a
> best-effort commit after each change (shelling out to the system `git`, so no
> new Go dependencies). Then **fw13 pulls** that repo over the private network for
> a durable second copy. History (`git log`, item-level restore, weekly-review
> checkpoints) falls out of the same substrate.
>
> **Cost.** No new dependencies (`vendorHash = null` holds), no change to the
> canonical plaintext format, declarative and within the existing hardening.
>
> **Decision requested.** Approve (a) the layered git-as-substrate + fw13-pull
> approach over the rejected alternatives in [§7](#7-rejected-alternatives), and
> (b) starting with **Phase 0–1** of the rollout in [§8](#8-rollout-plan).

**Success criteria.** (1) A durable copy of the data exists on a second machine,
verified by one real restore. (2) Any past version of any file is recoverable
beyond the 50-entry ring. (3) Zero new Go dependencies and the canonical files
remain byte-for-byte hand-editable.

> 📖 **Reading guide.** Decision-makers can read §§1–2 then skip to §7 (Rejected
> alternatives) and §8 (Rollout). §§3–6 are implementation detail for whoever
> builds it.

---

## 1. Overview

### 1.1 Problem

The GTD app stores its data as plaintext todo.txt files in `/var/lib/gtd` on a
single host (**t480**). Today's safety net is three local mechanisms:

- **Atomic writes** (temp file + rename) so a crash can't corrupt a file.
- A **50-entry per-file backup ring** taken before each write.
- A **single-level, whole-store undo/redo** held *in memory* (lost on restart).

This protects against the *common* mistake — "I fat-fingered an edit, undo it" —
but leaves two real gaps:

1. **Durability.** t480 is the single canonical copy. There is no replication and
   no offsite. If the SSD dies, is accidentally `rm`'d, or the machine is lost,
   **everything is gone.**
2. **History.** Undo is one level deep and dies with the process. There is no way
   to ask "what did I complete last week," to bring back a task I deleted on
   Tuesday without rolling back everything since, or to return to a known-good
   state from a weekly review.

### 1.2 Goals

- Give the data a **durable second copy off t480** that survives disk/host loss.
- Give the notes a **browsable, recoverable history** beyond the 50-entry ring.
- Keep todo.txt **canonical, plaintext, and hand-editable** — the Stow /
  cross-platform ethos means `vim todo.txt` must keep working.
- Stay **stdlib-only** in Go so `vendorHash = null` holds and no supply-chain
  surface is added.
- Keep changes **declarative** and respect the existing service hardening and
  sops-nix secret model.

### 1.3 Non-goals

- Multi-user access, per-user audit, or access control beyond the existing
  tailnet (the private Tailscale network) boundary.
- Real-time multi-device sync / conflict resolution (t480 stays canonical).
- Enterprise-grade backup machinery — write-once storage (object-lock/WORM),
  automated restore drills, a dead-man's-switch service. See
  [§7 Rejected alternatives](#7-rejected-alternatives) for why these are
  over-insurance for one person's todo list.

### 1.4 Which problem matters most

The original ask — "add version control or checkpointing" — actually bundles
**two different problems** that want different solutions:

| Problem | Felt need | Status today |
| --- | --- | --- |
| **History** | "undo a bad edit", "what did I get done" | *largely solved* by the backup ring + undo |
| **Durability** | "the SSD died and it was the only copy" | **unsolved — the real gap** |

The highest-value work is durability (a second copy). History is a genuine
nice-to-have that the chosen substrate gives us cheaply, but it is not where the
risk lives. The rollout in §8 is ordered accordingly.

### 1.5 Solution summary

Use **git as the substrate** — the underlying byte-versioning layer everything
else reads from. Make `/var/lib/gtd` a git working tree. The app shells out to the
system `git` binary (via `os/exec`, so `vendorHash` stays `null`) and records a
best-effort commit after each mutation. todo.txt bytes stay canonical — git just
*observes* them, which means an out-of-band `vim` edit is captured for free on the
next commit.

On top of that substrate:

- **Durability:** **fw13 pulls** the git repo from t480 over the tailnet, giving a
  second physical copy. t480 holds no outbound credentials. An *opt-in*,
  age-encrypted offsite push from fw13 covers the "both machines lost" tail.
- **History:** `git log` *is* the history. A stable `id:` tag (already present in
  the code) on each task makes item-level restore possible. Checkpoints are git
  tags.

The existing ring + undo stay as the fast, git-independent safety net.

---

## 2. Architecture

```
┌───────────────────────────── t480 (canonical) ──────────────────────────────┐
│                                                                              │
│  gtd-server (Go, stdlib-only)                                                │
│    │  mutation (capture / complete / trash / edit / note …)                  │
│    ▼                                                                         │
│  Store (internal/todotxt)                                                    │
│    • atomic write (temp + rename)     ← unchanged                            │
│    • 50-entry backup ring             ← unchanged                            │
│    • in-memory undo/redo              ← unchanged                            │
│    • enqueue commit message ──────────►  internal/vcs committer goroutine    │
│                                              │ (debounced, off-lock)         │
│  /var/lib/gtd/                               ▼                               │
│    todo.txt done.txt someday.txt reference.txt notes/   +   .git/            │
│                                              ▲                               │
│  systemd gtd-autocommit.timer ── catches out-of-band `vim` edits ───────────┤
│                                                                              │
└──────────── ssh: git-upload-pack (read-only, forced-command) ───────────────┘
                                   │  tailnet (Tailscale, via MagicDNS name)
┌───────────────────────── fw13 (replica) ─────────▼───────────────────────────┐
│  systemd gtd-pull.timer → git fetch --prune → /var/lib/gtd-mirror/.git        │
│  writes .last-pull-ok (freshness signal)                                      │
│  (optional, opt-in) → age-encrypt → push to private remote (offsite)          │
└───────────────────────────────────────────────────────────────────────────────┘
```

### 2.1 Architecture layers

> Note: these architecture **layers** are not the same as the rollout **phases**
> in §8. One layer can span phases (the substrate is built across Phases 0–1);
> the §8 table maps them.

1. **Substrate — git-in-dir (canonical byte-truth).** `/var/lib/gtd` is a git
   working tree. Every mutation produces a best-effort commit with a semantic
   message. Out-of-band edits are caught by a periodic autocommit timer.
2. **Local-redundant — fw13 pull.** A second physical copy on the laptop,
   pulled (not pushed) over the tailnet.
3. **Offsite-optional — encrypted push.** age-encrypted copy to a private
   remote, opt-in, driven from fw13 so t480 never holds offsite credentials.
4. **Monitoring — proportionate.** A `/healthz` `git fsck` field, a pull-freshness
   signal, and an annual manual restore test. No dead-man's-switch.

### 2.2 Why git is the substrate

- **It absorbs out-of-band edits for free.** A journal/event-log "source of
  truth" model breaks the moment the user runs `vim todo.txt`; git just diffs
  whatever bytes are on disk. This property — versioning on-disk bytes rather than
  intercepting writes — is what decided the substrate.
- **It is content-addressed.** Every object is SHA-verified on read/fetch, so
  corruption surfaces via `git fsck` and a corrupt fetch fails loudly. This is
  exactly why git beats restic+object-lock for *text* data
  (see [§7.2](#72-cloud-restic--object-lock-for-the-gtd-data)).
- **It costs ~no new code or dependencies.** `os/exec` to a Nix-pinned `git`
  keeps the app stdlib-only and `vendorHash = null` valid.
- **`git log` is the history layer.** No separate event log to keep in sync.

---

## 3. Detailed design — substrate (in-app git)

### 3.1 `internal/vcs` package

A new stdlib-only package wraps the git binary.

```go
package vcs

type Repo struct {
    dir     string        // store root
    gitPath string        // exec.LookPath("git"), "" if absent
    gitOK   bool          // gitPath != "" && init succeeded
    timeout time.Duration // per-exec bound (default 5s)
    // ... debounce/committer machinery (see §3.3)
}

func New(dir string) *Repo  // resolves git once; never errors on missing git
func (r *Repo) Commit(msg string) // enqueue a best-effort commit (non-blocking)
func (r *Repo) Flush()      // drain pending commits (tests + shutdown)
func (r *Repo) Close()      // flush then stop the committer goroutine
func (r *Repo) Available() bool
```

Design rules:

- **Best-effort, never fatal.** A missing, failing, or hung git must never turn a
  mutation into an error. If `exec.LookPath("git")` fails, `gitOK = false` and
  every `Commit` is a silent no-op. The app is fully functional without git; git
  is a *bonus* layer.
- **Fully isolated git environment.** The service runs hardened with `ProtectHome`
  and a cleared PATH, so each invocation runs with an **absolute** `gitPath` and a
  pinned env that depends on nothing outside the store:
  - `HOME=<dir>` (git wants a home for `gc.log` etc.; point it at the writable
    state dir),
  - `GIT_CONFIG_GLOBAL=/dev/null`, `GIT_CONFIG_SYSTEM=/dev/null` (ignore any
    system/global config),
  - `GIT_TERMINAL_PROMPT=0` (never block on a credential prompt),
  - identity per-invocation: `-c user.email=gtd@localhost -c user.name=gtd`,
  - `-c commit.gpgsign=false -c core.hooksPath=/dev/null -c core.autocrlf=false`.
- **Bounded execution.** Every git call uses `exec.CommandContext` with the
  `timeout`, so a stuck lock or slow disk can never hang forever.
- **Lazy idempotent init with an explicit baseline.** On first use: detect an
  existing repo with `git rev-parse --git-dir`; if absent, `git init -q`
  (with `--shared=group`, see §4.1), write `.gitignore`, then make a single
  **`baseline: import existing data`** commit capturing whatever files already
  live in `/var/lib/gtd`. This matters: the live data dir already has real
  content with no `id:` tags; the baseline commit imports it cleanly instead of
  conflating it with the first mutation's message.

### 3.2 `.gitignore` and what gets committed

```
backups/
.tmp-*
```

- `backups/` (the 50-entry ring × 4 files) and the `.tmp-*` atomic-write staging
  files are excluded — committing them would bloat history and risk staging a
  half-written temp file.
- The four `*.txt` files **and `notes/`** are tracked. Tracking `notes/` closes a
  real gap: `WriteNote` is *not* covered by the in-memory undo/snapshot today, so
  git becomes the only versioning notes get.
- **Staging:** `git add -- todo.txt done.txt someday.txt reference.txt notes/`.
  Listing the managed paths (rather than `git add -A`) keeps the rest of the dir
  out of history. Note `notes/` is a directory of arbitrarily-named files, so it
  must be staged as a directory — which means the `.gitignore` `.tmp-*` entry **is
  load-bearing** (it excludes `notes/.tmp-*` that `WriteNote` briefly creates).
  This is the one spot where the ignore list is not optional; a comment in
  `store.go` should tie the temp-file prefix to it.

### 3.3 Where the commit fires — and the concurrency decision

The hook must run for **every** mutation. Because `Append` and the append-half of
`Transfer` bypass the central `rawWriteLocked` (they use `O_APPEND`), the hook is
wired at the **method level**: `Mutate`, `Append`, `Transfer`, `WriteNote`, and
`Undo`/`Redo`.

> **Decision: commit asynchronously, off the store mutex, debounced.** The simpler
> "commit synchronously inside the mutex after the rename" was rejected for three
> reasons: (1) a hung `git` while holding the mutex freezes **all** reads and
> writes; (2) it serializes a real fork+exec (~30–80 ms) behind the global lock on
> every write; (3) the 420-mutation concurrency test would serialize 420 real
> commits.

**The committer.** Under the store lock, a mutation does two cheap things: record
its semantic message and set a `pending` flag, then signal the committer. A single
long-lived committer goroutine owns all git execution:

```
loop:
  wait for signal
  debounce: sleep until quiescent (idle window, e.g. 750ms) or a max-batch cap
  under committer lock: if !pending { continue }; take message(s); clear pending
  run: git add <managed paths> && git commit -m <coalesced message>   (off-lock)
```

**Avoiding the lost-wakeup bug.** The `pending` flag is re-checked under the
committer's own lock *after* each commit completes; if a mutation arrived during
the in-flight commit it set `pending` again, so the loop runs once more rather
than sleeping with work outstanding. (A bare buffered-1 channel without this
re-check can coalesce a wakeup into the in-flight run and strand the last
mutation.)

**Commit semantics are point-in-time, not transactional.** Because the commit runs
off-lock, it captures **whatever bytes are on disk at `git add` time**, not a
frozen snapshot of the mutation that triggered it. A commit's message is therefore
an *advisory label* on a tree state, not a guarantee that the tree contains
exactly that one logical change. This is acceptable and is the reason §5's history
features key off **content and `id:` tags**, not off a 1:1 message↔change mapping.

**Durability of the trigger.** The bytes are already durable via rename before the
commit runs; the only exposure is a crash in the sub-second debounce window
leaving an *uncommitted but intact* working tree, which the next mutation or the
§4.3 autocommit timer commits. **`Flush()`/`Close()`** drain the queue on clean
shutdown (wired into server shutdown) so a stop→start cycle doesn't leave the last
burst uncommitted.

**Undo/Redo caveat.** Undo/Redo are whole-store rollbacks that also produce
commits. In `git log` an Undo looks like a *forward* change, and the §5.2 Trash
predicate (ids-ever-seen − ids-now) will correctly resurface/re-hide ids as
undo/redo toggles. Item-level restore (§5.2) and whole-store undo are deliberately
separate mechanisms; the doc does not try to unify them.

### 3.4 Semantic commit messages

The message is built by the caller from the verb it already knows:

| Verb | Store call | Message |
| --- | --- | --- |
| capture | `Append` | `capture: <text, truncated>` |
| complete | `Transfer` → done | `complete #<id>` |
| restore | `Transfer` → active | `restore #<id>` |
| trash | `Mutate` | `trash #<id>` |
| edit | `Mutate` | `edit #<id>` |
| process → ref/someday | `Transfer` | `move #<id> -> <dest>` |
| process → project / next action | `Mutate` | `project: <name>` |
| note | `WriteNote` | `note #<id>` |
| undo / redo | `Undo`/`Redo` | `undo` / `redo` (self-described) |

This turns `git log --oneline` into a human-readable activity journal with **no
parsing** — the foundation for the history features in §5.

### 3.5 Item identity (`id:` tag)

Item-level restore needs a stable handle that survives a task moving between files
(todo → done → todo) and surviving a description edit. The code **already has
one**: `newDepID()` (the existing id minter) produces an `id:<base36>` tag for the
dependency feature, and `DisplayText` already strips `id:` so it never shows in the
UI or breaks interop with other todo.txt tools.

Change: mint `id:` on **every** task at creation. The main path is `s.capture`,
which web capture, `/api/capture`, and the CLI all funnel through (one edit); the
two project / next-action creation paths (`handleProcessDo` "project" branch and
`handleProjectAdd`) need the same one-liner, so ~3 edits total.

Accepted limitations:

- Only **future, app-created** tasks get ids. Tasks created by other todo.txt
  tools or by hand-editing have none, so item-restore (§5.2) under-reports for
  them. Documented, not hidden.
- **Collision hardening is a prerequisite for Phase 2's Trash, not an optional
  fast-follow.** `newDepID()` is nanosecond-based and can collide under rapid
  concurrent capture; since Trash keys on id uniqueness, a counter or
  `crypto/rand` suffix must land before the Trash feature relies on it. (Phase 0
  can mint as-is — ids are advisory there.)

### 3.6 Relationship to the existing 50-entry backup ring

The Store already keeps a per-file ring of the last 50 pre-write snapshots
(`keepBackups = 50` in `store.go`). A reasonable question is whether git makes it
redundant. **It does not — yet — and the decision is to keep it, then shrink it,
never to delete it.**

The ring and git cover different failure domains:

| | 50-entry ring | git layer |
| --- | --- | --- |
| Timing | synchronous, **pre-write** (old bytes captured before overwrite) | async, **post-write** (debounced, after rename) |
| Conditional? | always runs, stdlib, zero-dep | **best-effort** — no-ops if git is absent/failing/timing-out |
| In production? | works today | **dormant until Phase 1** (git on the service PATH) |

The ring uniquely covers "undo the immediately-prior write" when git has not yet
captured that state: inside the debounce window, when git is off PATH, or on the
first change since the last commit. Atomic writes prevent *torn* files but do not
provide this logical undo. Removing the ring would couple the primary
"undo the last bad write" path to an unproven, conditional, currently-dormant
layer.

Once git is reliably running in production, git owns *deep* history completely
(unbounded, off-host once pulled), and the ring's only enduring role is the
synchronous pre-write net above — which does **not** need 50 entries.

**Decision:**

- Keep the ring **untouched through Phases 0–3** while git is unproven and partly
  dormant.
- After git has run cleanly in production for a while, **shrink `keepBackups`
  from 50 to ~5** — git owns history; the ring becomes a thin synchronous
  crash/no-git net. This is the real complexity reduction (less `backups/` churn,
  simpler mental model) without giving up the one property git cannot offer.
- **Do not delete it** and do not invest further in it.

---

## 4. Detailed design — durability

### 4.1 fw13 pulls (the primary durability win)

A `systemd` timer on fw13 fetches the t480 repo over the tailnet into a mirror
clone. **Pull, not push,** is deliberate:

- t480 (the exposed, canonical host) holds **no outbound credentials**. A
  compromised or buggy t480 cannot reach into or corrupt the replica.
- The pull interval is a **corruption quarantine window**: a bad edit propagated
  from t480 remains recoverable on fw13 from the pre-corruption commit (and fw13's
  reflog). A plain `fetch` never force-updates — a non-fast-forward history from
  t480 is quarantined in remote-tracking refs, not merged over the replica — so
  fw13 is never silently overwritten.

**t480 side** — expose the repo read-only via a restricted SSH forced-command for
a dedicated, unprivileged `gtd-pull` user. The public key goes in the Nix module
in plaintext (public keys are not secrets); only fw13's *private* key is a sops
secret.

```
command="${pkgs.git}/bin/git-shell -c \"git-upload-pack '/var/lib/gtd'\"",\
  no-port-forwarding,no-X11-forwarding,no-agent-forwarding,no-pty <fw13-pull-pubkey>
```

`upload-pack` is read-only (fetch); `receive-pack`/push is never granted. SSH is
already firewalled to `tailscale0`.

**Repo readability is a real gotcha.** `git-upload-pack` must read `.git/objects`,
which the gtd server creates. To let the `gtd-pull` user (member of the `gtd`
group) read it without granting write:

- init the repo with `git init --shared=group` (`core.sharedRepository=group`) so
  new objects/packs are group-readable,
- set the service umask to `0027` and `StateDirectoryMode = "0750"`,
- **all** git operations on `/var/lib/gtd` (in-app committer, autocommit timer,
  weekly `gc`) run as the **`gtd`** user — never root — to avoid git's
  dubious-ownership refusal and root-owned, group-unreadable objects.

A Phase-3 test should assert a fresh clone over the forced-command succeeds.

**fw13 side** — `gtd-pull.service` (oneshot) + `gtd-pull.timer` (hourly):

```
GIT_SSH_COMMAND="ssh -i <sops:gtd-pull-key>" \
  git -C /var/lib/gtd-mirror fetch --prune origin \
  && date -Iseconds > /var/lib/gtd-mirror/.last-pull-ok
```

ordered `after = network-online.target tailscaled.service` (matching the existing
tailscale-serve precedent; a timer just retries next tick if the tailnet isn't
ready — no boot fragility).

### 4.2 Offsite (opt-in, off by default)

Covers only the low-likelihood "both machines lost" tail. Driven from **fw13**, so
t480 never touches a cloud secret:

- age-encrypt the repo (bundle or tree) and push to a private remote.
- **Never cleartext** — the nix-config repo is public, and the data leaks names
  ("waiting for: …"), health, and other sensitive items.
- Use **git** (encrypted bundle), not restic+object-lock — see §7.2.
- **Key escrow:** the age key lives in a password manager **and** on paper, never
  only on the two machines (or the disaster that justifies the offsite copy also
  destroys the key).

### 4.3 Catching out-of-band edits

The in-app committer only fires on app mutations. A direct `vim todo.txt` (or an
edit while the server is stopped) is caught by `gtd-autocommit.timer` on t480
(every 15 min), running as the `gtd` user:

```sh
cd /var/lib/gtd
git add -A -- todo.txt done.txt someday.txt reference.txt notes
git diff --cached --quiet \
  || git -c user.name=gtd -c user.email=gtd@localhost commit -m "autocommit $(date -Iseconds)"
```

Staging explicitly with `git add -A -- <paths>` (not `commit -am`) is required so
**newly created** files — e.g. a new note written by hand — are picked up; `-am`
only re-stages already-tracked files. The `--cached --quiet` guard makes empty
runs no-ops. A systemd timer (rather than in-app logic) is chosen because it also
covers the window when the server is down.

---

## 5. Detailed design — history features

> These ship in **Phase 2** (after the substrate and durability). They are
> read-only views over the history the substrate records.

### 5.1 `/history` — the cheap, robust first cut

A read-only page that runs `git log --oneline -- todo.txt done.txt` and a
`git show` for one commit. Because §3.4 already writes semantic subjects, this is
a readable timeline with **zero parsing**, and recovery is "find the commit, copy
the line back via the existing edit/capture UI." This deliberately defers the
fragile id/predicate machinery below.

### 5.2 Trash (item-level restore)

"Bring back the one task I deleted, without rolling back everything since."

- **Predicate:** trashed ids = (union of `id:` tokens ever seen in the history of
  `todo.txt` + `done.txt`) − (ids present in the *current* two files).
- **Cost & bounding.** Because `writeLocked` rewrites whole files, every commit
  touches todo.txt, so the "ever seen" scan is O(history) with no cheap
  path-filter shortcut. For a personal tool this is fine, but **bound the scan**
  (cap the window, or cache the ever-seen set) so a multi-thousand-commit history
  doesn't make the Trash page slow.
- **Recovering one line:** do **not** parse `git log -p` hunks (whole-file
  rewrites make every line look removed+re-added). For the single id being
  restored, use a point query (`git log -S "id:<x>" -p`).
- **Restore is a forward mutation, never `reset --hard`.** Re-append the recovered
  line as a new mutation so item-restores and any coarse rollback compose instead
  of clobbering each other. If the recovered line parses as `Done`, route it
  through the existing `markUndone`/`restoreDone` so it doesn't come back still
  `x`-marked and invisible.
- **Known false positive:** if a *completed* task's line is later hand-deleted
  from `done.txt` (e.g. another tool prunes the archive), the predicate sees it as
  trashed and offers to restore something deliberately archived. Acceptable;
  surface restore as a suggestion, not an automatic action.
- **Depends on id uniqueness** — hence the collision-hardening prerequisite in
  §3.5.

### 5.3 Checkpoints & weekly-review digest

- **Checkpoint = annotated git tag** (`gtd/review/<timestamp>`), surfaced to the
  user as a friendly label ("Review — Wk of Jun 16"), never a SHA.
- **Digest** between two review tags: completed (subjects matching `complete:`),
  stalled (open tasks untouched 2+ weeks), and "re-added after deleting" (an id
  whose history shows delete→re-add). This makes history an *input to the GTD
  process*, not just a recovery tool.

---

## 6. Monitoring (right-sized)

For one person's todo list, a full dead-man's-switch and automated restore-drills
are over-insurance. The proportionate set:

- **`/healthz` git-fsck field** — `git fsck` result + `HEAD` commit + a `dirty`
  flag (uncommitted out-of-band edits pending) + repo size. `fsck` is a full
  object walk, so **cache it** (run on a throttle, e.g. once an hour, not per
  request). Reachable over the tailnet through the existing `tailscale serve`
  front; no new port. (Reuse is tempting, but the existing `hc-ping-url`
  Healthchecks endpoint is a separate single-purpose check — don't overload it.)
- **Pull-freshness signal (the one safeguard worth keeping).** fw13 writes
  `.last-pull-ok` after each successful fetch (§4.1); `/healthz` flags red if it is
  older than ~48h. This converts the most likely catastrophic outcome — a
  silently-dead pull nobody notices for months — into a visible one, with one file
  and one comparison and no new daemon.
- **Annual manual restore test** — a calendar reminder to clone the fw13 mirror to
  a scratch dir, `git fsck`, and diff against t480. An untested backup is a
  hypothesis.

---

## 7. Rejected alternatives

### 7.1 Journal / event log as the source of truth

Modeling todo.txt as a *projection* of an append-only event log gives clean
semantic queries, but its source of truth desyncs the instant the user hand-edits
a file — exactly the workflow the repo's Stow ethos requires. Git, by contrast,
treats on-disk bytes as truth and absorbs the edit for free. Rejected as the
substrate; `git log` already provides the lossy-but-sufficient semantic layer.

### 7.2 Cloud restic + object-lock for the GTD data

restic to an object-locked bucket *looks* like the gold-standard backup, but for
this data it is a **silent-corruption trap**: bucket lifecycle rules delete packs
with no knowledge of restic's reference graph, and object-lock prevents `prune`
from compacting — so the repo grows unbounded until a blind lifecycle rule
eventually eats a live pack, and you find out at restore time. For line-oriented
*text*, git is content-addressed, self-verifying, and trivially recoverable.
**restic stays only for genuinely binary data** (e.g. paperless), untouched by
this design.

### 7.3 btrfs snapshots as the backup

t480 is btrfs, so read-only snapshots are cheap — but they live on the same disk
and pool, and `btrfs subvolume delete` by root erases them. Useful as a local
point-in-time consistency source at best; calling it a backup is a dangerous
fiction. A dedicated `@gtd` subvol also needs a one-time manual migration (disko
only acts at install). Not worth it here.

### 7.4 Push-based offsite with credentials on t480

Any backup the live host can delete or force-push shares the live host's blast
radius: a compromised t480 runs `restic forget --prune` / `git push --force` and
the "offsite" copy dies with the original. Pull-based replication (§4.1) inverts
this. Rejected.

### 7.5 Dead-man's-switch / automated restore-drill service

Operational machinery appropriate for a *service*, not a personal notes file. Its
own failure modes and maintenance cost exceed the risk it covers here. Replaced by
the §6 freshness signal + annual manual test.

---

## 8. Rollout plan

Rollout **phases** are sequenced by value-and-risk and do **not** map 1:1 to the
architecture **layers** in §2.1 — Phases 0–1 build the substrate layer, Phase 3 is
the local-redundant layer, Phase 4 the offsite layer.

| Phase | Scope | Who ships | Build gate |
| --- | --- | --- | --- |
| **0** | `internal/vcs` + async-debounced committer + `Flush/Close` + `id:` minting + tests | agent | ✅ fully agent-verifiable |
| **1** | `path = [ pkgs.git ]` on the gtd unit, `--shared=group` init, `gtd-autocommit.timer`, weekly `gc` (as `gtd`) | agent writes; user switches | ⚙️ builds; runtime needs switch |
| **2** | `/history` view, then Trash (after id-collision hardening) + checkpoints/digest | agent writes; user switches | ⚙️ builds; runtime needs switch |
| **3** | fw13 pull (forced-command key + timer) + `.last-pull-ok` + `/healthz` | agent writes; user does keygen + mirror init + switch | ⚙️ builds; runtime needs switch |
| **4** | *Opt-in* encrypted offsite from fw13 | user opts in + escrows key | — manual |

Legend: **✅ fully agent-verifiable** = `go test` + `nh os build` prove it with no
switch. **⚙️ builds** = the agent can write and `nh os build` it, but the runtime
effect needs a user `switch`. **— manual** = user-driven, not a code change.

**Phase 0 is fully agent-verifiable** (pure Go + a build check) and
**dormant-safe**: until Phase 1 puts `git` on the service PATH, `exec.LookPath`
fails and the layer silently no-ops — so it can land and be proven green long
before anything runs in production.

### 8.1 Definition of done (Phase 0)

- `internal/vcs` exists, stdlib-only; `gtd` builds with `vendorHash = null`
  unchanged.
- All six mutators enqueue a best-effort commit; a missing, failing, or hung git
  never turns a mutation into an error (proven by graceful-degrade and timeout
  tests).
- Lazy idempotent init writes `.gitignore` and a `baseline` commit; identity via
  `-c` flags; `Flush`/`Close` drain on shutdown.
- Every app-created task carries an `id:`; the UI never leaks it.
- `CGO_ENABLED=0 go test ./...` green and `nh os build` clean.

### 8.2 What the build gate actually proves (a caveat)

The Nix build sandbox has **no git and no `$HOME`**. Git-dependent tests therefore
`t.Skip` under `nh os build` — so the build gate proves *compilation, the
graceful-degrade (git-absent) path, and `vendorHash = null`*, but it does **not**
exercise real commits. The commit path is covered by `go test` run **locally with
git present** (and could later run in a dedicated CI check that provides git). The
doc states this so "Phase 0 proven green" is not overstated.

### 8.3 Key tests

- **Byte-fidelity invariant:** after `Flush()`, `git show HEAD:todo.txt` ==
  `Store.Raw(todo.txt)`. Guards the "git never rewrites the bytes" promise (the
  `Flush` is required — without it HEAD may lag the committer).
- **Graceful degrade:** mutations succeed and files are correct with git absent.
- **Clean tree under concurrency** (`-race`): after the 420-mutation storm and a
  `Flush`, the working tree is clean and `git show HEAD:todo.txt` has the expected
  line count.
- **Timeout doesn't hang:** a tiny injected timeout returns promptly.
- **No lost wakeup:** N mutations enqueued during an in-flight commit all land by
  `Flush()`.
- **id round-trip + UI hiding; idempotent init + baseline commit.**

---

## 9. Risks & mitigations

| Risk | Mitigation |
| --- | --- |
| Git subtly rewrites bytes (CRLF, trailing newline) → breaks byte-truth & interop | Pin `core.autocrlf=false`, no clean/smudge filters; byte-fidelity round-trip test |
| `exec(git)` hangs / blocks the server | Commit off the mutex + `CommandContext` timeout + `GIT_TERMINAL_PROMPT=0` |
| Lost wakeup strands the last mutation's commit | `pending` re-checked under committer lock after each commit; `Flush`/`Close` on shutdown |
| `.git` not readable by the pull user | `git init --shared=group`, umask 0027, `StateDirectoryMode=0750`, all git ops as `gtd` |
| `.git` bloat / gc race | Coalesced commits; weekly `git gc` timer (as `gtd`, off-hours); repo-size in `/healthz` |
| Silent dead pull (the most likely failure) | `.last-pull-ok` freshness flag in `/healthz` |
| `id:` leaks to UI / collides / lost on hand-edit | `DisplayText` already strips it; collision hardening gates Trash; documented coverage gap |
| Trash/timeline parsing fragile or slow | `/history` first (no parsing); point queries + bounded windows for the rest |
| Pre-existing live data has no baseline | Explicit `baseline` commit on init (§3.1) |
| Scope creep back into rejected alternatives | This doc's §7 is the standing answer |

---

## 10. Open questions

1. Commit coalescing window — **decided for Phase 0** as an idle debounce (~750ms)
   with a max-batch cap, because the lost-wakeup correctness depends on a concrete
   trigger. Tune the window after observing real `git log` granularity.
2. `id:` collision hardening — **decided as a Phase-2 prerequisite** (counter or
   `crypto/rand` suffix), since Trash depends on uniqueness.
3. Is the opt-in offsite (Phase 4) worth documenting now, or deferred until the
   data demonstrably matters more? (Owner decision; not blocking.)
