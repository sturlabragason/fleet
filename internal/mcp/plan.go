package mcp

const planParamDesc = "a plan (TOML, ADR-203) to start instead of one agent; name is the plan's name. One [task.<name>] section per task with profile, dir (absolute), prompt, optional check (done-check), after = [\"other-task\"] and review = { profile = \"<other vendor>\", max_rounds = 2 }. Fleet starts each task when its dependencies finish, within max_working. A task finishes when it reports done and its check passes; with review, a reviewer of another tool family reads the branch and its findings go back to the writer, up to max_rounds, then the task is blocked and you are told. The same plan again resumes it; fleet_status plan=NAME shows it"

// planCall starts or resumes a plan: fleet_spawn with plan set (ADR-203).
func (s *Server) planCall(a args) (string, error) {
	if err := need(a.Name, "name"); err != nil {
		return "", err
	}
	st, err := s.F.StartPlan(a.Name, a.Plan, s.Admiral)
	if err != nil {
		return "", err
	}
	return st.Summary(), nil
}
