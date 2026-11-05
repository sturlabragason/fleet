#!/usr/bin/env python3
"""Fold an HTML page's local assets into the file itself, so it renders anywhere.

usage: inline-html.py in.html out.html [--embed-pages]
       inline-html.py in.html --in-place [--embed-pages]

A sandboxed browser (Flatpak, snap) opened through xdg-open gets the single file
handed to it via the document portal as file:///run/user/.../doc/<id>/page.html.
Only that one file is exported, so every relative <img>, <link> and <script>
404s. Inlining them as data: URIs is what makes a page survive the trip.

Handled: <img>/<source>/<script>/<video>/<audio>/<embed> src, srcset,
<link rel=stylesheet>, and url(...) in <style> blocks, style="" attributes and
inlined stylesheets. With --embed-pages, a link to a local .html under the page's
own directory becomes an in-page <iframe srcdoc> section, so navigation works too.

Afterwards the output is rescanned: any local reference still left (unsupported
syntax, @import, <object data>, a link that was not embedded) is printed and the
exit code is 1, so a half-inlined page can never ship quietly. Assets outside the
page's own directory, and absolute paths, are refused rather than embedded.

Stdlib only; no network. Remote URLs and existing data: URIs are left alone.
"""
import base64
import html
import mimetypes
import os
import re
import sys

MAX_BYTES = 30 * 1024 * 1024  # over this, serve the folder over http instead

mimetypes.add_type("image/svg+xml", ".svg")
mimetypes.add_type("font/woff2", ".woff2")

# `[^>]` would stop at a `>` inside a quoted attribute value, so spell out that
# an attribute value may contain anything but its own quote.
ATTRS = r"""(?:[^>"']|"[^"]*"|'[^']*')*?"""
ATTR_RE = re.compile(
    r"""(?P<tag><(?P<name>img|source|script|video|audio|embed)\b%s)"""
    r"""(?P<attr>\ssrc\s*=\s*)(?P<q>["'])(?P<url>[^"']*)(?P=q)""" % ATTRS,
    re.I | re.S,
)
LINKTAG_RE = re.compile(r"""<link\b%s/?>""" % ATTRS, re.I | re.S)
HREF_RE = re.compile(r"""\shref\s*=\s*(?P<q>["'])(?P<url>[^"']*)(?P=q)""", re.I)
RELSTYLE_RE = re.compile(r"""\srel\s*=\s*["']?[^"'>]*\bstylesheet\b""", re.I)
SRCSET_RE = re.compile(
    r"""(?P<attr>\ssrcset\s*=\s*)(?P<q>["'])(?P<val>[^"']*)(?P=q)""", re.I
)
STYLE_BLOCK_RE = re.compile(r"""(<style\b[^>]*>)(.*?)(</style>)""", re.I | re.S)
STYLE_ATTR_RE = re.compile(r"""(\sstyle\s*=\s*)(["'])([^"']*)(\2)""", re.I)
URLFUNC_RE = re.compile(r"""url\(\s*(?P<q>["']?)(?P<url>[^"')]+)(?P=q)\s*\)""", re.I)
PAGELINK_RE = re.compile(
    r"""<a\b%s\shref\s*=\s*(?P<q>["'])(?P<url>[^"']+\.html)(?P=q)""" % ATTRS, re.I
)
# Final gate: anything local still pointing outside the file.
LEFTOVER_RE = re.compile(
    r"""(?:\s(?:src|href|data)\s*=\s*["']([^"']+)["'])"""
    r"""|(?:url\(\s*["']?([^"')]+))"""
    r"""|(?:@import\s+["']([^"']+)["'])""",
    re.I,
)
# A marker no HTML can contain, so stylesheets we already inlined are not
# rescanned later against the wrong base directory.
MARK = "\x00inlined-%d\x00"
MARK_RE = re.compile(r"\x00inlined-(\d+)\x00")


def is_local(url):
    """A reference that must be inlined to survive being handed over alone."""
    url = url.strip()
    if not url or url.startswith(
        ("data:", "#", "//", "mailto:", "tel:", "javascript:")
    ):
        return False
    return "://" not in url


def read_text(path):
    with open(path, encoding="utf-8", errors="replace") as fh:
        return fh.read()


def data_uri(path):
    mime = mimetypes.guess_type(path)[0] or "application/octet-stream"
    with open(path, "rb") as fh:
        return "data:%s;base64,%s" % (
            mime, base64.b64encode(fh.read()).decode("ascii")
        )


class Inliner:
    """Rewrites one page. `root` is the boundary: nothing outside it is embedded."""

    def __init__(self, root):
        self.root = os.path.realpath(root)
        self.missing = []
        self.refused = []
        self.inlined = []
        self.blocks = []  # already-inlined stylesheets, held out of later passes

    def resolve(self, base_dir, url):
        """Path for a local url, or None if unusable or outside root."""
        ref = url.strip().split("#", 1)[0].split("?", 1)[0]
        if not ref:
            return None
        if ref.startswith("/") or (len(ref) > 1 and ref[1] == ":"):
            self.refused.append("%s (absolute path)" % url)
            return None
        path = os.path.realpath(os.path.join(base_dir, ref))
        if path != self.root and not path.startswith(self.root + os.sep):
            self.refused.append("%s (outside %s)" % (url, self.root))
            return None
        if not os.path.isfile(path):
            self.missing.append(url)
            return None
        return path

    def stash(self, text):
        self.blocks.append(text)
        return MARK % (len(self.blocks) - 1)

    def unstash(self, text):
        return MARK_RE.sub(lambda m: self.blocks[int(m.group(1))], text)

    def css(self, css, base_dir):
        def sub(m):
            url = m.group("url")
            if not is_local(url):
                return m.group(0)
            path = self.resolve(base_dir, url)
            if path is None:
                return m.group(0)
            self.inlined.append(url)
            return "url(%s)" % data_uri(path)

        return URLFUNC_RE.sub(sub, css)

    def stylesheets(self, text, base_dir):
        """<link rel=stylesheet href=...> becomes a stashed <style> block."""

        def sub(m):
            tag = m.group(0)
            href = HREF_RE.search(tag)
            if (
                not href
                or not RELSTYLE_RE.search(tag)
                or not is_local(href.group("url"))
            ):
                return tag
            path = self.resolve(base_dir, href.group("url"))
            if path is None:
                return tag
            self.inlined.append(href.group("url"))
            css = self.css(read_text(path), os.path.dirname(path))
            return self.stash("<style>%s</style>" % css)

        return LINKTAG_RE.sub(sub, text)

    def srcset(self, text, base_dir):
        def sub(m):
            out = []
            for cand in m.group("val").split(","):
                parts = cand.strip().split(None, 1)
                if not parts:
                    continue
                url = parts[0]
                if is_local(url):
                    path = self.resolve(base_dir, url)
                    if path is not None:
                        self.inlined.append(url)
                        url = data_uri(path)
                out.append(" ".join([url] + parts[1:]))
            return "%s%s%s%s" % (
                m.group("attr"), m.group("q"), ", ".join(out), m.group("q")
            )

        return SRCSET_RE.sub(sub, text)

    def attrs(self, text, base_dir):
        def sub(m):
            url = m.group("url")
            if not is_local(url):
                return m.group(0)
            path = self.resolve(base_dir, url)
            if path is None:
                return m.group(0)
            self.inlined.append(url)
            return "%s%s%s%s%s" % (
                m.group("tag"), m.group("attr"), m.group("q"),
                data_uri(path), m.group("q"),
            )

        return ATTR_RE.sub(sub, text)

    def page(self, text, base_dir):
        """Every asset pass for one document, in order."""
        text = self.stylesheets(text, base_dir)
        text = self.attrs(text, base_dir)
        text = self.srcset(text, base_dir)
        text = STYLE_BLOCK_RE.sub(
            lambda m: m.group(1) + self.css(m.group(2), base_dir) + m.group(3), text
        )
        return STYLE_ATTR_RE.sub(
            lambda m: m.group(1) + m.group(2) + self.css(m.group(3), base_dir)
            + m.group(4),
            text,
        )

    def anchor_id(self, path):
        rel = os.path.relpath(path, self.root)
        return "embedded-" + re.sub(r"\W+", "-", rel).strip("-").lower()

    def embed_pages(self, text, base_dir, src):
        """Local .html links become in-page <iframe srcdoc> sections."""
        targets, seen = [], {os.path.realpath(src)}
        for m in PAGELINK_RE.finditer(text):
            url = m.group("url")
            if not is_local(url):
                continue
            path = self.resolve(base_dir, url)
            if path is None or path in seen:
                continue
            seen.add(path)
            targets.append((url, path))
        sections = []
        for url, path in targets:
            sub = self.unstash(self.page(read_text(path), os.path.dirname(path)))
            self.inlined.append(url)
            anchor = self.anchor_id(path)
            sections.append(
                '<section id="%s"><h2>%s</h2>'
                '<iframe srcdoc="%s" style="width:100%%;height:86vh;border:1px solid '
                '#23262c;border-radius:6px;background:#000"></iframe></section>'
                % (anchor, html.escape(os.path.relpath(path, self.root)),
                   html.escape(sub, quote=True))
            )
            href_re = re.compile(
                r"""(<a\b%s\shref\s*=\s*)(?P<q>["'])%s(?P=q)"""
                % (ATTRS, re.escape(url)), re.I
            )
            text = href_re.sub(
                lambda m, a=anchor: "%s%s#%s%s"
                % (m.group(1), m.group("q"), a, m.group("q")),
                text,
            )
        if not sections:
            return text
        block = "".join(sections)
        low = text.lower()
        if "</body>" in low:
            i = low.rindex("</body>")
            return text[:i] + block + text[i:]
        return text + block

    def leftovers(self, text):
        """Local references that survived: the page is not self-contained."""
        out = []
        for m in LEFTOVER_RE.finditer(text):
            url = m.group(1) or m.group(2) or m.group(3)
            if url and is_local(url) and url not in out:
                out.append(url)
        return out


def usage(stream=sys.stdout, code=0):
    print(__doc__.strip(), file=stream)
    return code


def main(argv):
    rest = argv[1:]
    args = [a for a in rest if not a.startswith("-")]
    flags = {a for a in rest if a.startswith("-")}
    if flags & {"--help", "-h"}:
        return usage()
    unknown = flags - {"--embed-pages", "--in-place"}
    if unknown:
        print("unknown option: %s" % ", ".join(sorted(unknown)), file=sys.stderr)
        return usage(sys.stderr, 2)
    if not args or (len(args) < 2 and "--in-place" not in flags):
        print("need an input page and an output path (or --in-place)", file=sys.stderr)
        return usage(sys.stderr, 2)

    src = os.path.abspath(args[0])
    dst = os.path.abspath(args[1]) if len(args) > 1 else src
    base_dir = os.path.dirname(src)
    inl = Inliner(base_dir)
    text = read_text(src)
    if "--embed-pages" in flags:
        text = inl.embed_pages(text, base_dir, src)
    text = inl.unstash(inl.page(text, base_dir))

    left = inl.leftovers(text)
    with open(dst, "w", encoding="utf-8") as fh:
        fh.write(text)
    size = os.path.getsize(dst)
    print("%s: %d assets inlined, %.1f MB"
          % (dst, len(inl.inlined), size / (1024 * 1024)))

    bad = False
    for label, items in (
        ("missing", inl.missing), ("refused", inl.refused), ("not inlined", left)
    ):
        for url in dict.fromkeys(items):
            print("  %s: %s" % (label, url), file=sys.stderr)
            bad = True
    if size > MAX_BYTES:
        print("  too big: %.1f MB over the %d MB limit; serve the folder with "
              "`python3 -m http.server` instead"
              % (size / (1024 * 1024), MAX_BYTES // (1024 * 1024)), file=sys.stderr)
        bad = True
    return 1 if bad else 0


if __name__ == "__main__":
    sys.exit(main(sys.argv))
