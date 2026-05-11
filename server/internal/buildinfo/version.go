package buildinfo

import (
	"runtime/debug"
	"time"
)

// ResolveDevVersion upgrades a "dev" version string to a Go pseudo-version
// derived from VCS metadata embedded by the Go toolchain (Go 1.18+). Returns
// the input unchanged when it is not "dev". When VCS info is unavailable (e.g.
// Docker build without .git), returns "dev" unchanged.
//
// The pseudo-version format matches golang.org/x/mod/module.PseudoVersion:
//
//	v0.0.0-yyyymmddhhmmss-abcdef123456
func ResolveDevVersion(v string) string {
	if v != "dev" {
		return v
	}
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return v
	}
	var revision, vcsTime string
	for _, s := range info.Settings {
		switch s.Key {
		case "vcs.revision":
			revision = s.Value
		case "vcs.time":
			vcsTime = s.Value
		}
	}
	if revision == "" || vcsTime == "" {
		return v
	}
	t, err := time.Parse(time.RFC3339, vcsTime)
	if err != nil {
		return v
	}
	if len(revision) > 12 {
		revision = revision[:12]
	}
	return "v0.0.0-" + t.UTC().Format("20060102150405") + "-" + revision
}
