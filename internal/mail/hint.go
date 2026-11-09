package mail

// hint is Store.Hint as a refusal suffix: ", or <hint>", or "" when unset.
func (s *Store) hint(from, to string) string {
	if s.Hint == nil {
		return ""
	}
	if h := s.Hint(from, to); h != "" {
		return " (to " + to + "), or " + h
	}
	return ""
}
