package util

// IsTerminalIssueStatus reports whether an issue status is terminal (closed).
// Terminal statuses are done, cancelled, and archived.
// Use this instead of open-coding the set at each call site — adding a new
// terminal status only requires changing this function.
func IsTerminalIssueStatus(status string) bool {
	switch status {
	case "done", "cancelled", "archived":
		return true
	default:
		return false
	}
}
