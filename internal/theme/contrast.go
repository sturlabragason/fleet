package theme

import (
	"math"
	"strconv"
)

// Contrast is the WCAG 2.1 contrast ratio between two #rrggbb colours, 1 for
// anything it cannot parse. docs/style.md §4 fixes the minimum per role; the
// palette test enforces it for every theme.
func Contrast(fg, bg string) float64 {
	a, b := luminance(fg), luminance(bg)
	if a < b {
		a, b = b, a
	}
	return (a + 0.05) / (b + 0.05)
}

func luminance(hex string) float64 {
	if len(hex) != 7 || hex[0] != '#' {
		return 0
	}
	ch := func(i int) float64 {
		v, err := strconv.ParseUint(hex[i:i+2], 16, 8)
		if err != nil {
			return 0
		}
		c := float64(v) / 255
		if c <= 0.03928 {
			return c / 12.92
		}
		return math.Pow((c+0.055)/1.055, 2.4)
	}
	return 0.2126*ch(1) + 0.7152*ch(3) + 0.0722*ch(5)
}
