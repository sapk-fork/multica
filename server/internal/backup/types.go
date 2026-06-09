// Package backup defines the portable, self-contained file format used to
// export and restore the contents of a Multica workspace.
//
// The types in this package are intentionally decoupled from the database
// layer (server/pkg/db/generated): they use plain Go types (string UUIDs,
// time.Time, json.RawMessage) so the serialized format stays stable and
// human-readable regardless of how the underlying schema evolves. Mapping
// between these backup types and the DB models lives in the export/import
// code, not here.
//
// # Cross-instance identity resolution
//
// A backup may be restored into a workspace on a different Multica instance,
// where the original UUIDs do not exist. To make references resolvable, the
// backup carries a Members section listing every human member referenced by
// the export (see BackupMember). On restore, human references (BackupActor
// with Type "member") are remapped by email — the only identity that is
// stable across instances. Agent, skill, label, project and squad references
// are remapped through their own sections, which are part of the backup.
package backup

import (
	"encoding/json"
	"time"
)

// FormatVersion is the current backup file format version. Unmarshal rejects
// files that do not declare this exact version.
//
// Version strategy: The version follows semantic versioning (major.minor).
// - Minor version bumps add optional fields with omitempty; old readers can
//   safely ignore unknown fields, so Unmarshal accepts any minor version
//   as long as the major version matches.
// - Major version bumps indicate breaking changes (removed/renamed fields,
//   changed semantics); Unmarshal rejects mismatched major versions.
// When adding fields, use pointer types for optional data to distinguish
// "not set" from zero values, and always use omitempty for optional fields.
const FormatVersion = "1.0"

// BackupFile is the top-level container of a workspace backup. Entity sections
// are ordered roughly by dependency (members, skills and agents before issues
// that reference them) so a restore can be applied top to bottom.
type BackupFile struct {
	Metadata   BackupMetadata    `json:"metadata"`
	Workspace  *BackupWorkspace  `json:"workspace,omitempty"`
	Members    []BackupMember    `json:"members,omitempty"`
	Skills     []BackupSkill     `json:"skills,omitempty"`
	Agents     []BackupAgent     `json:"agents,omitempty"`
	Labels     []BackupLabel     `json:"labels,omitempty"`
	Projects   []BackupProject   `json:"projects,omitempty"`
	Issues     []BackupIssue     `json:"issues,omitempty"`
	Squads     []BackupSquad     `json:"squads,omitempty"`
	Autopilots []BackupAutopilot `json:"autopilots,omitempty"`
}

// BackupMetadata describes the backup itself: the format version and when the
// backup was produced.
type BackupMetadata struct {
	// Version is the backup format version; always FormatVersion on export.
	Version string `json:"version"`
	// ExportedAt is the UTC time the backup was produced.
	ExportedAt time.Time `json:"exported_at"`
}

// BackupWorkspace captures workspace-level settings so a restore can recreate
// the workspace configuration, not just its contents. It is optional: an
// export that only snapshots entities may omit it.
type BackupWorkspace struct {
	ID           string          `json:"id"`
	Name         string          `json:"name"`
	Slug         string          `json:"slug"`
	Description  string          `json:"description,omitempty"`
	Context      string          `json:"context,omitempty"`
	Settings     json.RawMessage `json:"settings,omitempty"`
	Repos        json.RawMessage `json:"repos,omitempty"`
	IssuePrefix  string          `json:"issue_prefix,omitempty"`
	IssueCounter *int32          `json:"issue_counter,omitempty"`
	AvatarURL    string          `json:"avatar_url,omitempty"`
	CreatedAt    time.Time       `json:"created_at"`
}

// BackupMember is a human workspace member. Email is the cross-instance
// identity key used to remap member references on restore (see the package
// doc); ID is the source-instance UUID, meaningful only within the source.
type BackupMember struct {
	ID                 string `json:"id"`
	Name               string `json:"name,omitempty"`
	Email              string `json:"email"`
	AvatarURL          string `json:"avatar_url,omitempty"`
	Role               string `json:"role,omitempty"`
	Language           string `json:"language,omitempty"`
	Timezone           string `json:"timezone,omitempty"`
	ProfileDescription string `json:"profile_description,omitempty"`
}

// BackupSkill is a skill definition together with its attached files.
type BackupSkill struct {
	ID          string            `json:"id"`
	Name        string            `json:"name"`
	Description string            `json:"description,omitempty"`
	Content     string            `json:"content,omitempty"`
	Config      json.RawMessage   `json:"config,omitempty"`
	Files       []BackupSkillFile `json:"files,omitempty"`
	CreatedBy   BackupActor       `json:"created_by,omitempty"`
	CreatedAt   time.Time         `json:"created_at"`
}

// BackupSkillFile is a single file bundled with a skill.
type BackupSkillFile struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

// BackupAgent is an agent definition. Skills are referenced by ID; the
// referenced skills are expected to be present in BackupFile.Skills.
type BackupAgent struct {
	ID                 string          `json:"id"`
	Name               string          `json:"name"`
	Description        string          `json:"description,omitempty"`
	Instructions       string          `json:"instructions,omitempty"`
	RuntimeMode        string          `json:"runtime_mode"`
	RuntimeConfig      json.RawMessage `json:"runtime_config,omitempty"`
	Visibility         string          `json:"visibility,omitempty"`
	SkillIDs           []string        `json:"skill_ids,omitempty"`
	CustomEnv          json.RawMessage `json:"custom_env,omitempty"`
	CustomArgs         json.RawMessage `json:"custom_args,omitempty"`
	McpConfig          json.RawMessage `json:"mcp_config,omitempty"`
	Model              string          `json:"model,omitempty"`
	ThinkingLevel      string          `json:"thinking_level,omitempty"`
	AvatarURL          string          `json:"avatar_url,omitempty"`
	OwnerID            string          `json:"owner_id,omitempty"`
	MaxConcurrentTasks *int32          `json:"max_concurrent_tasks,omitempty"`
	ArchivedAt         *time.Time      `json:"archived_at,omitempty"`
	CreatedAt          time.Time       `json:"created_at"`
}

// BackupLabel is an issue label.
type BackupLabel struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Color     string    `json:"color,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

// BackupProject is a project together with its linked resources.
type BackupProject struct {
	ID          string                  `json:"id"`
	Title       string                  `json:"title"`
	Description string                  `json:"description,omitempty"`
	Icon        string                  `json:"icon,omitempty"`
	Status      string                  `json:"status,omitempty"`
	Priority    string                  `json:"priority,omitempty"`
	Lead        BackupActor             `json:"lead,omitempty"`
	Resources   []BackupProjectResource `json:"resources,omitempty"`
	CreatedAt   time.Time               `json:"created_at"`
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
// Member-typed actors are resolved against BackupFile.Members by email on
// cross-instance restore; agent-typed actors against BackupFile.Agents.
type BackupActor struct {
	Type string `json:"type,omitempty"`
	ID   string `json:"id,omitempty"`
}

// BackupIssue is an issue with its comments, labels, reactions and metadata.
type BackupIssue struct {
	ID                 string           `json:"id"`
	Number             int32            `json:"number"`
	Title              string           `json:"title"`
	Description        string           `json:"description,omitempty"`
	Status             string           `json:"status"`
	Priority           string           `json:"priority,omitempty"`
	Assignee           BackupActor      `json:"assignee,omitempty"`
	Creator            BackupActor      `json:"creator,omitempty"`
	ParentID           string           `json:"parent_id,omitempty"`
	ProjectID          string           `json:"project_id,omitempty"`
	LabelIDs           []string         `json:"label_ids,omitempty"`
	Comments           []BackupComment  `json:"comments,omitempty"`
	Metadata           json.RawMessage  `json:"metadata,omitempty"`
	Reactions          []BackupReaction `json:"reactions,omitempty"`
	Position           float64          `json:"position,omitempty"`
	DueDate            *time.Time       `json:"due_date,omitempty"`
	StartDate          *time.Time       `json:"start_date,omitempty"`
	AcceptanceCriteria json.RawMessage  `json:"acceptance_criteria,omitempty"`
	ContextRefs        json.RawMessage  `json:"context_refs,omitempty"`
	OriginType         string           `json:"origin_type,omitempty"`
	OriginID           string           `json:"origin_id,omitempty"`
	CreatedAt          time.Time        `json:"created_at"`
}

// BackupComment is a comment on an issue. Threading is preserved via ParentID.
type BackupComment struct {
	ID             string           `json:"id"`
	Author         BackupActor      `json:"author"`
	Content        string           `json:"content"`
	Type           string           `json:"type,omitempty"`
	ParentID       string           `json:"parent_id,omitempty"`
	CreatedAt      time.Time        `json:"created_at"`
	Reactions      []BackupReaction `json:"reactions,omitempty"`
	ResolvedAt     *time.Time       `json:"resolved_at,omitempty"`
	ResolvedBy     BackupActor      `json:"resolved_by,omitempty"`
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
	Leader       BackupActor         `json:"leader,omitempty"`
	Instructions string              `json:"instructions,omitempty"`
	AvatarURL    string              `json:"avatar_url,omitempty"`
	Members      []BackupSquadMember `json:"members,omitempty"`
	ArchivedAt   *time.Time          `json:"archived_at,omitempty"`
	CreatedAt    time.Time           `json:"created_at"`
}

// BackupSquadMember is a single member of a squad (a member or an agent).
type BackupSquadMember struct {
	MemberType string `json:"member_type"`
	MemberID   string `json:"member_id"`
	Role       string `json:"role,omitempty"`
}

// BackupAutopilot is an autopilot definition. Schedule is the cron expression
// (if any) of its scheduled trigger.
type BackupAutopilot struct {
	ID            string      `json:"id"`
	Name          string      `json:"name"`
	Schedule      string      `json:"schedule,omitempty"`
	Enabled       bool        `json:"enabled"`
	Assignee      BackupActor `json:"assignee,omitempty"`
	Status        string      `json:"status,omitempty"`
	ExecutionMode string      `json:"execution_mode,omitempty"`
	ProjectID     string      `json:"project_id,omitempty"`
	TriggerKind   string      `json:"trigger_kind,omitempty"`
	TriggerTZ     string      `json:"trigger_tz,omitempty"`
	CreatedAt     time.Time   `json:"created_at"`
}
