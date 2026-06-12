package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/backup"
	"github.com/multica-ai/multica/server/internal/logger"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// backupCommentCap bounds the number of comments fetched per issue. It mirrors
// the defensive cap used by the comment-list endpoint: issue comment counts are
// small in practice (~30 p99) and this only guards against a pathological row
// count blowing up a single export.
const backupCommentCap = 2000

// backupEntityTypes are the include_types values that POST /api/backup/export
// understands. The workspace envelope and the referenced members are always
// present; these toggle the optional entity sections.
var backupEntityTypes = []string{"skills", "agents", "labels", "projects", "issues", "squads", "autopilots"}

// includeSet is the parsed, validated set of entity sections an export should
// contain. A nil/absent include_types selects every section.
type includeSet map[string]bool

func (s includeSet) has(t string) bool { return s[t] }

// parseIncludeTypes turns the comma-separated include_types query param into a
// validated includeSet. An empty value selects all known types. An unknown
// type is a client error.
func parseIncludeTypes(raw string) (includeSet, error) {
	set := includeSet{}
	raw = strings.TrimSpace(raw)
	if raw == "" {
		for _, t := range backupEntityTypes {
			set[t] = true
		}
		return set, nil
	}
	for _, part := range strings.Split(raw, ",") {
		t := strings.TrimSpace(part)
		if t == "" {
			continue
		}
		known := false
		for _, valid := range backupEntityTypes {
			if t == valid {
				known = true
				break
			}
		}
		if !known {
			return nil, fmt.Errorf("unknown include_types value %q; valid values: %s", t, strings.Join(backupEntityTypes, ", "))
		}
		set[t] = true
	}
	return set, nil
}

// memberRefs accumulates the user IDs referenced by member-typed actors across
// an export (issue creators/assignees, comment authors, squad members, agent
// owners, ...). Member references are stored as the user's UUID — that is the
// value actor columns carry for humans — and resolved to BackupMember rows by
// BackupExport before the file is serialised.
type memberRefs map[string]struct{}

// addTyped records a reference only when it is a human member. Agent/squad/etc.
// actors are remapped through their own sections, so they are ignored here.
func (m memberRefs) addTyped(actorType string, id pgtype.UUID) {
	if actorType != "member" || !id.Valid {
		return
	}
	m[uuidToString(id)] = struct{}{}
}

// add records a bare user reference (no actor type to disambiguate), e.g. an
// agent's owner_id or a skill's created_by, both of which are always users.
func (m memberRefs) add(id pgtype.UUID) {
	if id.Valid {
		m[uuidToString(id)] = struct{}{}
	}
}

// BackupExport serialises the contents of a workspace into the portable backup
// format defined by the backup package and returns it as a JSON file.
//
// Query params:
//   - workspace_id: the workspace to export (also resolvable from the request
//     context / headers).
//   - include_types: comma-separated subset of skills,agents,labels,projects,
//     issues,squads,autopilots. Absent means "everything".
//
// The export bundles every workspace member referenced by the included
// entities so cross-instance restores can remap human identities by email.
// It is restricted to workspace owners/admins because the payload contains
// sensitive configuration (agent custom_env, autopilot signing secrets, ...).
func (h *Handler) BackupExport(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	workspaceID := h.resolveWorkspaceID(r)
	if _, ok := h.requireWorkspaceRole(w, r, workspaceID, "workspace not found", "owner", "admin"); !ok {
		return
	}
	wsUUID, ok := parseUUIDOrBadRequest(w, workspaceID, "workspace_id")
	if !ok {
		return
	}

	include, err := parseIncludeTypes(r.URL.Query().Get("include_types"))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	file := backup.New()
	refs := memberRefs{}

	ws, err := h.Queries.GetWorkspace(ctx, wsUUID)
	if err != nil {
		writeError(w, http.StatusNotFound, "workspace not found")
		return
	}
	file.Workspace = mapWorkspace(ws)

	if include.has("skills") {
		if file.Skills, err = h.exportSkills(ctx, wsUUID, refs); err != nil {
			h.writeBackupError(w, r, err, "skills")
			return
		}
	}
	if include.has("agents") {
		if file.Agents, err = h.exportAgents(ctx, wsUUID, refs); err != nil {
			h.writeBackupError(w, r, err, "agents")
			return
		}
	}
	if include.has("labels") {
		if file.Labels, err = h.exportLabels(ctx, wsUUID); err != nil {
			h.writeBackupError(w, r, err, "labels")
			return
		}
	}
	if include.has("projects") {
		if file.Projects, err = h.exportProjects(ctx, wsUUID, refs); err != nil {
			h.writeBackupError(w, r, err, "projects")
			return
		}
	}
	if include.has("issues") {
		if file.Issues, err = h.exportIssues(ctx, wsUUID, refs); err != nil {
			h.writeBackupError(w, r, err, "issues")
			return
		}
	}
	if include.has("squads") {
		if file.Squads, err = h.exportSquads(ctx, wsUUID, refs); err != nil {
			h.writeBackupError(w, r, err, "squads")
			return
		}
	}
	if include.has("autopilots") {
		if file.Autopilots, err = h.exportAutopilots(ctx, wsUUID, refs); err != nil {
			h.writeBackupError(w, r, err, "autopilots")
			return
		}
	}

	if file.Members, err = h.exportMembers(ctx, wsUUID, refs); err != nil {
		h.writeBackupError(w, r, err, "members")
		return
	}

	data, err := backup.Marshal(file)
	if err != nil {
		h.writeBackupError(w, r, err, "serialize")
		return
	}

	filename := fmt.Sprintf("multica-backup-%s-%s.json", ws.Slug, time.Now().UTC().Format("20060102-150405"))
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", filename))
	w.WriteHeader(http.StatusOK)
	if _, err := w.Write(data); err != nil {
		slog.Warn("backup export: failed to write response", append(logger.RequestAttrs(r), "error", err)...)
	}
}

// writeBackupError logs the underlying failure (an export touches many queries;
// the section label makes a 500 diagnosable) and returns a generic 500.
func (h *Handler) writeBackupError(w http.ResponseWriter, r *http.Request, err error, section string) {
	slog.Error("backup export failed", append(logger.RequestAttrs(r), "section", section, "error", err)...)
	writeError(w, http.StatusInternalServerError, "failed to export "+section)
}

func mapWorkspace(ws db.Workspace) *backup.BackupWorkspace {
	counter := ws.IssueCounter
	return &backup.BackupWorkspace{
		ID:           uuidToString(ws.ID),
		Name:         ws.Name,
		Slug:         ws.Slug,
		Description:  textOrEmpty(ws.Description),
		Context:      textOrEmpty(ws.Context),
		Settings:     rawJSON(ws.Settings),
		Repos:        rawJSON(ws.Repos),
		IssuePrefix:  ws.IssuePrefix,
		IssueCounter: &counter,
		AvatarURL:    textOrEmpty(ws.AvatarUrl),
		CreatedAt:    tsTime(ws.CreatedAt),
	}
}

func (h *Handler) exportSkills(ctx context.Context, wsUUID pgtype.UUID, refs memberRefs) ([]backup.BackupSkill, error) {
	skills, err := h.Queries.ListSkillsByWorkspace(ctx, wsUUID)
	if err != nil {
		return nil, fmt.Errorf("list skills: %w", err)
	}
	out := make([]backup.BackupSkill, 0, len(skills))
	for _, s := range skills {
		files, err := h.Queries.ListSkillFiles(ctx, s.ID)
		if err != nil {
			return nil, fmt.Errorf("list skill files for %s: %w", uuidToString(s.ID), err)
		}
		backupFiles := make([]backup.BackupSkillFile, 0, len(files))
		for _, f := range files {
			backupFiles = append(backupFiles, backup.BackupSkillFile{Path: f.Path, Content: f.Content})
		}
		// created_by is always a human member when set.
		refs.add(s.CreatedBy)
		var creator *backup.BackupActor
		if s.CreatedBy.Valid {
			creator = &backup.BackupActor{Type: "member", ID: uuidToString(s.CreatedBy)}
		}
		out = append(out, backup.BackupSkill{
			ID:          uuidToString(s.ID),
			Name:        s.Name,
			Description: s.Description,
			Content:     s.Content,
			Config:      rawJSON(s.Config),
			Files:       backupFiles,
			CreatedBy:   creator,
			CreatedAt:   tsTime(s.CreatedAt),
		})
	}
	return out, nil
}

func (h *Handler) exportAgents(ctx context.Context, wsUUID pgtype.UUID, refs memberRefs) ([]backup.BackupAgent, error) {
	agents, err := h.Queries.ListAllAgents(ctx, wsUUID)
	if err != nil {
		return nil, fmt.Errorf("list agents: %w", err)
	}
	// Batch the agent → skill links once for the whole workspace.
	skillRows, err := h.Queries.ListAgentSkillsByWorkspace(ctx, wsUUID)
	if err != nil {
		return nil, fmt.Errorf("list agent skills: %w", err)
	}
	skillsByAgent := map[string][]string{}
	for _, row := range skillRows {
		aid := uuidToString(row.AgentID)
		skillsByAgent[aid] = append(skillsByAgent[aid], uuidToString(row.ID))
	}
	out := make([]backup.BackupAgent, 0, len(agents))
	for _, a := range agents {
		refs.add(a.OwnerID)
		maxTasks := a.MaxConcurrentTasks
		out = append(out, backup.BackupAgent{
			ID:                 uuidToString(a.ID),
			Name:               a.Name,
			Description:        a.Description,
			Instructions:       a.Instructions,
			RuntimeMode:        a.RuntimeMode,
			RuntimeConfig:      rawJSON(a.RuntimeConfig),
			Visibility:         a.Visibility,
			SkillIDs:           skillsByAgent[uuidToString(a.ID)],
			CustomEnv:          rawJSON(a.CustomEnv),
			CustomArgs:         rawJSON(a.CustomArgs),
			McpConfig:          rawJSON(a.McpConfig),
			Model:              textOrEmpty(a.Model),
			ThinkingLevel:      textOrEmpty(a.ThinkingLevel),
			AvatarURL:          textOrEmpty(a.AvatarUrl),
			OwnerID:            uuidToString(a.OwnerID),
			MaxConcurrentTasks: &maxTasks,
			ArchivedAt:         tsTimePtr(a.ArchivedAt),
			CreatedAt:          tsTime(a.CreatedAt),
		})
	}
	return out, nil
}

func (h *Handler) exportLabels(ctx context.Context, wsUUID pgtype.UUID) ([]backup.BackupLabel, error) {
	labels, err := h.Queries.ListLabels(ctx, wsUUID)
	if err != nil {
		return nil, fmt.Errorf("list labels: %w", err)
	}
	out := make([]backup.BackupLabel, 0, len(labels))
	for _, l := range labels {
		out = append(out, backup.BackupLabel{
			ID:        uuidToString(l.ID),
			Name:      l.Name,
			Color:     l.Color,
			CreatedAt: tsTime(l.CreatedAt),
		})
	}
	return out, nil
}

func (h *Handler) exportProjects(ctx context.Context, wsUUID pgtype.UUID, refs memberRefs) ([]backup.BackupProject, error) {
	projects, err := h.Queries.ListProjects(ctx, db.ListProjectsParams{WorkspaceID: wsUUID})
	if err != nil {
		return nil, fmt.Errorf("list projects: %w", err)
	}
	if len(projects) == 0 {
		return nil, nil
	}
	projectIDs := make([]pgtype.UUID, len(projects))
	for i, p := range projects {
		projectIDs[i] = p.ID
	}
	resourceRows, err := h.Queries.ListProjectResourcesForProjects(ctx, projectIDs)
	if err != nil {
		return nil, fmt.Errorf("list project resources: %w", err)
	}
	resourcesByProject := map[string][]backup.BackupProjectResource{}
	for _, res := range resourceRows {
		pid := uuidToString(res.ProjectID)
		position := res.Position
		resourcesByProject[pid] = append(resourcesByProject[pid], backup.BackupProjectResource{
			ID:           uuidToString(res.ID),
			ResourceType: res.ResourceType,
			ResourceRef:  rawJSON(res.ResourceRef),
			Label:        textOrEmpty(res.Label),
			Position:     &position,
		})
	}
	out := make([]backup.BackupProject, 0, len(projects))
	for _, p := range projects {
		refs.addTyped(textOrEmpty(p.LeadType), p.LeadID)
		out = append(out, backup.BackupProject{
			ID:          uuidToString(p.ID),
			Title:       p.Title,
			Description: textOrEmpty(p.Description),
			Icon:        textOrEmpty(p.Icon),
			Status:      p.Status,
			Priority:    p.Priority,
			Lead:        backupActor(textOrEmpty(p.LeadType), p.LeadID),
			Resources:   resourcesByProject[uuidToString(p.ID)],
			CreatedAt:   tsTime(p.CreatedAt),
		})
	}
	return out, nil
}

func (h *Handler) exportIssues(ctx context.Context, wsUUID pgtype.UUID, refs memberRefs) ([]backup.BackupIssue, error) {
	issues, err := h.Queries.ListAllIssuesForBackup(ctx, wsUUID)
	if err != nil {
		return nil, fmt.Errorf("list issues: %w", err)
	}
	if len(issues) == 0 {
		return nil, nil
	}
	issueIDs := make([]pgtype.UUID, len(issues))
	for i, iss := range issues {
		issueIDs[i] = iss.ID
	}
	labelsByIssue, err := h.backupLabelIDsByIssue(ctx, wsUUID, issueIDs)
	if err != nil {
		return nil, err
	}

	out := make([]backup.BackupIssue, 0, len(issues))
	for _, iss := range issues {
		refs.addTyped(iss.CreatorType, iss.CreatorID)
		refs.addTyped(textOrEmpty(iss.AssigneeType), iss.AssigneeID)

		comments, err := h.exportComments(ctx, wsUUID, iss.ID, refs)
		if err != nil {
			return nil, err
		}
		reactions, err := h.exportIssueReactions(ctx, iss.ID, refs)
		if err != nil {
			return nil, err
		}
		out = append(out, backup.BackupIssue{
			ID:                 uuidToString(iss.ID),
			Number:             iss.Number,
			Title:              iss.Title,
			Description:        textOrEmpty(iss.Description),
			Status:             iss.Status,
			Priority:           iss.Priority,
			Assignee:           backupActor(textOrEmpty(iss.AssigneeType), iss.AssigneeID),
			Creator:            backupActor(iss.CreatorType, iss.CreatorID),
			ParentID:           uuidOrEmpty(iss.ParentIssueID),
			ProjectID:          uuidOrEmpty(iss.ProjectID),
			LabelIDs:           labelsByIssue[uuidToString(iss.ID)],
			Comments:           comments,
			Metadata:           rawJSON(iss.Metadata),
			Reactions:          reactions,
			Position:           iss.Position,
			DueDate:            dateTimePtr(iss.DueDate),
			StartDate:          dateTimePtr(iss.StartDate),
			AcceptanceCriteria: rawJSON(iss.AcceptanceCriteria),
			ContextRefs:        rawJSON(iss.ContextRefs),
			OriginType:         textOrEmpty(iss.OriginType),
			OriginID:           uuidOrEmpty(iss.OriginID),
			CreatedAt:          tsTime(iss.CreatedAt),
		})
	}
	return out, nil
}

// backupLabelIDsByIssue batches the label IDs attached to a set of issues.
// Unlike the list-rendering labelsByIssue, a backup needs only the IDs (the
// label rows themselves live in BackupFile.Labels) and treats a query failure
// as fatal so an export never silently drops associations.
func (h *Handler) backupLabelIDsByIssue(ctx context.Context, wsUUID pgtype.UUID, issueIDs []pgtype.UUID) (map[string][]string, error) {
	rows, err := h.Queries.ListLabelsForIssues(ctx, db.ListLabelsForIssuesParams{
		IssueIds:    issueIDs,
		WorkspaceID: wsUUID,
	})
	if err != nil {
		return nil, fmt.Errorf("list labels for issues: %w", err)
	}
	byIssue := map[string][]string{}
	for _, row := range rows {
		iid := uuidToString(row.IssueID)
		byIssue[iid] = append(byIssue[iid], uuidToString(row.ID))
	}
	return byIssue, nil
}

func (h *Handler) exportComments(ctx context.Context, wsUUID, issueID pgtype.UUID, refs memberRefs) ([]backup.BackupComment, error) {
	comments, err := h.Queries.ListCommentsForIssue(ctx, db.ListCommentsForIssueParams{
		IssueID:     issueID,
		WorkspaceID: wsUUID,
		Limit:       backupCommentCap,
	})
	if err != nil {
		return nil, fmt.Errorf("list comments for issue %s: %w", uuidToString(issueID), err)
	}
	if len(comments) == 0 {
		return nil, nil
	}
	// The cap orders by created_at ASC, so hitting it silently drops the
	// *newest* comments. The headroom is large (p99 ~30 per issue) but a
	// truncated backup is the kind of data loss that must be visible.
	if len(comments) == backupCommentCap {
		slog.Warn("backup export: comment cap reached, newest comments may be truncated",
			"issue_id", uuidToString(issueID),
			"workspace_id", uuidToString(wsUUID),
			"cap", backupCommentCap,
		)
	}
	commentIDs := make([]pgtype.UUID, len(comments))
	for i, c := range comments {
		commentIDs[i] = c.ID
	}
	reactionRows, err := h.Queries.ListReactionsByCommentIDs(ctx, commentIDs)
	if err != nil {
		return nil, fmt.Errorf("list comment reactions for issue %s: %w", uuidToString(issueID), err)
	}
	reactionsByComment := map[string][]backup.BackupReaction{}
	for _, rr := range reactionRows {
		refs.addTyped(rr.ActorType, rr.ActorID)
		cid := uuidToString(rr.CommentID)
		reactionsByComment[cid] = append(reactionsByComment[cid], backup.BackupReaction{
			Actor: backupActorValue(rr.ActorType, rr.ActorID),
			Emoji: rr.Emoji,
		})
	}
	out := make([]backup.BackupComment, 0, len(comments))
	for _, c := range comments {
		refs.addTyped(c.AuthorType, c.AuthorID)
		refs.addTyped(textOrEmpty(c.ResolvedByType), c.ResolvedByID)
		out = append(out, backup.BackupComment{
			ID:         uuidToString(c.ID),
			Author:     backupActorValue(c.AuthorType, c.AuthorID),
			Content:    c.Content,
			Type:       c.Type,
			ParentID:   uuidOrEmpty(c.ParentID),
			CreatedAt:  tsTime(c.CreatedAt),
			Reactions:  reactionsByComment[uuidToString(c.ID)],
			ResolvedAt: tsTimePtr(c.ResolvedAt),
			ResolvedBy: backupActor(textOrEmpty(c.ResolvedByType), c.ResolvedByID),
		})
	}
	return out, nil
}

func (h *Handler) exportIssueReactions(ctx context.Context, issueID pgtype.UUID, refs memberRefs) ([]backup.BackupReaction, error) {
	rows, err := h.Queries.ListIssueReactions(ctx, issueID)
	if err != nil {
		return nil, fmt.Errorf("list issue reactions for %s: %w", uuidToString(issueID), err)
	}
	if len(rows) == 0 {
		return nil, nil
	}
	out := make([]backup.BackupReaction, 0, len(rows))
	for _, rr := range rows {
		refs.addTyped(rr.ActorType, rr.ActorID)
		out = append(out, backup.BackupReaction{
			Actor: backupActorValue(rr.ActorType, rr.ActorID),
			Emoji: rr.Emoji,
		})
	}
	return out, nil
}

func (h *Handler) exportSquads(ctx context.Context, wsUUID pgtype.UUID, refs memberRefs) ([]backup.BackupSquad, error) {
	squads, err := h.Queries.ListAllSquads(ctx, wsUUID)
	if err != nil {
		return nil, fmt.Errorf("list squads: %w", err)
	}
	out := make([]backup.BackupSquad, 0, len(squads))
	for _, s := range squads {
		members, err := h.Queries.ListSquadMembers(ctx, s.ID)
		if err != nil {
			return nil, fmt.Errorf("list squad members for %s: %w", uuidToString(s.ID), err)
		}
		backupMembers := make([]backup.BackupSquadMember, 0, len(members))
		for _, m := range members {
			refs.addTyped(m.MemberType, m.MemberID)
			backupMembers = append(backupMembers, backup.BackupSquadMember{
				MemberType: m.MemberType,
				MemberID:   uuidToString(m.MemberID),
				Role:       m.Role,
			})
		}
		// A squad's leader_id always references an agent (the schema joins it
		// to the agent table); there is no leader_type column.
		var leader *backup.BackupActor
		if s.LeaderID.Valid {
			leader = &backup.BackupActor{Type: "agent", ID: uuidToString(s.LeaderID)}
		}
		out = append(out, backup.BackupSquad{
			ID:           uuidToString(s.ID),
			Name:         s.Name,
			Description:  s.Description,
			Leader:       leader,
			Instructions: s.Instructions,
			AvatarURL:    textOrEmpty(s.AvatarUrl),
			Members:      backupMembers,
			ArchivedAt:   tsTimePtr(s.ArchivedAt),
			CreatedAt:    tsTime(s.CreatedAt),
		})
	}
	return out, nil
}

func (h *Handler) exportAutopilots(ctx context.Context, wsUUID pgtype.UUID, refs memberRefs) ([]backup.BackupAutopilot, error) {
	autopilots, err := h.Queries.ListAutopilots(ctx, db.ListAutopilotsParams{WorkspaceID: wsUUID})
	if err != nil {
		return nil, fmt.Errorf("list autopilots: %w", err)
	}
	out := make([]backup.BackupAutopilot, 0, len(autopilots))
	for _, a := range autopilots {
		triggers, err := h.Queries.ListAutopilotTriggers(ctx, a.ID)
		if err != nil {
			return nil, fmt.Errorf("list autopilot triggers for %s: %w", uuidToString(a.ID), err)
		}
		refs.addTyped(a.AssigneeType, a.AssigneeID)
		out = append(out, backup.BackupAutopilot{
			ID:            uuidToString(a.ID),
			Name:          a.Title,
			Assignee:      backupActor(a.AssigneeType, a.AssigneeID),
			Status:        a.Status,
			ExecutionMode: a.ExecutionMode,
			ProjectID:     uuidOrEmpty(a.ProjectID),
			Triggers:      mapAutopilotTriggers(triggers),
			CreatedAt:     tsTime(a.CreatedAt),
		})
	}
	return out, nil
}

// mapAutopilotTriggers converts every trigger row attached to an autopilot
// into its backup representation. The list order is preserved from the
// query (ORDER BY created_at ASC) so a restore sees triggers in the order
// they were created. Secrets carried by webhook triggers (webhook_token,
// signing_secret) follow the same plaintext contract as agent custom_env:
// the owner/admin gate on the export endpoint is the security boundary.
func mapAutopilotTriggers(triggers []db.AutopilotTrigger) []backup.BackupAutopilotTrigger {
	if len(triggers) == 0 {
		return nil
	}
	out := make([]backup.BackupAutopilotTrigger, 0, len(triggers))
	for _, t := range triggers {
		out = append(out, backup.BackupAutopilotTrigger{
			Kind:     t.Kind,
			Enabled:  t.Enabled,
			Cron:     textOrEmpty(t.CronExpression),
			Timezone: textOrEmpty(t.Timezone),
			Label:    textOrEmpty(t.Label),
			Provider: t.Provider,
			Payload:  autopilotTriggerPayload(t),
		})
	}
	return out
}

// autopilotTriggerPayload bundles the provider-specific trigger fields that
// don't have a dedicated column on BackupAutopilotTrigger (webhook token,
// signing secret, event filters) into a single JSON blob. Fields are omitted
// when unset so a schedule trigger doesn't carry empty webhook fields.
func autopilotTriggerPayload(t db.AutopilotTrigger) json.RawMessage {
	payload := map[string]any{}
	if t.WebhookToken.Valid && t.WebhookToken.String != "" {
		payload["webhook_token"] = t.WebhookToken.String
	}
	if t.SigningSecret.Valid && t.SigningSecret.String != "" {
		payload["signing_secret"] = t.SigningSecret.String
	}
	if len(t.EventFilters) > 0 {
		payload["event_filters"] = json.RawMessage(t.EventFilters)
	}
	if len(payload) == 0 {
		return nil
	}
	data, err := json.Marshal(payload)
	if err != nil {
		// json.Marshal on a map[string]any with string/RawMessage values
		// cannot fail in practice; degrade to an empty payload rather than
		// failing the whole export.
		return nil
	}
	return data
}

// exportMembers resolves the collected member references into BackupMember
// rows. Email is the cross-instance identity key, so a reference whose user
// row can no longer be loaded is skipped (it cannot be remapped on restore).
func (h *Handler) exportMembers(ctx context.Context, wsUUID pgtype.UUID, refs memberRefs) ([]backup.BackupMember, error) {
	if len(refs) == 0 {
		return nil, nil
	}
	rolesByUser := map[string]string{}
	memberRows, err := h.Queries.ListMembersWithUser(ctx, wsUUID)
	if err != nil {
		return nil, fmt.Errorf("list members: %w", err)
	}
	for _, m := range memberRows {
		rolesByUser[uuidToString(m.UserID)] = m.Role
	}

	ids := make([]string, 0, len(refs))
	for id := range refs {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	out := make([]backup.BackupMember, 0, len(ids))
	for _, id := range ids {
		userUUID, err := util.ParseUUID(id)
		if err != nil {
			continue
		}
		user, err := h.Queries.GetUser(ctx, userUUID)
		if err != nil {
			// A referenced user that no longer exists cannot be remapped by
			// email on restore; drop it rather than emit an identity-less row.
			slog.Debug("backup export: skipping unresolvable member reference", "user_id", id, "error", err)
			continue
		}
		out = append(out, backup.BackupMember{
			ID:                 id,
			Name:               user.Name,
			Email:              user.Email,
			AvatarURL:          textOrEmpty(user.AvatarUrl),
			Role:               rolesByUser[id],
			Language:           textOrEmpty(user.Language),
			Timezone:           textOrEmpty(user.Timezone),
			ProfileDescription: user.ProfileDescription,
		})
	}
	return out, nil
}

// --- small pgtype → backup conversions ---

func textOrEmpty(t pgtype.Text) string {
	if !t.Valid {
		return ""
	}
	return t.String
}

func uuidOrEmpty(u pgtype.UUID) string {
	if !u.Valid {
		return ""
	}
	return uuidToString(u)
}

func tsTime(t pgtype.Timestamptz) time.Time {
	if !t.Valid {
		return time.Time{}
	}
	return t.Time.UTC()
}

func tsTimePtr(t pgtype.Timestamptz) *time.Time {
	if !t.Valid {
		return nil
	}
	u := t.Time.UTC()
	return &u
}

func dateTimePtr(d pgtype.Date) *time.Time {
	if !d.Valid {
		return nil
	}
	u := d.Time.UTC()
	return &u
}

// rawJSON passes a stored JSON/JSONB column through to the backup as-is,
// treating an empty column as "unset" so omitempty drops it.
func rawJSON(b []byte) json.RawMessage {
	if len(b) == 0 {
		return nil
	}
	return json.RawMessage(b)
}

// backupActor builds a polymorphic actor reference for an *optional* field,
// returning nil — which omitempty drops — when the id is unset. Use
// backupActorValue for fields where the actor is always present.
func backupActor(actorType string, id pgtype.UUID) *backup.BackupActor {
	if !id.Valid {
		return nil
	}
	return &backup.BackupActor{Type: actorType, ID: uuidToString(id)}
}

// backupActorValue builds a polymorphic actor reference for a *required*
// field (e.g. comment author, reaction actor) where there is no "unset"
// state to represent. Callers must ensure the id is valid.
func backupActorValue(actorType string, id pgtype.UUID) backup.BackupActor {
	return backup.BackupActor{Type: actorType, ID: uuidToString(id)}
}
