package util

import "testing"

func TestIsTerminalIssueStatus(t *testing.T) {
	tests := []struct {
		name   string
		status string
		want   bool
	}{
		{"done is terminal", "done", true},
		{"cancelled is terminal", "cancelled", true},
		{"archived is terminal", "archived", true},
		{"backlog is not terminal", "backlog", false},
		{"todo is not terminal", "todo", false},
		{"in_progress is not terminal", "in_progress", false},
		{"in_review is not terminal", "in_review", false},
		{"blocked is not terminal", "blocked", false},
		{"empty string is not terminal", "", false},
		{"unknown status is not terminal", "snoozed", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsTerminalIssueStatus(tt.status); got != tt.want {
				t.Errorf("IsTerminalIssueStatus(%q) = %v, want %v", tt.status, got, tt.want)
			}
		})
	}
}
