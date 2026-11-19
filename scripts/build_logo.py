"""Build the fleet logo files (docs/img/logo*.svg, lockup*.svg).

Style: square frame, ink lines that cross and resolve into parallel lanes,
one blue lane, IBM Plex Mono Bold wordmark converted to outlines.
Run: python3 scripts/build_logo.py /path/to/IBMPlexMono-Bold.ttf docs/img
"""
import sys

INK, PAPER, LANE = "#161616", "#FFFFFF", "#0033CC"
INK_D, PAPER_D, LANE_D = "#F4F4F4", "#161616", "#8AA4FF"

# The geometry of the mark, in the 96x96 icon's units. scripts/build_ascii_logo.py
# reads it too, so the terminal logo is drawn from the same lines.
STARTS = [70, 28, 62, 36, 54]  # tangled entry heights
LANES = [28, 38, 48, 58, 68]   # resolved lanes
ACCENT = 2                     # the blue lane
LANE_END = 80                  # x of the square stops


def strand(i):
    """The SVG path of strand i: in at STARTS[i], out on LANES[i]."""
    a, b = STARTS[i], LANES[i]
    return f"M8,{a} C22,{a} 26,{STARTS[(i+2)%5]} 34,{(a+b)/2} S44,{b} 52,{b} H{LANE_END}"


def mark(ink, paper, lane, frame):
    # 96x96 icon. Frame stroke 4, square corners.
    starts, lanes, accent = STARTS, LANES, ACCENT
    out = [f'<rect x="2" y="2" width="92" height="92" fill="{paper}" stroke="{frame}" stroke-width="4"/>']
    for i, (a, b) in enumerate(zip(starts, lanes)):
        col, w = (lane, 2.6) if i == accent else (ink, 1.6)
        d = strand(i)
        out.append(f'<path d="{d}" fill="none" stroke="{col}" stroke-width="{w}"/>')
        s = 5 if i == accent else 4
        out.append(f'<rect x="{80-s/2}" y="{b-s/2}" width="{s}" height="{s}" fill="{col}"/>')
    return '<g clip-path="url(#c)">' + "".join(out[1:]) + "</g>" + out[0].replace(f'fill="{paper}"', 'fill="none"'), out[0]

def word(text, size, left, baseline, color):
    from fontTools.pens.svgPathPen import SVGPathPen
    from fontTools.pens.transformPen import TransformPen
    gs, cmap, upm = FONT.getGlyphSet(), FONT.getBestCmap(), FONT["head"].unitsPerEm
    s = size / upm
    pen = SVGPathPen(gs)
    x = left
    for ch in text:
        g = gs[cmap[ord(ch)]]
        g.draw(TransformPen(pen, (s, 0, 0, -s, x, baseline)))
        x += g.width * s
    return f'<path d="{pen.getCommands()}" fill="{color}"/>', x

def svg(w, h, body, defs=True):
    clip = '<defs><clipPath id="c"><rect x="4" y="4" width="88" height="88"/></clipPath></defs>' if defs else ""
    return f'<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 {w} {h}" width="{w}" height="{h}" role="img" aria-label="fleet"><title>fleet</title>{clip}{body}</svg>\n'

def main():
    from fontTools.ttLib import TTFont
    global FONT
    FONT = TTFont(sys.argv[1])
    out = sys.argv[2]
    for suffix, (ink, paper, lane, frame) in {"": (INK, PAPER, LANE, "#000000"), "-on-ink": (INK_D, PAPER_D, LANE_D, INK_D)}.items():
        lines, bg = mark(ink, paper, lane, frame)
        icon = bg + lines
        open(f"{out}/logo{suffix}.svg", "w").write(svg(96, 96, icon))
        p, x = word("fleet", 64, 120, 70, frame if suffix else "#000000")
        open(f"{out}/lockup{suffix}.svg", "w").write(svg(int(x) + 8, 96, icon + p))


if __name__ == "__main__":
    main()
