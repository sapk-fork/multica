package backup

import (
	"encoding/json"
	"errors"
	"testing"
	"time"
)

func TestMarshalNil(t *testing.T) {
	if _, err := Marshal(nil); err == nil {
		t.Fatal("Marshal(nil) = nil error, want error")
	}
}

func TestMarshalUnmarshalRoundTrip(t *testing.T) {
	ts := time.Date(2026, 6, 9, 12, 0, 0, 0, time.UTC)
	due := time.Date(2026, 6, 20, 0, 0, 0, 0, time.UTC)
	start := time.Date(2026, 6, 10, 0, 0, 0, 0, time.UTC)
	agentArchived := time.Date(2026, 6, 8, 9, 0, 0, 0, time.UTC)
	squadArchived := time.Date(2026, 6, 7, 10, 0, 0, 0, time.UTC)
	commentResolved := time.Date(2026, 6, 9, 16, 0, 0, 0, time.UTC)
	issueCounter := int32(42)
	maxTasks := int32(3)
	resPosition := int32(1)
	original := &BackupFile{
		Metadata: BackupMetadata{
			Version:    FormatVersion,
			ExportedAt: ts,
		},
		Workspace: &BackupWorkspace{
			ID:           "ws-1",
			Name:         "Acme",
			Slug:         "acme",
			Description:  "the workspace",
			Context:      "be helpful",
			Settings:     json.RawMessage(`{"theme":"dark"}`),
			Repos:        json.RawMessage(`["https://example.com/repo"]`),
			IssuePrefix:  "ACME",
			IssueCounter: &issueCounter,
			AvatarURL:    "https://example.com/ws.png",
			CreatedAt:    ts,
		},
		Members: []BackupMember{{
			ID:                 "user-1",
			Name:               "Alice",
			Email:              "alice@example.com",
			AvatarURL:          "https://example.com/a.png",
			Role:               "admin",
			Language:           "en",
			Timezone:           "America/New_York",
			ProfileDescription: "Backend developer",
		}},
		Skills: []BackupSkill{{
			ID:          "skill-1",
			Name:        "lint",
			Description: "runs the linter",
			Content:     "# Lint\n",
			Config:      json.RawMessage(`{"auto_fix":true}`),
			Files:       []BackupSkillFile{{Path: "scripts/run.sh", Content: "echo hi"}},
			CreatedBy:   &BackupActor{Type: "member", ID: "user-1"},
			CreatedAt:   ts,
		}},
		Agents: []BackupAgent{{
			ID:                 "agent-1",
			Name:               "Go Developer",
			RuntimeMode:        "local",
			RuntimeConfig:      json.RawMessage(`{"image":"go"}`),
			Visibility:         "workspace",
			SkillIDs:           []string{"skill-1"},
			CustomEnv:          json.RawMessage(`{"GOFLAGS":"-mod=mod"}`),
			CustomArgs:         json.RawMessage(`["--verbose"]`),
			McpConfig:          json.RawMessage(`{"servers":[]}`),
			Model:              "claude-opus-4-8",
			ThinkingLevel:      "high",
			AvatarURL:          "https://example.com/agent.png",
			OwnerID:            "user-1",
			MaxConcurrentTasks: &maxTasks,
			ArchivedAt:         &agentArchived,
			CreatedAt:          ts,
		}},
		Labels: []BackupLabel{{ID: "label-1", Name: "bug", Color: "#f00", CreatedAt: ts}},
		Projects: []BackupProject{{
			ID:       "proj-1",
			Title:    "Backup feature",
			Priority: "high",
			Lead:     &BackupActor{Type: "member", ID: "user-1"},
			Resources: []BackupProjectResource{{
				ID:           "res-1",
				ResourceType: "github_repo",
				ResourceRef:  json.RawMessage(`{"url":"https://example.com"}`),
				Label:        "repo",
				Position:     &resPosition,
			}},
			CreatedAt: ts,
		}},
		Issues: []BackupIssue{{
			ID:       "issue-1",
			Number:   23,
			Title:    "Define backup format",
			Status:   "in_progress",
			Priority: "medium",
			Assignee: &BackupActor{Type: "agent", ID: "agent-1"},
			Creator:  &BackupActor{Type: "agent", ID: "agent-2"},
			LabelIDs: []string{"label-1"},
			Comments: []BackupComment{{
				ID:         "comment-1",
				Author:     BackupActor{Type: "agent", ID: "agent-2"},
				Content:    "please do this",
				CreatedAt:  time.Date(2026, 6, 9, 15, 15, 0, 0, time.UTC),
				Reactions:  []BackupReaction{{Actor: BackupActor{Type: "agent", ID: "agent-1"}, Emoji: "👍"}},
				ResolvedAt: &commentResolved,
				ResolvedBy: &BackupActor{Type: "member", ID: "user-1"},
			}},
			Metadata:           json.RawMessage(`{"pr_url":"https://example.com/pr/1"}`),
			Reactions:          []BackupReaction{{Actor: BackupActor{Type: "member", ID: "user-1"}, Emoji: "🚀"}},
			Position:           -5,
			DueDate:            &due,
			StartDate:          &start,
			AcceptanceCriteria: json.RawMessage(`["builds","tests pass"]`),
			ContextRefs:        json.RawMessage(`[{"kind":"issue","id":"issue-0"}]`),
			OriginType:         "autopilot",
			OriginID:           "ap-1",
			CreatedAt:          ts,
		}},
		Squads: []BackupSquad{{
			ID:           "squad-1",
			Name:         "Core",
			Leader:       &BackupActor{Type: "agent", ID: "agent-1"},
			Instructions: "ship it",
			AvatarURL:    "https://example.com/squad.png",
			Members:      []BackupSquadMember{{MemberType: "agent", MemberID: "agent-1", Role: "leader"}},
			ArchivedAt:   &squadArchived,
			CreatedAt:    ts,
		}},
		Autopilots: []BackupAutopilot{{
			ID:            "ap-1",
			Name:          "nightly",
			Assignee:      &BackupActor{Type: "agent", ID: "agent-1"},
			Status:        "active",
			ExecutionMode: "sequential",
			ProjectID:     "proj-1",
			Triggers: []BackupAutopilotTrigger{
				{
					Kind:     "schedule",
					Enabled:  true,
					Cron:     "0 0 * * *",
					Timezone: "UTC",
					Label:    "nightly run",
					Provider: "generic",
				},
				{
					Kind:     "webhook",
					Enabled:  true,
					Provider: "github",
					Payload:  json.RawMessage(`{"webhook_token":"tok","event_filters":["push"]}`),
				},
			},
			CreatedAt: ts,
		}},
	}

	data, err := Marshal(original)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	got, err := Unmarshal(data)
	if err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	// Re-marshal both sides and compare bytes: this verifies every field
	// survives the round trip without enumerating them by hand.
	want, err := Marshal(original)
	if err != nil {
		t.Fatalf("Marshal(original) second pass: %v", err)
	}
	roundTripped, err := Marshal(got)
	if err != nil {
		t.Fatalf("Marshal(got): %v", err)
	}
	if string(want) != string(roundTripped) {
		t.Errorf("round trip mismatch\n want: %s\n  got: %s", want, roundTripped)
	}
}

func TestUnmarshalVersionValidation(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantErr bool
		isUnsup bool
	}{
		{
			name:    "valid current version",
			input:   `{"metadata":{"version":"1.0","exported_at":"2026-06-09T00:00:00Z"}}`,
			wantErr: false,
		},
		{
			name:    "valid minor version ahead",
			input:   `{"metadata":{"version":"1.5","exported_at":"2026-06-09T00:00:00Z"}}`,
			wantErr: false,
		},
		{
			name:    "missing version",
			input:   `{"metadata":{"exported_at":"2026-06-09T00:00:00Z"}}`,
			wantErr: true,
		},
		{
			name:    "unsupported major version",
			input:   `{"metadata":{"version":"2.0"}}`,
			wantErr: true,
			isUnsup: true,
		},
		{
			name:    "invalid json",
			input:   `{not json`,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Unmarshal([]byte(tt.input))
			if tt.wantErr != (err != nil) {
				t.Fatalf("Unmarshal err = %v, wantErr = %v", err, tt.wantErr)
			}
			if tt.isUnsup && !errors.Is(err, ErrUnsupportedVersion) {
				t.Errorf("err = %v, want errors.Is ErrUnsupportedVersion", err)
			}
		})
	}
}

func TestNew(t *testing.T) {
	before := time.Now().UTC()
	bf := New()
	after := time.Now().UTC()

	if bf.Metadata.Version != FormatVersion {
		t.Errorf("Version = %q, want %q", bf.Metadata.Version, FormatVersion)
	}
	if bf.Metadata.ExportedAt.Before(before) || bf.Metadata.ExportedAt.After(after) {
		t.Errorf("ExportedAt = %v, want within [%v, %v]", bf.Metadata.ExportedAt, before, after)
	}
	if bf.Metadata.ExportedAt.Location() != time.UTC {
		t.Errorf("ExportedAt location = %v, want UTC", bf.Metadata.ExportedAt.Location())
	}
}
