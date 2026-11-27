package routes

// Spawn is the part of a spawn request a route can fill. An empty string or
// a nil pointer or slice is unset; Fill leaves what is set alone, so explicit
// arguments win over the route.
type Spawn struct {
	Dir, Profile, Effort, Model, Check string
	Worktree, Interactive              *bool
	May                                []string
	Prompt                             string
}

// Fill sets s's unset fields from the route and wraps s.Prompt in the
// route's template. fleet_spawn route=NAME and fleet spawn --route NAME both
// go through it.
func (r Route) Fill(s *Spawn) {
	fill := func(p *string, v string) {
		if *p == "" {
			*p = v
		}
	}
	fill(&s.Dir, ExpandDir(r.Dir))
	fill(&s.Profile, r.Profile)
	fill(&s.Effort, r.Effort)
	fill(&s.Model, r.Model)
	fill(&s.Check, r.Check)
	flag := func(p **bool, v string) {
		if *p == nil && v != "" {
			b := v == "true"
			*p = &b
		}
	}
	flag(&s.Worktree, r.Worktree)
	flag(&s.Interactive, r.Interactive)
	if s.May == nil {
		s.May = r.May
	}
	s.Prompt = r.PromptFor(s.Prompt)
}
