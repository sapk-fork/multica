package handler

import (
	"strings"
	"testing"
)

func TestValidateBranchName(t *testing.T) {
	tests := []struct {
		name    string
		field   string
		value   string
		wantErr bool
		wantSub string // substring expected in the error when wantErr
	}{
		{"valid feature branch", "git_work_branch", "feature/my-branch", false, ""},
		{"valid with dots and slash", "git_work_branch", "fix.issue.123/sub", false, ""},
		{"empty allowed for work", "git_work_branch", "", false, ""},
		{"empty allowed for base", "git_base_branch", "", false, ""},
		{"HEAD forbidden for work", "git_work_branch", "HEAD", true, "HEAD"},
		{"HEAD forbidden for base", "git_base_branch", "HEAD", true, "HEAD"},
		{"main forbidden for work branch", "git_work_branch", "main", true, "integration branch"},
		{"master forbidden for work branch", "git_work_branch", "master", true, "integration branch"},
		{"main ok for base branch", "git_base_branch", "main", false, ""},
		{"master ok for base branch", "git_base_branch", "master", false, ""},
		{"double dot rejected", "git_work_branch", "feat..branch", true, ".."},
		{"at brace rejected", "git_work_branch", "feat@{branch}", true, "invalid characters"},
		{"leading dash rejected", "git_work_branch", "-bad", true, "start with '-'"},
		{"space rejected", "git_work_branch", "feat branch", true, "invalid characters"},
		{"unicode rejected", "git_work_branch", "feat/naïve", true, "invalid characters"},
		{"too long rejected", "git_work_branch", strings.Repeat("a", 201), true, "200 characters"},
		{"max length accepted", "git_work_branch", strings.Repeat("a", 200), false, ""},
		{"slash only accepted for base", "git_base_branch", "release/v1.0", false, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateBranchName(tt.field, tt.value)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("validateBranchName(%q, %q) = nil, want error", tt.field, tt.value)
				}
				if !strings.Contains(err.Error(), tt.wantSub) {
					t.Errorf("validateBranchName(%q, %q) err=%q, want substring %q", tt.field, tt.value, err.Error(), tt.wantSub)
				}
				return
			}
			if err != nil {
				t.Errorf("validateBranchName(%q, %q) = %v, want nil", tt.field, tt.value, err)
			}
		})
	}
}
