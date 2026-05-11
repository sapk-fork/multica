package buildinfo

import "testing"

func TestResolveDevVersion(t *testing.T) {
	t.Run("non-dev passes through", func(t *testing.T) {
		got := ResolveDevVersion("v0.2.20")
		if got != "v0.2.20" {
			t.Errorf("ResolveDevVersion(%q) = %q, want %q", "v0.2.20", got, "v0.2.20")
		}
	})

	t.Run("git-describe passes through", func(t *testing.T) {
		got := ResolveDevVersion("v0.2.15-235-gdaf0e935")
		if got != "v0.2.15-235-gdaf0e935" {
			t.Errorf("ResolveDevVersion(%q) = %q, want unchanged", "v0.2.15-235-gdaf0e935", got)
		}
	})

	t.Run("dev upgrades to pseudo-version", func(t *testing.T) {
		got := ResolveDevVersion("dev")
		if got == "dev" {
			t.Skip("no VCS info in build environment")
		}
		if len(got) < len("v0.0.0-20060102150405-000000000000") {
			t.Errorf("ResolveDevVersion(%q) = %q, too short for a pseudo-version", "dev", got)
		}
		if got[:7] != "v0.0.0-" {
			t.Errorf("ResolveDevVersion(%q) = %q, want pseudo-version prefix v0.0.0-", "dev", got)
		}
	})
}
