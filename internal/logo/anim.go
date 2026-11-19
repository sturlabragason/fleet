package logo

import "strings"

// FrameCount is how many frames the start-up animation draws: at the player's
// FrameRate it runs for about 1.2 s, then the full splash holds briefly.
const FrameCount = 36

// headCells is how wide the accent sweep is, in columns.
const headCells = 2

// Frames is the splash drawn in over n frames, as the Copilot CLI draws its
// banner: a sweep runs left to right along the lanes, cells behind it take
// their own colours, the sweep's edge is lit in the accent and cells ahead of
// it are blank. The last frame is Render(s, asc), so the animation ends on the
// static mark (ADR-171).
func Frames(s Size, asc bool, n int) []string {
	n = max(n, 1)
	plain := Plain(s, asc)
	roles := s.art().roles
	w := s.Width()
	frames := make([]string, 0, n)
	for k := 1; k < n; k++ {
		edge := (k*(w+headCells) + n - 1) / n
		rows := make([]string, len(plain))
		for i, r := range plain {
			rows[i] = paint(sweep([]rune(r), []rune(roles[i]), edge))
		}
		frames = append(frames, strings.Join(rows, "\n"))
	}
	return append(frames, Render(s, asc))
}

// sweep blanks the cells at and past edge and gives the headCells columns
// before it the accent role; the cells stay as they are, so every frame has
// the drawing's full width.
func sweep(cells, roles []rune, edge int) ([]rune, []rune) {
	out := make([]rune, len(cells))
	rs := make([]rune, len(roles))
	for c := range cells {
		switch {
		case c >= edge:
			out[c], rs[c] = ' ', ' '
		case c >= edge-headCells && cells[c] != ' ':
			out[c], rs[c] = cells[c], 'a'
		default:
			out[c], rs[c] = cells[c], roles[c]
		}
	}
	return out, rs
}

// Gate is what decides whether the banner may move.
type Gate struct {
	Getenv   func(string) string
	TTY      bool // stdin and stdout are terminals
	Off      bool // [defaults] animation = false or --no-anim
	Explicit bool // the user asked for it (fleet logo): tmux is no reason to skip
}

// quietEnv turns the animation off when any of them is set: NO_COLOR and a
// reduced-motion opt-out by the user, CI, and an agent's pane, where the
// output is read by a tool rather than a person.
var quietEnv = []string{"NO_COLOR", "FLEET_NO_ANIM", "CI", "FLEET_AGENT"}

// Animate reports whether the banner may animate. Off whenever no person is
// watching or one asked for stillness: not a terminal, opted out, NO_COLOR,
// CI, TERM=dumb, an agent's pane, or (unless asked for) inside tmux, where
// fleet up is re-run on every attach and the animation would get in the way.
func Animate(g Gate) bool {
	if g.Off || !g.TTY || g.Getenv("TERM") == "dumb" {
		return false
	}
	for _, k := range quietEnv {
		if g.Getenv(k) != "" {
			return false
		}
	}
	return g.Explicit || g.Getenv("TMUX") == ""
}
