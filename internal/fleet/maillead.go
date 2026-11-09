package fleet

// leadOf is the agent that leads to's team: its squad leader, or a squad
// leader in its team. "" when it has none (fleet#35).
func (f *Fleet) leadOf(to string) string {
	ag := f.Store.Load().Agents
	a, ok := ag[to]
	if !ok {
		return ""
	}
	if _, ok := ag[a.LeadBy]; ok && a.LeadBy != "" {
		return a.LeadBy
	}
	if f.Teams == nil {
		return ""
	}
	for _, m := range f.Teams.Teammates(to) {
		if l, ok := ag[m]; ok && l.Leader {
			return m
		}
	}
	return ""
}

// mailHint is the next step a "no access" refusal names when from may not
// message to: to's lead, when from may reach that lead freely.
func (f *Fleet) mailHint(policy, from, to string, repos map[string]string) string {
	l := f.leadOf(to)
	if l == "" || l == from || f.mailReason(policy, from, l, repos) == "" {
		return ""
	}
	return "message " + l + " (lead of " + to + "'s team), who you may reach freely"
}
