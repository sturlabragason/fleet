package mcp

import "github.com/sturlabragason/fleet/internal/rules"

// checkRule refuses a rule=true note that could not become a rule (too long).
func checkRule(rule bool, text string) error {
	if !rule {
		return nil
	}
	_, err := rules.Normalize(text)
	return err
}

// ruleNote adds a fleet_note rule=true to the user's playbook layer, so every
// later spawn gets it (ADR-131); out is the note's own result.
func ruleNote(out, home, text string) (string, error) {
	r, added, err := rules.Add(rules.UserFile(home), text)
	if err != nil {
		return out + "; not a rule: " + err.Error(), nil
	}
	if !added {
		return out + "; already a standing rule: " + r, nil
	}
	return out + "; standing rule, now in every new spawn's playbook (fleet rule ls)", nil
}
