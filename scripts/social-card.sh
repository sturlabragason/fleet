#!/usr/bin/env bash
# Build docs/img/social.png (1280x640), the GitHub social preview, from
# docs/img/hero.png and the lockup. Upload it by hand under Settings > Social
# preview; the API has no endpoint for it. Run scripts/screenshot.sh first.
# Needs ImageMagick (magick) with SVG support.
set -eu
cd "$(dirname "$0")/.."
BG='#1e1e2e' FG='#cdd6f4'
FONT=/usr/share/fonts/adwaita-mono-fonts/AdwaitaMono-Regular.ttf
D=$(mktemp -d)
trap 'rm -r "$D"' EXIT
magick -background none -density 192 docs/img/lockup-on-ink.svg -resize x72 "$D/lockup.png"
magick docs/img/hero.png -resize 1200x "$D/hero.png"
magick -size 1280x640 "xc:$BG" \
  "$D/lockup.png" -geometry +40+32 -composite \
  -font "$FONT" -fill "$FG" -pointsize 32 -annotate +300+80 'One window. The whole fleet.' \
  "$D/hero.png" -geometry +40+130 -composite \
  -strip -depth 8 docs/img/social.png
echo "wrote docs/img/social.png"
