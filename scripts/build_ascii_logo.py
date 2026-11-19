"""Build the terminal logo (internal/logo/art.go) from the SVG mark's geometry.

usage: python3 scripts/build_ascii_logo.py IBMPlexMono-Bold.ttf > internal/logo/art.go

The splash mark is drawn from the strands of docs/img/logo.svg (build_logo.py),
metro-style: a strand holds a row and turns at right angles. Lanes sit on even
rows and entries and bends on odd rows, so two strands only ever share a cell
as a clean crossing (┼), never a junction. ROWS lists the rows each strand
holds, read off its turning points; the turn columns are searched near where
the curve really crosses, so the picture keeps the SVG's shape. The blue lane
is heavy, as its stroke is in the SVG. The word is IBM Plex Mono Bold
rasterised to half blocks. The 3-row mark and the 1-row line are hand-drawn
reductions (three rows cannot hold a clean crossing between lanes).
Needs Pillow. See docs/style.md §3 and DESIGN.md ADR-166.
"""
import itertools
import sys

from PIL import Image, ImageDraw, ImageFont

from build_logo import ACCENT, LANE_END, LANES, STARTS

XU = 3.0                    # SVG units per column
ROW_Y = lambda r: 18 + 5 * r  # SVG y at the centre of row r; lanes 28..68 land on rows 2..10
ROWS = {0: [11, 2], 1: [1, 4], 2: [9, 6], 3: [3, 7, 5, 8], 4: [7, 5, 12, 10]}
SPREAD = 3                  # columns a turn may move from its geometric place
STOP = round((LANE_END - 8) / XU)
H = 14                      # rows inside the frame
WORD_PX, WORD_THRESHOLD = 14, 110


def bez(p0, p1, p2, p3, t):
    u = 1 - t
    return tuple(u**3 * a + 3 * u * u * t * b + 3 * u * t * t * c + t**3 * d
                 for a, b, c, d in zip(p0, p1, p2, p3))


def curve(i):
    """y(x) along strand i: the same two cubics as build_logo.strand."""
    a, b, s = STARTS[i], LANES[i], STARTS[(i + 2) % 5]
    m = (a + b) / 2
    pts = [bez((8, a), (22, a), (26, s), (34, m), k / 2000) for k in range(2001)]
    pts += [bez((34, m), (42, 2 * m - s), (44, b), (52, b), k / 2000) for k in range(2001)]
    xs = [8 + k * 0.25 for k in range(177)]
    return xs, [min(pts, key=lambda p: abs(p[0] - x))[1] for x in xs]


def geo_turns(i):
    """Column where strand i passes midway between each pair of its rows."""
    xs, ys = curve(i)
    rows, out, start = ROWS[i], [], 0
    for k in range(len(rows) - 1):
        mid, d = (ROW_Y(rows[k]) + ROW_Y(rows[k + 1])) / 2, rows[k + 1] - rows[k]
        for j in range(start, len(xs) - 1):
            if (ys[j] - mid) * (ys[j + 1] - mid) <= 0 and (ys[j + 1] - ys[j]) * d > 0:
                out.append(round((xs[j] - 8) / XU))
                start = j + 1
                break
        else:
            out.append(STOP - 3)
    return out


def build(plan):
    """{(row, col): {strand: arms}} for strands routed as plan {i: turn cols}."""
    E = {}

    def add(r, c, i, arm):
        E.setdefault((r, c), {}).setdefault(i, set()).add(arm)

    for i, cols in plan.items():
        rows, prev = ROWS[i], 0
        for k, r in enumerate(rows):
            end = cols[k] if k < len(cols) else STOP
            for c in range(prev, end + 1):
                if c == prev and k > 0:
                    continue
                if c == end and k < len(rows) - 1:
                    n = rows[k + 1]
                    d = 1 if n > r else -1
                    if c > 0 or k > 0:
                        add(r, c, i, "W")
                    add(r, c, i, "S" if d > 0 else "N")
                    for q in range(r + d, n, d):
                        add(q, c, i, "N")
                        add(q, c, i, "S")
                    add(n, c, i, "N" if d > 0 else "S")
                    add(n, c, i, "E")
                else:
                    if c > 0:
                        add(r, c, i, "W")
                    if c < STOP:
                        add(r, c, i, "E")
            prev = end
    return E


def clean(E):
    cross = {frozenset("NS"), frozenset("EW")}
    return all(len(d) == 1 or (len(d) == 2 and {frozenset(s) for s in d.values()} == cross)
               for d in E.values())


def solve():
    geo = {i: geo_turns(i) for i in ROWS}
    best = [None]

    def opts(i):
        for cs in itertools.product(*[range(b - SPREAD, b + SPREAD + 1) for b in geo[i]]):
            if all(a < b for a, b in zip(cs, cs[1:])) and cs[0] >= 1 and cs[-1] < STOP - 2:
                yield sum(abs(a - b) for a, b in zip(cs, geo[i])), list(cs)

    def rec(order, plan, cost):
        if best[0] and cost >= best[0][0]:
            return
        if not order:
            best[0] = (cost, dict(plan))
            return
        i = order[0]
        for c, cs in sorted(opts(i)):
            plan[i] = cs
            if clean(build(plan)):
                rec(order[1:], plan, cost + c)
            del plan[i]

    rec([0, 2, 1, 3, 4], {}, 0)
    if not best[0]:
        sys.exit("build_ascii_logo: no clean routing")
    return build(best[0][1])


LIGHT = {"EW": "─", "NS": "│", "ES": "╭", "SW": "╮", "NE": "╰", "NW": "╯", "E": "─", "W": "─"}
HEAVY = {"EW": "━", "NS": "┃", "ES": "┏", "SW": "┓", "NE": "┗", "NW": "┛", "E": "━", "W": "━"}


def glyph(d):
    """Glyph and role for one cell's strands: accent wins the colour."""
    acc = ACCENT in d
    if len(d) == 2:
        g = "┼" if not acc else ("┿" if d[ACCENT] == set("EW") else "╂")
    else:
        (i, arms), = d.items()
        g = (HEAVY if i == ACCENT else LIGHT)["".join(sorted(arms, key="NESW".index))]
    return g, "a" if acc else "i"


def mark():
    E = solve()
    W, L, R = STOP + 1, 1, 2
    rows, roles = ["┏" + "━" * (W + L + R) + "┓"], ["f" * (W + L + R + 2)]
    for r in range(H):
        g, o = "┃" + " " * L, "f" + " " * L
        for c in range(W):
            d = E.get((r, c))
            if not d:
                g, o = g + " ", o + " "
            elif c == STOP:
                g, o = g + ("■" if ACCENT in d else "▪"), o + ("a" if ACCENT in d else "i")
            else:
                gg, oo = glyph(d)
                g, o = g + gg, o + oo
        rows.append(g + " " * R + "┃")
        roles.append(o + " " * R + "f")
    rows.append("┗" + "━" * (W + L + R) + "┛")
    roles.append("f" * (W + L + R + 2))
    return rows, roles


def word(font_path):
    """'fleet' in IBM Plex Mono Bold, two pixels a row, as half blocks."""
    S = 8
    font = ImageFont.truetype(font_path, WORD_PX * S)
    l, t, r, b = font.getbbox("fleet")
    img = Image.new("L", (r - l + 2 * S, b - t + 2 * S), 0)
    ImageDraw.Draw(img).text((S - l, S - t), "fleet", font=font, fill=255)
    w, h = (img.width + S - 1) // S, (img.height + S - 1) // S
    small = Image.new("L", (w, h + h % 2), 0)
    small.paste(img.resize((w, h), Image.BOX), (0, 0))
    P = small.load()
    out = []
    for y in range(0, small.height, 2):
        out.append("".join(" ▀▄█"[(P[x, y] >= WORD_THRESHOLD) + 2 * (P[x, y + 1] >= WORD_THRESHOLD)]
                           for x in range(w)).rstrip())
    while out and not out[0].strip():
        out.pop(0)
    while out and not out[-1].strip():
        out.pop()
    lead = min(len(s) - len(s.lstrip()) for s in out if s.strip())
    return [s[lead:] for s in out]


def splash(font_path):
    rows, roles = mark()
    w = word(font_path)
    base = 1 + max(r[-1] for r in ROWS.values())   # frame row of the lowest lane
    for k, line in enumerate(w):
        r = base - len(w) + 1 + k
        rows[r] += "   " + line
        roles[r] += "   " + "".join(" " if ch == " " else "w" for ch in line)
    return rows, roles


MARK = [
    ("─╲ ╱───▪", "ii iiiii"),
    ("━━╳━━━━■   fleet", "aaiaaaaa   wwwww"),
    ("─╱ ╲───▪", "ii iiiii"),
]
LINE = [("╳━■ fleet", "iaa wwwww")]


def go_art(name, rows, roles):
    width = max(len(r) for r in rows)
    out = [f"var {name} = art{{", "\trows: []string{"]
    out += [f'\t\t"{r.ljust(width)}",' for r in rows]
    out += ["\t},", "\troles: []string{"]
    out += [f'\t\t"{r.ljust(width)}",' for r in roles]
    out += ["\t},", "}"]
    return "\n".join(out)


def main():
    if len(sys.argv) != 2:
        sys.exit(__doc__.split("\n\n")[1])
    print("// Code generated by scripts/build_ascii_logo.py; DO NOT EDIT.\n")
    print("package logo\n")
    print(go_art("lineArt", *zip(*LINE)) + "\n")
    print(go_art("markArt", *zip(*MARK)) + "\n")
    print(go_art("splashArt", *splash(sys.argv[1])))


if __name__ == "__main__":
    main()
