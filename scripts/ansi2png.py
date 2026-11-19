#!/usr/bin/env python3
"""Render a `tmux capture-pane -e -p` dump to PNG on a fixed character grid.

usage: ansi2png.py in.ans out.png [font-size] [cols] [rows]

Give cols and rows to pin the grid: every frame of an animation then comes out
the same size, whatever the panes happen to hold.
Needs Pillow and fontTools. Used by scripts/screenshot.sh for docs/img.
"""
import re
import sys
import unicodedata

from fontTools.ttLib import TTFont
from PIL import Image, ImageDraw, ImageFont

FONTS = [  # first font that has the glyph wins
    "/usr/share/fonts/adwaita-mono-fonts/AdwaitaMono-Regular.ttf",
    "/usr/share/fonts/liberation-mono-fonts/LiberationMono-Regular.ttf",
    "/usr/share/fonts/google-noto-vf/NotoSansMono[wght].ttf",
    "/usr/share/fonts/google-noto/NotoSansSymbols2-Regular.ttf",
    "/usr/share/fonts/google-noto-vf/NotoSansSymbols[wght].ttf",
    "/usr/share/fonts/google-noto/NotoSansMath-Regular.ttf",
]
BOLD = "/usr/share/fonts/adwaita-mono-fonts/AdwaitaMono-Bold.ttf"
# The ground and the default text are a pinned copy of the TUI's Base and Text
# (Lane today, ADR-166), so the padding around the capture is the same colour
# as the terminal inside it. It is a copy, not a read: if internal/theme changes
# its Base or Text, edit these two tuples by hand and re-run scripts/screenshot.sh.
# No window chrome, no shadow, no rounded corners: a screenshot is a terminal,
# not a device (docs/style.md §5).
BG, FG = (22, 22, 22), (244, 244, 244)
BASE16 = [(0, 0, 0), (205, 49, 49), (13, 188, 121), (229, 229, 16), (36, 114, 200), (188, 63, 188), (17, 168, 205), (229, 229, 229),
          (102, 102, 102), (241, 76, 76), (35, 209, 139), (245, 245, 67), (59, 142, 234), (214, 112, 214), (41, 184, 219), (255, 255, 255)]


def c256(n):
    if n < 16:
        return BASE16[n]
    if n < 232:
        n -= 16
        lv = [0, 95, 135, 175, 215, 255]
        return (lv[n // 36], lv[(n // 6) % 6], lv[n % 6])
    g = 8 + (n - 232) * 10
    return (g, g, g)


# Box-drawing glyphs are drawn, not typeset, the way a terminal does it: each
# arm runs from the cell centre to the cell edge, so lines join across cells.
# Arms are (up, down, left, right), 1 light and 2 heavy; ╭╮╰╯ get an arc.
BOX = {}
for chars, arms in [
    ("─━", [(0, 0, 1, 1), (0, 0, 2, 2)]), ("│┃", [(1, 1, 0, 0), (2, 2, 0, 0)]),
    ("┌┐└┘", [(0, 1, 0, 1), (0, 1, 1, 0), (1, 0, 0, 1), (1, 0, 1, 0)]),
    ("┏┓┗┛", [(0, 2, 0, 2), (0, 2, 2, 0), (2, 0, 0, 2), (2, 0, 2, 0)]),
    ("╭╮╰╯", [(0, 1, 0, 1), (0, 1, 1, 0), (1, 0, 0, 1), (1, 0, 1, 0)]),
    ("├┤┬┴┼", [(1, 1, 0, 1), (1, 1, 1, 0), (0, 1, 1, 1), (1, 0, 1, 1), (1, 1, 1, 1)]),
    ("┣┫┳┻╋", [(2, 2, 0, 2), (2, 2, 2, 0), (0, 2, 2, 2), (2, 0, 2, 2), (2, 2, 2, 2)]),
    ("┿╂", [(1, 1, 2, 2), (2, 2, 1, 1)]),
]:
    for c, a in zip(chars, arms):
        BOX[c] = a
ROUND = "╭╮╰╯"


def draw_box(d, c, px, py, cw, ch, f):
    up, down, left, right = BOX[c]
    cx, cy = px + cw // 2, py + ch // 2
    w = {1: max(1, cw // 9), 2: max(2, cw // 4)}
    if c in ROUND:  # quarter arc of radius r, then straight to the edges
        r = cw // 2
        t = w[1]
        ox = cx + r if right else cx - r
        oy = cy + r if down else cy - r
        start = {(0, 1, 0, 1): 180, (0, 1, 1, 0): 270, (1, 0, 0, 1): 90, (1, 0, 1, 0): 0}[(up, down, left, right)]
        d.arc((ox - r, oy - r, ox + r - 1, oy + r - 1), start, start + 90, fill=f, width=t)
        if down:
            d.rectangle((cx - t // 2, oy, cx - t // 2 + t - 1, py + ch - 1), fill=f)
        if up:
            d.rectangle((cx - t // 2, py, cx - t // 2 + t - 1, oy), fill=f)
        if right and ox < px + cw - 1:
            d.rectangle((ox, cy - t // 2, px + cw - 1, cy - t // 2 + t - 1), fill=f)
        if left and ox > px:
            d.rectangle((px, cy - t // 2, ox, cy - t // 2 + t - 1), fill=f)
        return
    for on, box in ((up, lambda t: (cx - t // 2, py, cx - t // 2 + t - 1, cy + t // 2)),
                    (down, lambda t: (cx - t // 2, cy - t // 2, cx - t // 2 + t - 1, py + ch - 1)),
                    (left, lambda t: (px, cy - t // 2, cx + t // 2, cy - t // 2 + t - 1)),
                    (right, lambda t: (cx - t // 2, cy - t // 2, px + cw - 1, cy - t // 2 + t - 1))):
        if on:
            d.rectangle(box(w[on]), fill=f)


# Block elements as the share of the cell they fill (top, bottom), drawn
# edge to edge like a terminal does, so half-block text has no seams.
BLOCK = {"█": (0, 1), "▀": (0, 0.5), "▄": (0.5, 1)}


def parse(text):
    """Yield rows of (char, fg, bg, bold, dim) cells."""
    rows = []
    fg, bg, bold, dim, rev = None, None, False, False, False
    for line in text.split("\n"):
        row = []
        for tok in re.split(r"(\x1b\[[0-9;:]*m)", line):
            if tok.startswith("\x1b["):
                ps = [int(p or 0) for p in re.split(r"[;:]", tok[2:-1])] or [0]
                i = 0
                while i < len(ps):
                    p = ps[i]
                    if p == 0:
                        fg, bg, bold, dim, rev = None, None, False, False, False
                    elif p == 1:
                        bold = True
                    elif p == 2:
                        dim = True
                    elif p == 22:
                        bold = dim = False
                    elif p == 7:
                        rev = True
                    elif p == 27:
                        rev = False
                    elif 30 <= p <= 37:
                        fg = BASE16[p - 30]
                    elif 90 <= p <= 97:
                        fg = BASE16[p - 82]
                    elif 40 <= p <= 47:
                        bg = BASE16[p - 40]
                    elif 100 <= p <= 107:
                        bg = BASE16[p - 92]
                    elif p == 39:
                        fg = None
                    elif p == 49:
                        bg = None
                    elif p in (38, 48) and i + 1 < len(ps):
                        if ps[i + 1] == 5 and i + 2 < len(ps):
                            col, i = c256(ps[i + 2]), i + 2
                        elif ps[i + 1] == 2 and i + 4 < len(ps):
                            col, i = tuple(ps[i + 2:i + 5]), i + 4
                        else:
                            col = None
                        if p == 38:
                            fg = col
                        else:
                            bg = col
                    i += 1
                continue
            for ch in tok:
                f, b = fg or FG, bg
                if rev:
                    f, b = (bg or BG), (fg or FG)
                row.append((ch, f, b, bold, dim))
                if unicodedata.east_asian_width(ch) in "WF":
                    row.append(("", f, b, bold, dim))
        rows.append(row)
    while rows and not rows[-1]:
        rows.pop()
    return rows


def main():
    src, dst = sys.argv[1], sys.argv[2]
    size = int(sys.argv[3]) if len(sys.argv) > 3 else 14
    grid = (int(sys.argv[4]), int(sys.argv[5])) if len(sys.argv) > 5 else None
    rows = parse(open(src, encoding="utf-8").read())
    if grid:
        rows = (rows + [[]] * grid[1])[:grid[1]]
    fonts = [(ImageFont.truetype(p, size), set(TTFont(p, fontNumber=0).getBestCmap())) for p in FONTS]
    bold = ImageFont.truetype(BOLD, size)
    cw = round(fonts[0][0].getlength("M"))
    ch = round(size * 1.3)
    pad = round(size * 1.4)
    cols = grid[0] if grid else max(len(r) for r in rows)
    img = Image.new("RGB", (cols * cw + 2 * pad, len(rows) * ch + 2 * pad), BG)
    d = ImageDraw.Draw(img)
    for y, row in enumerate(rows):
        for x, (c, f, b, bo, di) in enumerate(row):
            px, py = pad + x * cw, pad + y * ch
            if b:
                d.rectangle((px, py, px + cw - 1, py + ch - 1), fill=b)
            if not c or c == " ":
                continue
            if di:
                f = tuple((v + bb) // 2 for v, bb in zip(f, b or BG))
            if c in BOX:
                draw_box(d, c, px, py, cw, ch, f)
                continue
            if c in BLOCK:
                top, bot = BLOCK[c]
                d.rectangle((px, py + round(ch * top), px + cw - 1, py + round(ch * bot) - 1), fill=f)
                continue
            font = None
            for fnt, cmap in fonts:
                if ord(c) in cmap:
                    font = fnt
                    break
            if font is None:
                continue
            if bo and font is fonts[0][0]:
                font = bold
            d.text((px, py + (ch - size) // 2 - 1), c, font=font, fill=f)
    img.save(dst, optimize=True)


if __name__ == "__main__":
    main()
