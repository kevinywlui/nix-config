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

Web (same-origin, browser): `/`, `/capture`, `/process`, `/next`, `/contexts`,
`/waiting`, `/projects`, `/done` (completed; POST also completes a task),
`/restore`, `/edit`, `/undo`, `/raw`, `/help`. JSON (CLI):
`GET /api/tasks?view=next|inbox|waiting|done|all&context=&project=`, `POST /api/capture`,
`POST /api/done`, `POST /api/edit`, `POST /api/restore`, `POST /api/undo`. All
mutating requests must be same-origin or carry the `X-GTD-Client` header (CSRF
defense); the CLI sets it automatically.

Mutations keep a single-level, whole-store **undo** point (a snapshot of every
file taken before each write); `POST /undo` restores it. Notes live in their own
files so they may be multi-line; only the short `note:<key>` pointer sits on the
todo.txt line, and it's hidden from the displayed text.

## Develop

```
cd apps/gtd
go test ./...                       # parser + GTD view unit tests
go run ./cmd/gtd-server -dir /tmp/gtd -addr 127.0.0.1:8730
GTD_ENDPOINT=http://127.0.0.1:8730 go run ./cmd/gtd add "try it out"
```

Stdlib-only by design — this keeps `vendorHash = null` valid in the overlay and
means zero third-party supply-chain surface. Keep it that way unless there's a
strong reason; a new dependency would need a real `vendorHash` and a trust review.
