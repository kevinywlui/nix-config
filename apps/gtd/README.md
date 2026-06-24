# apps/gtd — guided GTD over todo.txt

A small Go program (standard library only) with two binaries:

- **`gtd-server`** — a mobile-friendly web UI + JSON API that walks you through the
  GTD workflow over a todo.txt file.
- **`gtd`** — a CLI client for the same API.

It is packaged for NixOS as `pkgs.gtd` (`nix/overlays/gtd.nix`) and deployed by
`nix/modules/nixos/services/gtd/`. This README covers the app itself.

## The GTD ↔ todo.txt mapping

The data files are ordinary [todo.txt](https://github.com/todotxt/todo.txt) — any
todo.txt tool can read them. GTD concepts are encoded with these conventions:

| GTD concept            | Encoding                                  |
| ---------------------- | ----------------------------------------- |
| Inbox (unprocessed)    | `@inbox` context (an explicit marker)     |
| Context list           | `@calls`, `@computer`, `@errands`, …      |
| Next action            | a task with a real `@context`, not done, threshold reached — *derived*, not a tag |
| Project                | `+ProjectName` on its action(s)           |
| Waiting / delegated    | `@waiting` + `for:<person>`               |
| Defer / tickler        | `t:YYYY-MM-DD` (dormant until that date)  |
| Hard due date          | `due:YYYY-MM-DD`                          |
| Dependency / sequence  | `id:<key>` on a prerequisite, `after:<key>` on the task that waits for it (blocked, hidden from next actions, until the prerequisite is done) |
| Someday / Maybe        | a line in `someday.txt`                   |
| Reference              | a line in `reference.txt`                 |

**Why next-action is derived, not a priority.** A common shortcut is to use the
`(A)` priority to mean "next action." We deliberately don't: GTD treats priority
as contextual (decided at the moment you engage), and the todo.txt spec drops
priority when a task completes — so that meaning would be both doctrinally wrong
and lossy. Instead, *any actionable task parked on a context* is a next action.

The parser keeps each task's description verbatim, so unknown `key:value` tags
written by other todo.txt tools round-trip untouched.

## Files in the data directory

`todo.txt` (active), `done.txt` (completed archive), `someday.txt`,
`reference.txt`, `notes/` (free-form per-item notes, one file per `note:<key>`
tag), and `backups/` (last 50 pre-write snapshots per file).

## HTTP surface

Web (same-origin, browser): `/`, `/capture`, `/process`, `/review` (weekly review:
a read-only sequenced pass — inbox-to-zero, hard landscape, waiting, stalled
projects, someday scan), `/next`, `/contexts`,
`/waiting`, `/projects`, `/project?name=` (one project's plan; `/project/add`
appends a task, optionally blocked by another), `/done` (completed; POST also
completes a task), `/restore`, `/edit`, `/undo`, `/redo`, `/raw`, `/appearance`
(theme picker; `/theme` POST stores the choice in a cookie), `/help`. JSON
(CLI): `GET /api/tasks?view=next|inbox|waiting|done|all&context=&project=`,
`GET /api/projects`, `POST /api/capture`, `POST /api/done`, `POST /api/edit`,
`POST /api/restore`, `POST /api/undo`, `POST /api/redo`. All
mutating requests must be same-origin or carry the `X-GTD-Client` header (CSRF
defense); the CLI sets it automatically.

Mutations keep a single-level, whole-store **undo** point (a snapshot of every
file taken before each write); `POST /undo` restores it, and `POST /redo`
reapplies an undo (the pre-undo state is snapshotted too). In the web UI the
undo/redo affordance is transient — offered only on the page you land on right
after an action (via a one-shot `?undo=1`/`?redo=1` redirect flag), not a
persistent control, so it can't be mis-tapped from the nav. Notes live in their own
files so they may be multi-line; only the short `note:<key>` pointer sits on the
todo.txt line, and it's hidden from the displayed text.

## Theming

The UI ships twelve themes (dark: Mocha [default], Nord, Gruvbox, Dracula, Tokyo
Night, Rosé Pine; light: Latte, Solarized Light, GitHub Light, Everforest Light,
Rosé Pine Dawn, Gruvbox Light), all tuned to WCAG AA contrast. A theme is just a
set of CSS custom properties; the server stores the choice in a `theme` cookie and
emits `data-theme="<id>"` on `<html>` (default when unset), so the whole thing is
server-rendered and works with JS off. Colors never touch Go — only CSS.

To add a theme:

1. Add a `[data-theme="<id>"] { … }` block to `static/style.css` defining all the
   palette tokens (copy an existing block; the comment above the theme blocks lists
   the tokens and their roles). Keep it a bare single-attribute selector — the
   `/appearance` swatch previews rely on a nested `data-theme` re-scoping the vars.
2. Add a `themeInfo` entry to the `themes` registry in `cmd/gtd-server/server.go`
   with the same `ID`, a display `Name`, `Dark` (for the picker's grouping), and
   `BG` equal to that block's `--bg` (it drives the browser `theme-color` meta).

`TestThemesMatchCSS` enforces that every registry entry has a matching CSS block
and that `BG` equals the block's `--bg`. The default theme's `BG` is also mirrored
in `static/manifest.json` (the PWA splash/chrome color); update both if it changes.

## Progressive enhancement (optional JS)

The pages are fully server-rendered and work with JavaScript off; `static/app.js`
only layers on conveniences, each injected at runtime so nothing dead renders when
it's unavailable:

- **Voice capture** — a mic button on the Capture field that dictates straight
  into it via the browser's Web Speech API (`SpeechRecognition`), for adding a
  task hands-light on a phone: tap, speak, Capture. It appends to whatever's
  already typed and is injected only when the browser supports recognition **and**
  the page is a secure context — the HTTPS Tailscale Serve endpoint qualifies; over
  plain HTTP the button simply won't appear. **Privacy:** on most browsers,
  including Android Chrome, recognition uploads the captured audio to the browser
  vendor's cloud (e.g. Google) to transcribe it, so voice input does *not* stay on
  your device or tailnet the way the rest of the app's data does — type instead for
  anything sensitive. Markup/colors never touch Go.
- **Quick-date buttons** beside every date field (Today / Tomorrow / +1 week).
- **Ctrl/Cmd-Enter** submits the form you're in (uses `requestSubmit`, so the
  required-field validation still fires).

## Develop

```
cd apps/gtd
go test ./...                       # unit, integration, CLI, concurrency & fuzz-seed tests
go test -race ./...                 # exercise the store's locking (the concurrency
                                    # tests are written for this; needs CGO, so it's a
                                    # dev-only command — the Nix build gate stays CGO-free)
go test -run=Fuzz ./internal/todotxt              # run the fuzz seed corpora as plain tests
go test -fuzz=FuzzParseRoundTrip ./internal/todotxt   # actively fuzz the parser (Ctrl-C to stop)
go run ./cmd/gtd-server -dir /tmp/gtd -addr 127.0.0.1:8730
GTD_ENDPOINT=http://127.0.0.1:8730 go run ./cmd/gtd add "try it out"
```

Stdlib-only by design — this keeps `vendorHash = null` valid in the overlay and
means zero third-party supply-chain surface. Keep it that way unless there's a
strong reason; a new dependency would need a real `vendorHash` and a trust review.
