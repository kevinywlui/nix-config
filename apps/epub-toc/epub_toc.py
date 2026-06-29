#!/usr/bin/env python3
"""epub-toc — add a table of contents to EPUB files that are missing one.

Standard-library only (no third-party deps), mirroring the zero-dependency
ethos of the other in-repo apps (cf. apps/gtd). The point is a tiny, auditable
tool with no external closure to pin — see nix/overlays/epub-toc.nix.

What "missing" means
--------------------
An EPUB carries its TOC in one or both of:

  * an EPUB 3 *nav document* — an XHTML file flagged ``properties="nav"`` in the
    OPF manifest, containing ``<nav epub:type="toc">``; and/or
  * an EPUB 2 *NCX* — ``toc.ncx`` (media-type application/x-dtbncx+xml),
    referenced from ``<spine toc="...">``.

A file is treated as *missing a TOC* when neither source yields at least
``--min-entries`` navigable links (default 2 — a one-entry TOC is not useful).

How the TOC is generated
------------------------
Without rewriting any content document (so existing links can never break), we
walk the spine in reading order and, for each XHTML document, emit one
top-level entry titled by its first heading (``<h1>``..``<h6>``), falling back
to its ``<title>`` or a prettified filename. Headings *that already carry an
id* become nested sub-entries linking to the fragment; headings without an id
are not linked individually (we never inject ids into content). We then write a
fresh nav document **and** an NCX (both, for maximum reader compatibility),
register them in the OPF, and repackage the zip with ``mimetype`` stored first
and uncompressed as the spec requires.

The operation is idempotent: a file that already has a usable TOC is skipped,
so the tool is safe to re-run over a whole library.
"""
from __future__ import annotations

import argparse
import html
import os
import posixpath
import sys
import tempfile
import zipfile
from dataclasses import dataclass, field
from html.parser import HTMLParser
from urllib.parse import unquote
from xml.etree import ElementTree as ET

CONTAINER_PATH = "META-INF/container.xml"
CONTAINER_NS = "urn:oasis:names:tc:opendocument:xmlns:container"
OPF_NS = "http://www.idpf.org/2007/opf"
DC_NS = "http://purl.org/dc/elements/1.1/"
NCX_NS = "http://www.daisy.org/z3986/2005/ncx/"
NCX_MEDIA_TYPE = "application/x-dtbncx+xml"
XHTML_MEDIA_TYPES = {"application/xhtml+xml", "text/html"}

HEADING_TAGS = {"h1", "h2", "h3", "h4", "h5", "h6"}


# --------------------------------------------------------------------------- #
# Parsing helpers
# --------------------------------------------------------------------------- #
@dataclass
class ManifestItem:
    item_id: str
    href: str            # as written in the OPF (relative to the OPF dir)
    media_type: str
    properties: str = ""


@dataclass
class Package:
    opf_path: str                       # zip path of the OPF
    opf_dir: str                        # zip dir containing the OPF
    version: str
    manifest: dict[str, ManifestItem]   # id -> item
    spine: list[str]                    # itemref ids, in reading order
    spine_toc: str | None               # value of <spine toc="...">
    uid: str                            # dc:identifier, for the NCX


@dataclass
class TocEntry:
    title: str
    href: str                           # zip path of the target document
    fragment: str | None = None
    children: list["TocEntry"] = field(default_factory=list)


class _HeadingParser(HTMLParser):
    """Collect (level, text, id) for each heading and the document <title>."""

    def __init__(self) -> None:
        super().__init__(convert_charrefs=True)
        self.headings: list[tuple[int, str, str | None]] = []
        self.title: str | None = None
        self._level = 0
        self._buf: list[str] = []
        self._cur_id: str | None = None
        self._in_title = False
        self._title_buf: list[str] = []

    def handle_starttag(self, tag, attrs):
        if tag in HEADING_TAGS:
            self._level = int(tag[1])
            self._buf = []
            self._cur_id = dict(attrs).get("id")
        elif tag == "title" and self._level == 0:
            self._in_title = True
            self._title_buf = []

    def handle_endtag(self, tag):
        if tag in HEADING_TAGS and self._level:
            text = " ".join("".join(self._buf).split())
            if text:
                self.headings.append((self._level, text, self._cur_id))
            self._level = 0
            self._buf = []
            self._cur_id = None
        elif tag == "title" and self._in_title:
            self._in_title = False
            text = " ".join("".join(self._title_buf).split())
            if text and self.title is None:
                self.title = text

    def handle_data(self, data):
        if self._level:
            self._buf.append(data)
        elif self._in_title:
            self._title_buf.append(data)


class _NavCounter(HTMLParser):
    """Count <a href> links inside a TOC <nav> of an EPUB 3 nav document."""

    def __init__(self) -> None:
        super().__init__(convert_charrefs=True)
        self.count = 0
        self._nav_depth = 0
        self._is_toc = False

    def handle_starttag(self, tag, attrs):
        a = dict(attrs)
        if tag == "nav":
            self._nav_depth += 1
            epub_type = a.get("epub:type", "")
            # A nav with no epub:type is ambiguous; treat it as the TOC too.
            self._is_toc = (not epub_type) or ("toc" in epub_type.split())
        elif tag == "a" and self._nav_depth and self._is_toc and a.get("href"):
            self.count += 1

    def handle_endtag(self, tag):
        if tag == "nav" and self._nav_depth:
            self._nav_depth -= 1
            if self._nav_depth == 0:
                self._is_toc = False


def _read_text(zf: zipfile.ZipFile, name: str) -> str:
    return zf.read(name).decode("utf-8", errors="replace")


def _resolve(opf_dir: str, href: str) -> str:
    """Resolve an OPF-relative href to a normalized zip path."""
    href = unquote(href.split("#", 1)[0])
    joined = posixpath.join(opf_dir, href) if opf_dir else href
    return posixpath.normpath(joined)


def parse_package(zf: zipfile.ZipFile) -> Package:
    container = ET.fromstring(zf.read(CONTAINER_PATH))
    rootfile = container.find(f".//{{{CONTAINER_NS}}}rootfile")
    if rootfile is None or not rootfile.get("full-path"):
        raise ValueError("container.xml has no rootfile")
    opf_path = rootfile.get("full-path")
    opf_dir = posixpath.dirname(opf_path)

    root = ET.fromstring(zf.read(opf_path))
    version = root.get("version", "2.0")

    manifest: dict[str, ManifestItem] = {}
    for item in root.findall(f"{{{OPF_NS}}}manifest/{{{OPF_NS}}}item"):
        item_id = item.get("id")
        href = item.get("href")
        if not item_id or not href:
            continue
        manifest[item_id] = ManifestItem(
            item_id=item_id,
            href=href,
            media_type=item.get("media-type", ""),
            properties=item.get("properties", ""),
        )

    spine_el = root.find(f"{{{OPF_NS}}}spine")
    spine: list[str] = []
    spine_toc = None
    if spine_el is not None:
        spine_toc = spine_el.get("toc")
        for itemref in spine_el.findall(f"{{{OPF_NS}}}itemref"):
            idref = itemref.get("idref")
            if idref:
                spine.append(idref)

    uid_el = root.find(f".//{{{DC_NS}}}identifier")
    uid = (uid_el.text or "").strip() if uid_el is not None else ""

    return Package(
        opf_path=opf_path,
        opf_dir=opf_dir,
        version=version,
        manifest=manifest,
        spine=spine,
        spine_toc=spine_toc,
        uid=uid or "urn:uuid:epub-toc",
    )


def _nav_item(pkg: Package) -> ManifestItem | None:
    for item in pkg.manifest.values():
        if "nav" in item.properties.split():
            return item
    return None


def _ncx_item(pkg: Package) -> ManifestItem | None:
    if pkg.spine_toc and pkg.spine_toc in pkg.manifest:
        return pkg.manifest[pkg.spine_toc]
    for item in pkg.manifest.values():
        if item.media_type == NCX_MEDIA_TYPE:
            return item
    return None


def count_existing_entries(zf: zipfile.ZipFile, pkg: Package) -> int:
    """Largest entry count across an existing nav doc and NCX (0 if none)."""
    best = 0

    nav = _nav_item(pkg)
    if nav is not None:
        try:
            parser = _NavCounter()
            parser.feed(_read_text(zf, _resolve(pkg.opf_dir, nav.href)))
            best = max(best, parser.count)
        except (KeyError, ValueError):
            pass

    ncx = _ncx_item(pkg)
    if ncx is not None:
        try:
            root = ET.fromstring(zf.read(_resolve(pkg.opf_dir, ncx.href)))
            best = max(best, len(root.findall(f".//{{{NCX_NS}}}navPoint")))
        except (KeyError, ET.ParseError):
            pass

    return best


# --------------------------------------------------------------------------- #
# TOC construction
# --------------------------------------------------------------------------- #
def _prettify_filename(zip_path: str) -> str:
    stem = posixpath.splitext(posixpath.basename(zip_path))[0]
    return stem.replace("_", " ").replace("-", " ").strip() or stem


def build_entries(zf: zipfile.ZipFile, pkg: Package) -> list[TocEntry]:
    entries: list[TocEntry] = []
    for idref in pkg.spine:
        item = pkg.manifest.get(idref)
        if item is None or item.media_type not in XHTML_MEDIA_TYPES:
            continue
        doc_path = _resolve(pkg.opf_dir, item.href)
        try:
            parser = _HeadingParser()
            parser.feed(_read_text(zf, doc_path))
        except (KeyError, ValueError):
            continue

        if parser.headings:
            title = parser.headings[0][1]
        else:
            title = parser.title or _prettify_filename(doc_path)

        entry = TocEntry(title=title, href=doc_path)
        for _level, text, hid in parser.headings[1:]:
            if hid:
                entry.children.append(
                    TocEntry(title=text, href=doc_path, fragment=hid)
                )
        entries.append(entry)
    return entries


def _href_from(toc_path: str, entry: TocEntry) -> str:
    toc_dir = posixpath.dirname(toc_path)
    rel = posixpath.relpath(entry.href, toc_dir or ".")
    if entry.fragment:
        rel += "#" + entry.fragment
    return rel


def render_nav(toc_path: str, entries: list[TocEntry]) -> bytes:
    def render_list(items: list[TocEntry], indent: str) -> str:
        out = [f"{indent}<ol>"]
        for it in items:
            href = html.escape(_href_from(toc_path, it), quote=True)
            label = html.escape(it.title)
            if it.children:
                out.append(f'{indent}  <li><a href="{href}">{label}</a>')
                out.append(render_list(it.children, indent + "  "))
                out.append(f"{indent}  </li>")
            else:
                out.append(f'{indent}  <li><a href="{href}">{label}</a></li>')
        out.append(f"{indent}</ol>")
        return "\n".join(out)

    body = render_list(entries, "    ")
    return (
        '<?xml version="1.0" encoding="utf-8"?>\n'
        '<!DOCTYPE html>\n'
        '<html xmlns="http://www.w3.org/1999/xhtml" '
        'xmlns:epub="http://www.idpf.org/2007/ops" lang="en">\n'
        "<head>\n"
        '  <meta charset="utf-8"/>\n'
        "  <title>Table of Contents</title>\n"
        "</head>\n"
        "<body>\n"
        '  <nav epub:type="toc" id="toc" role="doc-toc">\n'
        "    <h1>Table of Contents</h1>\n"
        f"{body}\n"
        "  </nav>\n"
        "</body>\n"
        "</html>\n"
    ).encode("utf-8")


def render_ncx(toc_path: str, entries: list[TocEntry], uid: str,
               doc_title: str) -> bytes:
    counter = [0]

    def render_points(items: list[TocEntry], indent: str) -> str:
        out = []
        for it in items:
            counter[0] += 1
            order = counter[0]
            src = html.escape(_href_from(toc_path, it), quote=True)
            label = html.escape(it.title)
            out.append(f'{indent}<navPoint id="navpoint-{order}" '
                       f'playOrder="{order}">')
            out.append(f"{indent}  <navLabel><text>{label}</text></navLabel>")
            out.append(f'{indent}  <content src="{src}"/>')
            if it.children:
                out.append(render_points(it.children, indent + "  "))
            out.append(f"{indent}</navPoint>")
        return "\n".join(out)

    depth = 1 + (1 if any(e.children for e in entries) else 0)
    body = render_points(entries, "    ")
    return (
        '<?xml version="1.0" encoding="utf-8"?>\n'
        '<!DOCTYPE ncx PUBLIC "-//NISO//DTD ncx 2005-1//EN" '
        '"http://www.daisy.org/z3986/2005/ncx/ncx-2005-1.dtd">\n'
        '<ncx xmlns="http://www.daisy.org/z3986/2005/ncx/" version="2005-1">\n'
        "  <head>\n"
        f'    <meta name="dtb:uid" content="{html.escape(uid, quote=True)}"/>\n'
        f'    <meta name="dtb:depth" content="{depth}"/>\n'
        '    <meta name="dtb:totalPageCount" content="0"/>\n'
        '    <meta name="dtb:maxPageNumber" content="0"/>\n'
        "  </head>\n"
        f"  <docTitle><text>{html.escape(doc_title)}</text></docTitle>\n"
        "  <navMap>\n"
        f"{body}\n"
        "  </navMap>\n"
        "</ncx>\n"
    ).encode("utf-8")


# --------------------------------------------------------------------------- #
# OPF rewriting + repackaging
# --------------------------------------------------------------------------- #
def _unique_id(manifest: dict[str, ManifestItem], base: str) -> str:
    if base not in manifest:
        return base
    n = 1
    while f"{base}-{n}" in manifest:
        n += 1
    return f"{base}-{n}"


def rewrite_opf(zf: zipfile.ZipFile, pkg: Package,
                nav_path: str, ncx_path: str) -> bytes:
    """Return updated OPF bytes registering the nav doc and NCX."""
    ET.register_namespace("", OPF_NS)
    ET.register_namespace("dc", DC_NS)
    root = ET.fromstring(zf.read(pkg.opf_path))
    manifest_el = root.find(f"{{{OPF_NS}}}manifest")
    spine_el = root.find(f"{{{OPF_NS}}}spine")

    nav_href = posixpath.relpath(nav_path, pkg.opf_dir or ".")
    ncx_href = posixpath.relpath(ncx_path, pkg.opf_dir or ".")

    # nav document: reuse an existing nav item, else add one.
    nav = _nav_item(pkg)
    if nav is None:
        nid = _unique_id(pkg.manifest, "nav-epub-toc")
        el = ET.SubElement(manifest_el, f"{{{OPF_NS}}}item")
        el.set("id", nid)
        el.set("href", nav_href)
        el.set("media-type", "application/xhtml+xml")
        el.set("properties", "nav")

    # NCX: reuse an existing one, else add it and wire up <spine toc="...">.
    ncx = _ncx_item(pkg)
    if ncx is None:
        ncx_id = _unique_id(pkg.manifest, "ncx-epub-toc")
        el = ET.SubElement(manifest_el, f"{{{OPF_NS}}}item")
        el.set("id", ncx_id)
        el.set("href", ncx_href)
        el.set("media-type", NCX_MEDIA_TYPE)
    else:
        ncx_id = ncx.item_id
    if spine_el is not None and spine_el.get("toc") != ncx_id:
        spine_el.set("toc", ncx_id)

    xml = ET.tostring(root, encoding="unicode")
    return ('<?xml version="1.0" encoding="utf-8"?>\n' + xml + "\n").encode("utf-8")


def repackage(src: str, dst: str, replacements: dict[str, bytes]) -> None:
    """Copy src->dst applying replacements; mimetype stored first."""
    with zipfile.ZipFile(src) as zin:
        names = zin.namelist()
        with zipfile.ZipFile(dst, "w") as zout:
            mimetype = b"application/epub+zip"
            if "mimetype" in names:
                mimetype = zin.read("mimetype")
            info = zipfile.ZipInfo("mimetype")
            info.compress_type = zipfile.ZIP_STORED
            zout.writestr(info, mimetype)

            written = {"mimetype"}
            for name in names:
                if name == "mimetype":
                    continue
                data = replacements.get(name, zin.read(name))
                zout.writestr(name, data, zipfile.ZIP_DEFLATED)
                written.add(name)
            for name, data in replacements.items():
                if name not in written:
                    zout.writestr(name, data, zipfile.ZIP_DEFLATED)


# --------------------------------------------------------------------------- #
# Per-file driver
# --------------------------------------------------------------------------- #
@dataclass
class Result:
    path: str
    status: str          # "added" | "present" | "empty" | "error"
    detail: str = ""


def process_file(path: str, *, min_entries: int, check: bool,
                 backup: bool) -> Result:
    try:
        with zipfile.ZipFile(path) as zf:
            pkg = parse_package(zf)
            existing = count_existing_entries(zf, pkg)
            if existing >= min_entries:
                return Result(path, "present",
                              f"{existing} existing entries")

            entries = build_entries(zf, pkg)
            if not entries:
                return Result(path, "empty",
                              "no spine documents to build a TOC from")

            nav_path = posixpath.normpath(
                posixpath.join(pkg.opf_dir, "nav.xhtml")
                if pkg.opf_dir else "nav.xhtml"
            )
            ncx_path = posixpath.normpath(
                posixpath.join(pkg.opf_dir, "toc.ncx")
                if pkg.opf_dir else "toc.ncx"
            )
            existing_nav = _nav_item(pkg)
            existing_ncx = _ncx_item(pkg)
            if existing_nav is not None:
                nav_path = _resolve(pkg.opf_dir, existing_nav.href)
            if existing_ncx is not None:
                ncx_path = _resolve(pkg.opf_dir, existing_ncx.href)

            doc_title = pkg.uid
            replacements = {
                nav_path: render_nav(nav_path, entries),
                ncx_path: render_ncx(ncx_path, entries, pkg.uid, doc_title),
                pkg.opf_path: rewrite_opf(zf, pkg, nav_path, ncx_path),
            }
    except (zipfile.BadZipFile, ValueError, ET.ParseError, KeyError) as exc:
        return Result(path, "error", str(exc))

    n = len(entries)
    if check:
        return Result(path, "added", f"would add TOC with {n} entries")

    directory = os.path.dirname(os.path.abspath(path))
    fd, tmp = tempfile.mkstemp(suffix=".epub", dir=directory)
    os.close(fd)
    try:
        repackage(path, tmp, replacements)
        if backup:
            os.replace(path, path + ".bak")
        os.replace(tmp, path)
    except Exception:
        if os.path.exists(tmp):
            os.remove(tmp)
        raise
    return Result(path, "added", f"added TOC with {n} entries")


def iter_epubs(paths: list[str], recursive: bool):
    for p in paths:
        if os.path.isdir(p):
            if recursive:
                for dirpath, _dirs, files in os.walk(p):
                    for name in sorted(files):
                        if name.lower().endswith(".epub"):
                            yield os.path.join(dirpath, name)
            else:
                for name in sorted(os.listdir(p)):
                    full = os.path.join(p, name)
                    if name.lower().endswith(".epub") and os.path.isfile(full):
                        yield full
        else:
            yield p


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(
        prog="epub-toc",
        description="Add a table of contents to EPUBs that are missing one.",
    )
    parser.add_argument("paths", nargs="+",
                        help="EPUB files or directories to process")
    parser.add_argument("-r", "--recursive", action="store_true",
                        help="recurse into directories looking for .epub files")
    parser.add_argument("--min-entries", type=int, default=2, metavar="N",
                        help="treat a TOC with fewer than N entries as missing "
                             "(default: 2)")
    parser.add_argument("--check", action="store_true",
                        help="report what would change without writing; exit "
                             "non-zero if any file is missing a TOC")
    parser.add_argument("--backup", action="store_true",
                        help="keep the original alongside as <file>.bak")
    parser.add_argument("-q", "--quiet", action="store_true",
                        help="only print files that were changed or errored")
    args = parser.parse_args(argv)

    counts = {"added": 0, "present": 0, "empty": 0, "error": 0}
    for path in iter_epubs(args.paths, args.recursive):
        res = process_file(path, min_entries=args.min_entries,
                           check=args.check, backup=args.backup)
        counts[res.status] += 1
        if res.status == "present" and args.quiet:
            continue
        marker = {
            "added": "+", "present": "=", "empty": "·", "error": "!",
        }[res.status]
        line = f"{marker} {path}"
        if res.detail:
            line += f"  ({res.detail})"
        out = sys.stderr if res.status == "error" else sys.stdout
        print(line, file=out)

    summary = (f"{counts['added']} added, {counts['present']} present, "
               f"{counts['empty']} empty, {counts['error']} errors")
    print(summary, file=sys.stderr)

    if counts["error"]:
        return 2
    if args.check and counts["added"]:
        return 1
    return 0


if __name__ == "__main__":
    sys.exit(main())
