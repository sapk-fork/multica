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
	original := &BackupFile{
		Metadata: BackupMetadata{
			Version:             FormatVersion,
			ExportedAt:          time.Date(2026, 6, 9, 12, 0, 0, 0, time.UTC),
			SourceWorkspaceID:   "ws-1",
			SourceWorkspaceName: "Acme",
			SourceWorkspaceSlug: "acme",
		},
		Skills: []BackupSkill{{
			ID:          "skill-1",
			Name:        "lint",
			Description: "runs the linter",
			Content:     "# Lint\n",
			Config:      json.RawMessage(`{"auto_fix":true}`),
			Files:       []BackupSkillFile{{Path: "scripts/run.sh", Content: "echo hi"}},
		}},
		Agents: []BackupAgent{{
			ID:            "agent-1",
			Name:          "Go Developer",
			RuntimeMode:   "local",
			RuntimeConfig: json.RawMessage(`{"image":"go"}`),
			Visibility:    "workspace",
			SkillIDs:      []string{"skill-1"},
			Model:         "claude-opus-4-8",
		}},
		Labels: []BackupLabel{{ID: "label-1", Name: "bug", Color: "#f00"}},
		Projects: []BackupProject{{
			ID:    "proj-1",
			Title: "Backup feature",
			Resources: []BackupProjectResource{{
				ID:           "res-1",
				ResourceType: "github_repo",
				ResourceRef:  json.RawMessage(`{"url":"https://example.com"}`),
				Label:        "repo",
				Position:     1,
			}},
		}},
		Issues: []BackupIssue{{
			ID:       "issue-1",
			Number:   23,
			Title:    "Define backup format",
			Status:   "in_progress",
			Priority: "medium",
			Assignee: BackupActor{Type: "agent", ID: "agent-1"},
			Creator:  BackupActor{Type: "agent", ID: "agent-2"},
			LabelIDs: []string{"label-1"},
			Comments: []BackupComment{{
				ID:        "comment-1",
				Author:    BackupActor{Type: "agent", ID: "agent-2"},
				Content:   "please do this",
				CreatedAt: time.Date(2026, 6, 9, 15, 15, 0, 0, time.UTC),
				Reactions: []BackupReaction{{Actor: BackupActor{Type: "agent", ID: "agent-1"}, Emoji: "👍"}},
			}},
			Metadata:  json.RawMessage(`{"pr_url":"https://example.com/pr/1"}`),
			Reactions: []BackupReaction{{Actor: BackupActor{Type: "member", ID: "user-1"}, Emoji: "🚀"}},
		}},
		Squads: []BackupSquad{{
			ID:           "squad-1",
			Name:         "Core",
			LeaderID:     "agent-1",
			Instructions: "ship it",
			Members:      []BackupSquadMember{{MemberType: "agent", MemberID: "agent-1", Role: "leader"}},
		}},
		Autopilots: []BackupAutopilot{{
			ID:       "ap-1",
			Name:     "nightly",
			Config:   json.RawMessage(`{"mode":"auto"}`),
			Schedule: "0 0 * * *",
			Enabled:  true,
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
			input:   `{"metadata":{"version":"1.0","exported_at":"2026-06-09T00:00:00Z","source_workspace_id":"ws"}}`,
			wantErr: false,
		},
		{
			name:    "missing version",
			input:   `{"metadata":{"exported_at":"2026-06-09T00:00:00Z","source_workspace_id":"ws"}}`,
			wantErr: true,
		},
		{
			name:    "unsupported version",
			input:   `{"metadata":{"version":"2.0","source_workspace_id":"ws"}}`,
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
	bf := New("ws-1", "Acme", "acme")
	after := time.Now().UTC()

	if bf.Metadata.Version != FormatVersion {
		t.Errorf("Version = %q, want %q", bf.Metadata.Version, FormatVersion)
	}
	if bf.Metadata.SourceWorkspaceID != "ws-1" {
		t.Errorf("SourceWorkspaceID = %q, want %q", bf.Metadata.SourceWorkspaceID, "ws-1")
	}
	if bf.Metadata.ExportedAt.Before(before) || bf.Metadata.ExportedAt.After(after) {
		t.Errorf("ExportedAt = %v, want within [%v, %v]", bf.Metadata.ExportedAt, before, after)
	}
	if bf.Metadata.ExportedAt.Location() != time.UTC {
		t.Errorf("ExportedAt location = %v, want UTC", bf.Metadata.ExportedAt.Location())
	}
}
