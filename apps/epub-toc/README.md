# apps/epub-toc — add a table of contents to EPUBs missing one

A small Python program (standard library only) that scans EPUB files and, for
any that lack a usable table of contents, generates one and writes it back into
the file. It is packaged for NixOS as `pkgs.epub-toc`
(`nix/overlays/epub-toc.nix`) and wired into the Calibre/Syncthing book pipeline
in `nix/modules/home/profiles/core.nix`, so books synced to the e-readers
(Boox, Pixel) get a navigable TOC even when the source file shipped without one.

## Why

Many EPUBs — especially ones produced by ad-hoc conversion — ship with no
navigation. On a phone that is a minor annoyance; on an e-reader it means no
chapter list and no jump-to-chapter at all. This tool repairs them in place.

## What counts as "missing"

An EPUB stores its TOC in one or both of:

| Source                | Where                                                        |
| --------------------- | ----------------------------------------------------------- |
| EPUB 3 *nav document* | an XHTML file marked `properties="nav"` in the OPF manifest, containing `<nav epub:type="toc">` |
| EPUB 2 *NCX*          | `toc.ncx` (`application/x-dtbncx+xml`), referenced from `<spine toc="…">` |

A file is treated as **missing a TOC** when neither source yields at least
`--min-entries` links (default `2` — a one-entry TOC is not useful). Files that
already have a real TOC are left byte-for-byte untouched, so the tool is safe to
re-run over an entire library.

## How the TOC is built

Without modifying any content document (so existing internal links can never
break), the tool walks the spine in reading order and, for each XHTML document,
emits:

- one **top-level entry** titled by the document's first heading (`<h1>`..`<h6>`),
  falling back to its `<title>` element, then a prettified filename; and
- **nested sub-entries** for any heading that *already* carries an `id`
  (linked as `file#id`). Headings without an id are not linked individually —
  the tool never injects ids into content.

It then writes a fresh nav document **and** an NCX (both, for maximum reader
compatibility), registers them in the OPF manifest/spine, and repackages the zip
with `mimetype` stored first and uncompressed, as the EPUB spec requires. Writes
are atomic (temp file + rename in the same directory).

## Usage

```console
$ epub-toc book.epub                 # repair a single file in place
$ epub-toc -r /var/lib/syncthing/books   # recurse a directory tree
$ epub-toc --check -r books/         # report only; exit 1 if any need a TOC
$ epub-toc --backup book.epub        # keep the original as book.epub.bak
```

Output marks each file: `+` added, `=` already present, `·` nothing to build
from, `!` error. `--quiet` suppresses the `=` lines. Exit status is `0` on
success, `1` in `--check` mode when something would change, `2` on errors.

## Development

Standard library only — no third-party dependencies (matching the
zero-dependency ethos of `apps/gtd`). Run the tests with:

```console
$ python3 -m unittest discover -s apps/epub-toc -v
```

The Nix package (`nix/overlays/epub-toc.nix`) runs the same suite as its
`checkPhase`, so a regression fails `nh os build`.
