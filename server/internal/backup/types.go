// Package backup defines the portable, self-contained file format used to
// export and restore the contents of a Multica workspace.
//
// The types in this package are intentionally decoupled from the database
// layer (server/pkg/db/generated): they use plain Go types (string UUIDs,
// time.Time, json.RawMessage) so the serialized format stays stable and
// human-readable regardless of how the underlying schema evolves. Mapping
// between these backup types and the DB models lives in the export/import
// code, not here.
package backup

import (
	"encoding/json"
	"time"
)

// FormatVersion is the current backup file format version. Unmarshal rejects
// files that do not declare this exact version.
const FormatVersion = "1.0"

// BackupFile is the top-level container of a workspace backup. Entity sections
// are ordered roughly by dependency (skills and agents before issues that
// reference them) so a restore can be applied top to bottom.
type BackupFile struct {
	Metadata   BackupMetadata    `json:"metadata"`
	Skills     []BackupSkill     `json:"skills,omitempty"`
	Agents     []BackupAgent     `json:"agents,omitempty"`
	Labels     []BackupLabel     `json:"labels,omitempty"`
	Projects   []BackupProject   `json:"projects,omitempty"`
	Issues     []BackupIssue     `json:"issues,omitempty"`
	Squads     []BackupSquad     `json:"squads,omitempty"`
	Autopilots []BackupAutopilot `json:"autopilots,omitempty"`
}

// BackupMetadata describes the backup itself and the workspace it was
// exported from.
type BackupMetadata struct {
	// Version is the backup format version; always FormatVersion on export.
	Version string `json:"version"`
	// ExportedAt is the UTC time the backup was produced.
	ExportedAt time.Time `json:"exported_at"`
	// SourceWorkspaceID is the UUID of the workspace the data came from.
	SourceWorkspaceID string `json:"source_workspace_id"`
	// SourceWorkspaceName is the human-readable workspace name, for context.
	SourceWorkspaceName string `json:"source_workspace_name,omitempty"`
	// SourceWorkspaceSlug is the workspace slug, for context.
	SourceWorkspaceSlug string `json:"source_workspace_slug,omitempty"`
}

// BackupSkill is a skill definition together with its attached files.
type BackupSkill struct {
	ID          string            `json:"id"`
	Name        string            `json:"name"`
	Description string            `json:"description,omitempty"`
	Content     string            `json:"content,omitempty"`
	Config      json.RawMessage   `json:"config,omitempty"`
	Files       []BackupSkillFile `json:"files,omitempty"`
}

// BackupSkillFile is a single file bundled with a skill.
type BackupSkillFile struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

// BackupAgent is an agent definition. Skills are referenced by ID; the
// referenced skills are expected to be present in BackupFile.Skills.
type BackupAgent struct {
	ID            string          `json:"id"`
	Name          string          `json:"name"`
	Description   string          `json:"description,omitempty"`
	Instructions  string          `json:"instructions,omitempty"`
	RuntimeMode   string          `json:"runtime_mode"`
	RuntimeConfig json.RawMessage `json:"runtime_config,omitempty"`
	Visibility    string          `json:"visibility,omitempty"`
	SkillIDs      []string        `json:"skill_ids,omitempty"`
	CustomEnv     json.RawMessage `json:"custom_env,omitempty"`
	CustomArgs    json.RawMessage `json:"custom_args,omitempty"`
	Model         string          `json:"model,omitempty"`
}

// BackupLabel is an issue label.
type BackupLabel struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Color string `json:"color,omitempty"`
}

// BackupProject is a project together with its linked resources.
type BackupProject struct {
	ID          string                  `json:"id"`
	Title       string                  `json:"title"`
	Description string                  `json:"description,omitempty"`
	Icon        string                  `json:"icon,omitempty"`
	Status      string                  `json:"status,omitempty"`
	LeadType    string                  `json:"lead_type,omitempty"`
	LeadID      string                  `json:"lead_id,omitempty"`
	Resources   []BackupProjectResource `json:"resources,omitempty"`
}

// BackupProjectResource is a resource (repo, link, ...) attached to a project.
type BackupProjectResource struct {
	ID           string          `json:"id"`
	ResourceType string          `json:"resource_type"`
	ResourceRef  json.RawMessage `json:"resource_ref,omitempty"`
	Label        string          `json:"label,omitempty"`
	Position     int32           `json:"position,omitempty"`
}

// BackupActor identifies a polymorphic actor (a member or an agent) by type
// and ID. An empty Type/ID pair means "unset" (e.g. an unassigned issue).
type BackupActor struct {
	Type string `json:"type,omitempty"`
	ID   string `json:"id,omitempty"`
}

// BackupIssue is an issue with its comments, labels, reactions and metadata.
type BackupIssue struct {
	ID          string           `json:"id"`
	Number      int32            `json:"number"`
	Title       string           `json:"title"`
	Description string           `json:"description,omitempty"`
	Status      string           `json:"status"`
	Priority    string           `json:"priority,omitempty"`
	Assignee    BackupActor      `json:"assignee,omitempty"`
	Creator     BackupActor      `json:"creator,omitempty"`
	ParentID    string           `json:"parent_id,omitempty"`
	ProjectID   string           `json:"project_id,omitempty"`
	LabelIDs    []string         `json:"label_ids,omitempty"`
	Comments    []BackupComment  `json:"comments,omitempty"`
	Metadata    json.RawMessage  `json:"metadata,omitempty"`
	Reactions   []BackupReaction `json:"reactions,omitempty"`
}

// BackupComment is a comment on an issue. Threading is preserved via ParentID.
type BackupComment struct {
	ID        string           `json:"id"`
	Author    BackupActor      `json:"author"`
	Content   string           `json:"content"`
	Type      string           `json:"type,omitempty"`
	ParentID  string           `json:"parent_id,omitempty"`
	CreatedAt time.Time        `json:"created_at"`
	Reactions []BackupReaction `json:"reactions,omitempty"`
}

// BackupReaction is an emoji reaction on an issue or a comment.
type BackupReaction struct {
	Actor BackupActor `json:"actor"`
	Emoji string      `json:"emoji"`
}

// BackupSquad is a squad with its members.
type BackupSquad struct {
	ID           string              `json:"id"`
	Name         string              `json:"name"`
	Description  string              `json:"description,omitempty"`
	LeaderID     string              `json:"leader_id,omitempty"`
	Instructions string              `json:"instructions,omitempty"`
	Members      []BackupSquadMember `json:"members,omitempty"`
}

// BackupSquadMember is a single member of a squad (a member or an agent).
type BackupSquadMember struct {
	MemberType string `json:"member_type"`
	MemberID   string `json:"member_id"`
	Role       string `json:"role,omitempty"`
}

// BackupAutopilot is an autopilot definition. Config carries the autopilot's
// settings as an opaque JSON blob; Schedule is the cron expression (if any)
// of its scheduled trigger.
type BackupAutopilot struct {
	ID       string          `json:"id"`
	Name     string          `json:"name"`
	Config   json.RawMessage `json:"config,omitempty"`
	Schedule string          `json:"schedule,omitempty"`
	Enabled  bool            `json:"enabled"`
}
