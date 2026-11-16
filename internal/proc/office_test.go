package proc

import "testing"

func TestOffice(t *testing.T) {
	for comm, want := range map[string]bool{"soffice.bin": true, "oosplash": true, "soffice": true, "node": false, "office-helper": false} {
		if Office(comm) != want {
			t.Errorf("Office(%q) = %v, want %v", comm, !want, want)
		}
	}
}
