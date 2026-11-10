package main

import "testing"

func TestVersionLine(t *testing.T) {
	// go test binaries carry no vcs build info, so the build part is "(unknown build)".
	for _, c := range []struct{ linked, module, want string }{
		{"v0.4.0", "", "fleet v0.4.0"},
		{"v0.4.0", "v0.3.0", "fleet v0.4.0"},
		{"", "v0.3.0", "fleet v0.3.0"},
		{"", "(devel)", "fleet (unknown build)"},
		{"", "", "fleet (unknown build)"},
	} {
		if got := versionLine(c.linked, c.module); got != c.want {
			t.Errorf("versionLine(%q, %q) = %q, want %q", c.linked, c.module, got, c.want)
		}
	}
}

func TestShortVersion(t *testing.T) {
	if got := shortVersion("v1.2.0", "v1.1.0"); got != "v1.2.0" {
		t.Errorf("linked tag: %q", got)
	}
	if got := shortVersion("", "v1.1.0"); got != "v1.1.0" {
		t.Errorf("module version: %q", got)
	}
	// go test binaries carry no vcs info: no version rather than "(unknown build)".
	if got := shortVersion("", "(devel)"); got != "" {
		t.Errorf("unknown build: %q", got)
	}
}
