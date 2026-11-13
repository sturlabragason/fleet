package mcp

import (
	"fmt"
	"strings"
	"time"

	"github.com/sturlabragason/fleet/internal/conf"
	"github.com/sturlabragason/fleet/internal/routes"
	"github.com/sturlabragason/fleet/internal/supervisor"
)

// Request routes (ADR-120): the admiral matches a request to a route from
// the brief's index; fleet stores the routes and fills a spawn from one.

var routeToolDef = tool{"fleet_route", "Request routes: where and how a recurring kind of request runs (dir, profile, effort, skill, may, interactive, check, prompt template), kept in the user's local routes file. The brief lists them by name and trigger. list; show (name): the whole card; set (name, fields): create or change a route, e.g. when the user corrects one (\"no, that goes to X\"): save it at once with reason = the user's words; rm (name). fleet_spawn route=NAME applies a route.", obj([]string{"action"}, map[string]any{
	"action": map[string]any{"type": "string", "enum": []string{"list", "show", "set", "rm"}},
	"name":   str("route name: lowercase letters, digits, - and _"),
	"fields": map[string]any{"type": "object", "description": "for set: the fields to change, e.g. {\"dir\": \"/abs/repo\", \"triggers\": [\"check my mail\"]}; \"\" unsets one. Fields: " + strings.Join(routes.Fields, ", ") + ". triggers and may are lists; worktree and interactive are true/false; effort is light, standard or pro; prompt is a template where {task} stands for the spawn's prompt (without {task} it goes before it); skill adds \"Use the <skill> skill\" to the prompt."},
	"reason": str("for set: why, in the user's words (kept on the route as learned_from and in the decisions log)"),
})}

func init() { toolDefs = append(toolDefs, routeToolDef) }

func (s *Server) route(a args) (string, error) {
	home := s.F.Home
	switch a.Action {
	case "", "list":
		rs, err := routes.Load(home)
		if err != nil {
			return "", err
		}
		if len(rs) == 0 {
			return "no routes yet: fleet_route action=set adds one", nil
		}
		return strings.TrimRight(routes.Index(rs), "\n"), nil
	case "show":
		if a.Name == "" {
			return "", fmt.Errorf("name is required")
		}
		r, err := routes.Get(home, a.Name)
		if err != nil {
			return "", err
		}
		return r.Show(), nil
	case "set":
		if a.Name == "" {
			return "", fmt.Errorf("name is required")
		}
		if len(a.Fields) == 0 && strings.TrimSpace(a.Reason) == "" {
			return "", fmt.Errorf("fields is required, e.g. {\"dir\": \"/abs/repo\"}")
		}
		f, err := routeFields(a.Fields)
		if err != nil {
			return "", err
		}
		_, ch, err := routes.Set(home, a.Name, f, a.Reason, time.Now())
		if err != nil {
			return "", err
		}
		if len(ch) == 0 {
			return "route " + a.Name + ": nothing changed", nil
		}
		var parts []string
		for _, c := range ch {
			if c.Field == "prompt" {
				parts = append(parts, "prompt changed")
				continue
			}
			parts = append(parts, fmt.Sprintf("%s %s → %s", c.Field, short(c.Old), short(c.New)))
		}
		line := "route " + a.Name + ": " + strings.Join(parts, "; ")
		if r := strings.TrimSpace(a.Reason); r != "" {
			line += " (" + r + ")"
		}
		if err := supervisor.AppendNoteBy(home, s.Me(), "", oneLine(line)); err != nil {
			return "", err
		}
		return "saved " + line, nil
	case "rm":
		if a.Name == "" {
			return "", fmt.Errorf("name is required")
		}
		if err := routes.Remove(home, a.Name); err != nil {
			return "", err
		}
		return "removed route " + a.Name, supervisor.AppendNoteBy(home, s.Me(), "", "route "+a.Name+" removed")
	}
	return "", fmt.Errorf("action must be list, show, set or rm")
}

// routeFields turns JSON values into route values: strings, booleans and
// numbers as text, arrays as lists, null as unset.
func routeFields(in map[string]any) (map[string]conf.Value, error) {
	out := map[string]conf.Value{}
	for k, v := range in {
		switch x := v.(type) {
		case nil:
			out[k] = ""
		case string:
			out[k] = x
		case bool:
			out[k] = fmt.Sprint(x)
		case float64:
			out[k] = fmt.Sprint(x)
		case []any:
			l := []string{}
			for _, e := range x {
				s, ok := e.(string)
				if !ok {
					return nil, fmt.Errorf("%s: want a list of strings", k)
				}
				l = append(l, s)
			}
			out[k] = l
		default:
			return nil, fmt.Errorf("%s: want a string, true/false or a list of strings", k)
		}
	}
	return out, nil
}

// applyRoute fills a fleet_spawn's unset arguments from a.Route. It returns
// the line the spawn reply ends with: the route used, or a hint to save one.
func (s *Server) applyRoute(a *args) (string, error) {
	if a.Route == "" {
		return " — no route used; if this kind of request recurs, save it with fleet_route action=set", nil
	}
	r, err := routes.Get(s.F.Home, a.Route)
	if err != nil {
		return "", err
	}
	fill := func(p *string, v string) {
		if *p == "" {
			*p = v
		}
	}
	fill(&a.Dir, routes.ExpandDir(r.Dir))
	fill(&a.Profile, r.Profile)
	fill(&a.Effort, r.Effort)
	fill(&a.Model, r.Model)
	fill(&a.Check, r.Check)
	if a.Worktree == nil && r.Worktree != "" {
		w := r.Worktree == "true"
		a.Worktree = &w
	}
	if a.May == nil {
		a.May = r.May
	}
	if !a.interactiveSet && r.Interactive != "" {
		a.Interactive = r.Interactive == "true"
	}
	a.Prompt = r.PromptFor(a.Prompt)
	return " — route " + r.Name, nil
}

// short is a changed value for the log line: (none) when unset, cut to 60.
func short(s string) string {
	if s == "" {
		return "(none)"
	}
	if r := []rune(s); len(r) > 60 {
		return string(r[:59]) + "…"
	}
	return s
}

func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }

// routesBrief is fleet_brief's route index: names and one-line triggers.
func (s *Server) routesBrief() string {
	rs, err := routes.Load(s.F.Home)
	if err != nil {
		return "## Routes\n" + err.Error() + "\n\n"
	}
	if len(rs) == 0 {
		return ""
	}
	return "## Routes (match the request here before asking where; fleet_spawn route=NAME, fleet_route show NAME)\n" + routes.Index(rs) + "\n"
}
