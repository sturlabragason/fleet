package tools

import "strings"

// ModelArg is the model a launch argv selects: the value of the last
// --model (or --model=X) across the arg lists, in launch order, so a later
// list (the user's extra_args) wins. "" = none set, the tool's own default.
func ModelArg(lists ...[]string) string {
	m := ""
	for _, args := range lists {
		for i, a := range args {
			if v, ok := strings.CutPrefix(a, "--model="); ok {
				m = v
			} else if a == "--model" && i+1 < len(args) {
				m = args[i+1]
			}
		}
	}
	return m
}
