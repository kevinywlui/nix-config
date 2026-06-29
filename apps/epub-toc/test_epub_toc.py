#!/usr/bin/env python3
"""Tests for epub_toc. Stdlib unittest only — run with:

    python3 -m unittest discover -s apps/epub-toc
"""
import os
import tempfile
import unittest
import zipfile
from xml.etree import ElementTree as ET

import epub_toc

CONTAINER = """<?xml version="1.0"?>
<container version="1.0"
    xmlns="urn:oasis:names:tc:opendocument:xmlns:container">
  <rootfiles>
    <rootfile full-path="OEBPS/content.opf"
        media-type="application/oebps-package+xml"/>
  </rootfiles>
</container>
"""

CHAP1 = """<?xml version="1.0" encoding="utf-8"?>
<html xmlns="http://www.w3.org/1999/xhtml">
<head><title>The First Chapter</title></head>
<body><h1>Chapter One</h1><p>hi</p>
<h2 id="sec-a">Section A</h2><p>...</p></body></html>
"""

CHAP2 = """<?xml version="1.0" encoding="utf-8"?>
<html xmlns="http://www.w3.org/1999/xhtml">
<head><title>second</title></head>
<body><h1>Chapter Two</h1><p>bye</p></body></html>
"""


def _opf(*, with_ncx_entries=0):
    """Build an OPF; optionally reference a populated NCX so a TOC exists."""
    spine_toc = ' toc="ncx"' if with_ncx_entries else ""
    ncx_item = ('<item id="ncx" href="toc.ncx" '
                'media-type="application/x-dtbncx+xml"/>'
                if with_ncx_entries else "")
    return f"""<?xml version="1.0" encoding="utf-8"?>
<package xmlns="http://www.idpf.org/2007/opf" version="3.0" unique-identifier="bid">
  <metadata xmlns:dc="http://purl.org/dc/elements/1.1/">
    <dc:identifier id="bid">urn:uuid:test-123</dc:identifier>
    <dc:title>Test Book</dc:title>
  </metadata>
  <manifest>
    <item id="c1" href="c1.xhtml" media-type="application/xhtml+xml"/>
    <item id="c2" href="c2.xhtml" media-type="application/xhtml+xml"/>
    {ncx_item}
  </manifest>
  <spine{spine_toc}>
    <itemref idref="c1"/>
    <itemref idref="c2"/>
  </spine>
</package>
"""


def _ncx(n):
    points = "".join(
        f'<navPoint id="n{i}" playOrder="{i}">'
        f"<navLabel><text>Ch {i}</text></navLabel>"
        f'<content src="c{i}.xhtml"/></navPoint>'
        for i in range(1, n + 1)
    )
    return ('<?xml version="1.0"?>'
            '<ncx xmlns="http://www.daisy.org/z3986/2005/ncx/" '
            'version="2005-1"><navMap>' + points + "</navMap></ncx>")


def make_epub(path, *, with_ncx_entries=0):
    with zipfile.ZipFile(path, "w") as zf:
        info = zipfile.ZipInfo("mimetype")
        info.compress_type = zipfile.ZIP_STORED
        zf.writestr(info, "application/epub+zip")
        zf.writestr("META-INF/container.xml", CONTAINER)
        zf.writestr("OEBPS/content.opf", _opf(with_ncx_entries=with_ncx_entries))
        zf.writestr("OEBPS/c1.xhtml", CHAP1)
        zf.writestr("OEBPS/c2.xhtml", CHAP2)
        if with_ncx_entries:
            zf.writestr("OEBPS/toc.ncx", _ncx(with_ncx_entries))


class EpubTocTest(unittest.TestCase):
    def setUp(self):
        self.dir = tempfile.mkdtemp()

    def _path(self, name="book.epub"):
        return os.path.join(self.dir, name)

    def test_adds_toc_when_missing(self):
        p = self._path()
        make_epub(p)
        res = epub_toc.process_file(p, min_entries=2, check=False, backup=False)
        self.assertEqual(res.status, "added", res.detail)

        with zipfile.ZipFile(p) as zf:
            names = zf.namelist()
            self.assertIn("OEBPS/nav.xhtml", names)
            self.assertIn("OEBPS/toc.ncx", names)

            # mimetype must be the first entry and stored uncompressed.
            first = zf.infolist()[0]
            self.assertEqual(first.filename, "mimetype")
            self.assertEqual(first.compress_type, zipfile.ZIP_STORED)

            # nav has one top-level entry per chapter, titled by first heading.
            nav = zf.read("OEBPS/nav.xhtml").decode()
            self.assertIn("Chapter One", nav)
            self.assertIn("Chapter Two", nav)
            self.assertIn("c1.xhtml#sec-a", nav)  # id'd heading -> fragment
            self.assertIn('epub:type="toc"', nav)

            # ncx has navPoints.
            ncx = zf.read("OEBPS/toc.ncx").decode()
            root = ET.fromstring(ncx)
            ns = "{http://www.daisy.org/z3986/2005/ncx/}"
            self.assertGreaterEqual(len(root.findall(f".//{ns}navPoint")), 2)

            # OPF wired up: nav property + spine toc.
            opf = zf.read("OEBPS/content.opf").decode()
            self.assertIn('properties="nav"', opf)
            self.assertIn('toc=', opf)

    def test_idempotent(self):
        p = self._path()
        make_epub(p)
        epub_toc.process_file(p, min_entries=2, check=False, backup=False)
        res = epub_toc.process_file(p, min_entries=2, check=False, backup=False)
        self.assertEqual(res.status, "present", res.detail)

    def test_existing_toc_left_untouched(self):
        p = self._path()
        make_epub(p, with_ncx_entries=3)
        res = epub_toc.process_file(p, min_entries=2, check=False, backup=False)
        self.assertEqual(res.status, "present", res.detail)
        with zipfile.ZipFile(p) as zf:
            self.assertNotIn("OEBPS/nav.xhtml", zf.namelist())

    def test_check_mode_does_not_write(self):
        p = self._path()
        make_epub(p)
        before = os.path.getsize(p)
        res = epub_toc.process_file(p, min_entries=2, check=True, backup=False)
        self.assertEqual(res.status, "added")
        self.assertEqual(os.path.getsize(p), before)
        with zipfile.ZipFile(p) as zf:
            self.assertNotIn("OEBPS/nav.xhtml", zf.namelist())

    def test_produces_valid_zip_with_backup(self):
        p = self._path()
        make_epub(p)
        epub_toc.process_file(p, min_entries=2, check=False, backup=True)
        self.assertTrue(os.path.exists(p + ".bak"))
        with zipfile.ZipFile(p) as zf:
            self.assertIsNone(zf.testzip())


if __name__ == "__main__":
    unittest.main()
