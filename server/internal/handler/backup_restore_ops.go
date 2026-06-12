package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/backup"
	skillpkg "github.com/multica-ai/multica/server/internal/skill"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// This file holds the per-section operations the restore planner calls.
// They are split out from backup_restore.go so the orchestrator reads
// top-to-bottom while the heavy lifting lives in a per-entity group
// below.

// --- Section: find by identifier (returns the existing target row) ---

func findSkillByName(ctx context.Context, q *db.Queries, workspaceID pgtype.UUID, name string) (db.Skill, bool, error) {
	sk, err := q.GetSkillByWorkspaceAndName(ctx, db.GetSkillByWorkspaceAndNameParams{
		WorkspaceID: workspaceID,
		Name:        name,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return db.Skill{}, false, nil
		}
		return db.Skill{}, false, err
	}
	return sk, true, nil
}

func findLabelByName(ctx context.Context, q *db.Queries, workspaceID pgtype.UUID, name string) (db.IssueLabel, bool, error) {
	labels, err := q.ListLabels(ctx, workspaceID)
	if err != nil {
		return db.IssueLabel{}, false, err
	}
	for _, l := range labels {
		if strings.EqualFold(l.Name, name) {
			return l, true, nil
		}
	}
	return db.IssueLabel{}, false, nil
}

func findAgentByName(ctx context.Context, q *db.Queries, workspaceID pgtype.UUID, name string) (db.Agent, bool, error) {
	agents, err := q.ListAllAgents(ctx, workspaceID)
	if err != nil {
		return db.Agent{}, false, err
	}
	for _, a := range agents {
		if a.Name == name {
			return a, true, nil
		}
	}
	return db.Agent{}, false, nil
}

func findProjectByTitle(ctx context.Context, q *db.Queries, workspaceID pgtype.UUID, title string) (db.Project, bool, error) {
	rows, err := q.ListProjects(ctx, db.ListProjectsParams{
		WorkspaceID: workspaceID,
		Status:      pgtype.Text{},
		Priority:    pgtype.Text{},
	})
	if err != nil {
		return db.Project{}, false, err
	}
	for _, p := range rows {
		if p.Title == title {
			return p, true, nil
		}
	}
	return db.Project{}, false, nil
}

func findSquadByName(ctx context.Context, q *db.Queries, workspaceID pgtype.UUID, name string) (db.Squad, bool, error) {
	squads, err := q.ListAllSquads(ctx, workspaceID)
	if err != nil {
		return db.Squad{}, false, err
	}
	for _, s := range squads {
		if s.Name == name {
			return s, true, nil
		}
	}
	return db.Squad{}, false, nil
}

func findAutopilotByTitle(ctx context.Context, q *db.Queries, workspaceID pgtype.UUID, title string) (db.Autopilot, bool, error) {
	rows, err := q.ListAutopilots(ctx, db.ListAutopilotsParams{
		WorkspaceID: workspaceID,
	})
	if err != nil {
		return db.Autopilot{}, false, err
	}
	for _, ap := range rows {
		if ap.Title == title {
			return ap, true, nil
		}
	}
	return db.Autopilot{}, false, nil
}

func findIssueByTitle(ctx context.Context, q *db.Queries, workspaceID pgtype.UUID, title string) (db.Issue, bool, error) {
	rows, err := q.ListIssues(ctx, db.ListIssuesParams{
		WorkspaceID: workspaceID,
		// Wide net: leave filters unset so the search hits every
		// issue in the workspace regardless of status, assignee,
		// or project. The list call returns paginated results;
		// for very large workspaces a targeted query would be
		// cheaper, but restore is operator-driven and runs at
		// human pace.
		Limit:  1000,
		Offset: 0,
	})
	if err != nil {
		return db.Issue{}, false, err
	}
	for _, r := range rows {
		if r.Title == title {
			return db.Issue{
				ID:          r.ID,
				WorkspaceID: r.WorkspaceID,
				Title:       r.Title,
				Status:      r.Status,
			}, true, nil
		}
	}
	return db.Issue{}, false, nil
}

func overwriteIssueLabels(ctx context.Context, q *db.Queries, workspaceID, issueID pgtype.UUID, labelSourceIDs []string, remapLabel map[string]pgtype.UUID) error {
	// Detach everything currently attached, then re-attach the
	// labels the backup brought along. ListLabelsForIssues is
	// the available list path; DetachLabelFromIssue is the
	// single-row detach.
	currentLabels, err := q.ListLabelsForIssues(ctx, db.ListLabelsForIssuesParams{
		IssueIds:    []pgtype.UUID{issueID},
		WorkspaceID: workspaceID,
	})
	if err != nil {
		return fmt.Errorf("list issue labels: %w", err)
	}
	for _, l := range currentLabels {
		if err := q.DetachLabelFromIssue(ctx, db.DetachLabelFromIssueParams{
			IssueID:     issueID,
			LabelID:     l.ID,
			WorkspaceID: workspaceID,
		}); err != nil {
			return fmt.Errorf("detach label: %w", err)
		}
	}
	for _, sid := range labelSourceIDs {
		target, ok := remapLabel[sid]
		if !ok {
			continue
		}
		if err := q.AttachLabelToIssue(ctx, db.AttachLabelToIssueParams{
			IssueID:     issueID,
			LabelID:     target,
			WorkspaceID: workspaceID,
		}); err != nil {
			return fmt.Errorf("attach label: %w", err)
		}
	}
	return nil
}

// --- Section: update (overwrite path) ---

func updateSkill(ctx context.Context, q *db.Queries, workspaceID pgtype.UUID, existing db.Skill, src backup.BackupSkill) error {
	description := src.Description
	content := src.Content
	config := []byte(src.Config)
	if len(config) == 0 {
		config = []byte("{}")
	}
	_, err := q.UpdateSkill(ctx, db.UpdateSkillParams{
		ID:          existing.ID,
		Name:        pgtype.Text{String: src.Name, Valid: true},
		Description: pgtype.Text{String: description, Valid: true},
		Content:     pgtype.Text{String: content, Valid: true},
		Config:      config,
	})
	if err != nil {
		return fmt.Errorf("update skill: %w", err)
	}
	// Files: delete-then-upsert via the existing skill-files path. The
	// restore contract is "the backup is the new source of truth", so
	// any file the backup did not bring along is removed.
	if err := q.DeleteSkillFilesBySkill(ctx, existing.ID); err != nil {
		return fmt.Errorf("clear skill files: %w", err)
	}
	for _, f := range src.Files {
		if !validateFilePath(f.Path) {
			continue
		}
		if skillpkg.IsReservedContentPath(f.Path) {
			continue
		}
		if _, err := q.UpsertSkillFile(ctx, db.UpsertSkillFileParams{
			SkillID: existing.ID,
			Path:    sanitizeNullBytes(f.Path),
			Content: sanitizeNullBytes(f.Content),
		}); err != nil {
			return fmt.Errorf("upsert skill file %q: %w", f.Path, err)
		}
	}
	return nil
}

func updateLabel(ctx context.Context, q *db.Queries, workspaceID pgtype.UUID, existing db.IssueLabel, src backup.BackupLabel) error {
	color := src.Color
	if color == "" {
		color = existing.Color
	}
	if _, err := q.UpdateLabel(ctx, db.UpdateLabelParams{
		ID:          existing.ID,
		WorkspaceID: workspaceID,
		Name:        pgtype.Text{String: existing.Name, Valid: true},
		Color:       pgtype.Text{String: color, Valid: true},
	}); err != nil {
		return fmt.Errorf("update label: %w", err)
	}
	return nil
}

func updateAgent(ctx context.Context, q *db.Queries, workspaceID pgtype.UUID, existing db.Agent, src backup.BackupAgent, remapSkill map[string]pgtype.UUID) error {
	// Agent update path is intentionally narrow: the live
	// PUT /api/agents/{id} handler is the canonical surface for
	// mutating agent fields. For restore we only need to refresh
	// the skill links and a couple of free-form fields, since the
	// rest of the agent config (runtime, model, env) is
	// runtime-bound and should not be silently overwritten from a
	// backup.
	if err := q.RemoveAllAgentSkills(ctx, existing.ID); err != nil {
		return fmt.Errorf("clear agent skills: %w", err)
	}
	for _, sid := range src.SkillIDs {
		target, ok := remapSkill[sid]
		if !ok {
			// Skill is not in the restore plan; skip silently
			// so a partial restore does not abort the agent
			// update.
			continue
		}
		if err := q.AddAgentSkill(ctx, db.AddAgentSkillParams{
			AgentID: existing.ID,
			SkillID: target,
		}); err != nil {
			return fmt.Errorf("add agent skill: %w", err)
		}
	}
	// Update the two text fields the existing UpdateAgent handles
	// (description / instructions). The other agent fields stay
	// as the live agent had them — runtime-bound state like
	// model, env, and visibility is intentionally NOT restored
	// because it would silently break the agent's runtime
	// connection.
	if _, err := q.UpdateAgent(ctx, db.UpdateAgentParams{
		ID:                 existing.ID,
		Name:               pgtype.Text{String: src.Name, Valid: true},
		Description:        pgtype.Text{String: src.Description, Valid: true},
		AvatarUrl:          existing.AvatarUrl,
		RuntimeConfig:      existing.RuntimeConfig,
		RuntimeMode:        pgtype.Text{String: existing.RuntimeMode, Valid: true},
		RuntimeID:          existing.RuntimeID,
		Visibility:         pgtype.Text{String: existing.Visibility, Valid: true},
		Status:             pgtype.Text{String: existing.Status, Valid: true},
		MaxConcurrentTasks: pgtype.Int4{Int32: existing.MaxConcurrentTasks, Valid: true},
		Instructions:       pgtype.Text{String: src.Instructions, Valid: true},
		CustomEnv:          existing.CustomEnv,
		CustomArgs:         existing.CustomArgs,
		McpConfig:          existing.McpConfig,
		Model:              existing.Model,
		ThinkingLevel:      existing.ThinkingLevel,
	}); err != nil {
		return fmt.Errorf("update agent: %w", err)
	}
	return nil
}

func updateProject(ctx context.Context, q *db.Queries, workspaceID pgtype.UUID, existing db.Project, src backup.BackupProject, remapMember, remapAgent map[string]pgtype.UUID) error {
	leadType, leadID, err := resolveProjectLead(ctx, q, workspaceID, src.Lead, remapMember, remapAgent)
	if err != nil {
		return err
	}
	status := src.Status
	if status == "" {
		status = existing.Status
	}
	if !isValidProjectStatus(status) {
		status = existing.Status
	}
	priority := src.Priority
	if priority == "" {
		priority = existing.Priority
	}
	if _, err := q.UpdateProject(ctx, db.UpdateProjectParams{
		ID:          existing.ID,
		Title:       pgtype.Text{String: src.Title, Valid: true},
		Description: textOr(src.Description, existing.Description),
		Icon:        textOr(src.Icon, existing.Icon),
		Status:      pgtype.Text{String: status, Valid: true},
		Priority:    pgtype.Text{String: priority, Valid: true},
		LeadType:    leadType,
		LeadID:      leadID,
	}); err != nil {
		return fmt.Errorf("update project: %w", err)
	}
	// Resources: clear + recreate, like skill files. The DB schema
	// does not expose a single "delete all" query, so we list
	// first and then delete each row.
	existingResources, err := q.ListProjectResources(ctx, existing.ID)
	if err != nil {
		return fmt.Errorf("list project resources: %w", err)
	}
	for _, er := range existingResources {
		if err := q.DeleteProjectResource(ctx, er.ID); err != nil {
			return fmt.Errorf("delete project resource: %w", err)
		}
	}
	for i, r := range src.Resources {
		ref := r.ResourceRef
		if len(ref) == 0 {
			ref = json.RawMessage("{}")
		}
		pos := int32(i)
		if r.Position != nil {
			pos = *r.Position
		}
		if _, err := q.CreateProjectResource(ctx, db.CreateProjectResourceParams{
			ProjectID:    existing.ID,
			WorkspaceID:  workspaceID,
			ResourceType: r.ResourceType,
			ResourceRef:  ref,
			Label:        textOr(r.Label, pgtype.Text{}),
			Position:     pos,
			CreatedBy:    pgtype.UUID{},
		}); err != nil {
			return fmt.Errorf("create project resource: %w", err)
		}
	}
	return nil
}

func updateSquad(ctx context.Context, q *db.Queries, workspaceID pgtype.UUID, existing db.Squad, src backup.BackupSquad, creatorID pgtype.UUID, remapAgent, remapMember map[string]pgtype.UUID) error {
	leaderID, err := resolveSquadLeader(ctx, q, workspaceID, src.Leader, remapAgent, remapMember)
	if err != nil {
		return err
	}
	if _, err := q.UpdateSquad(ctx, db.UpdateSquadParams{
		ID:           existing.ID,
		Name:         pgtype.Text{String: src.Name, Valid: true},
		Description:  pgtype.Text{String: src.Description, Valid: true},
		LeaderID:     leaderID,
		AvatarUrl:    textOr(src.AvatarURL, existing.AvatarUrl),
		Instructions: pgtype.Text{String: src.Instructions, Valid: true},
	}); err != nil {
		return fmt.Errorf("update squad: %w", err)
	}
	// Members: clear + recreate. List-then-delete mirrors the
	// project-resources loop above.
	existingMembers, err := q.ListSquadMembers(ctx, existing.ID)
	if err != nil {
		return fmt.Errorf("list squad members: %w", err)
	}
	for _, em := range existingMembers {
		if _, err := q.RemoveSquadMember(ctx, db.RemoveSquadMemberParams{
			SquadID:    existing.ID,
			MemberType: em.MemberType,
			MemberID:   em.MemberID,
		}); err != nil {
			return fmt.Errorf("remove squad member: %w", err)
		}
	}
	for _, m := range src.Members {
		var mid pgtype.UUID
		switch m.MemberType {
		case "agent":
			if v, ok := remapAgent[m.MemberID]; ok {
				mid = v
			} else {
				continue
			}
		case "member":
			if v, ok := remapMember[m.MemberID]; ok {
				mid = v
			} else {
				continue
			}
		default:
			continue
		}
		if _, err := q.AddSquadMember(ctx, db.AddSquadMemberParams{
			SquadID:    existing.ID,
			MemberType: m.MemberType,
			MemberID:   mid,
			Role:       m.Role,
		}); err != nil {
			return fmt.Errorf("add squad member: %w", err)
		}
	}
	_ = creatorID
	return nil
}

func updateAutopilot(ctx context.Context, q *db.Queries, workspaceID pgtype.UUID, existing db.Autopilot, src backup.BackupAutopilot, remapAgent, remapSquad, remapProject map[string]pgtype.UUID) error {
	assigneeType := src.Assignee.Type
	if assigneeType == "" {
		assigneeType = existing.AssigneeType
	}
	var assigneeID pgtype.UUID
	switch assigneeType {
	case "agent":
		if v, ok := remapAgent[src.Assignee.ID]; ok {
			assigneeID = v
		}
	case "squad":
		if v, ok := remapSquad[src.Assignee.ID]; ok {
			assigneeID = v
		}
	}
	if !assigneeID.Valid {
		// Fall back to the existing assignee so an overwrite
		// does not silently null the autopilot's executor.
		assigneeID = existing.AssigneeID
	}
	status := src.Status
	if status == "" {
		status = existing.Status
	}
	execMode := src.ExecutionMode
	if execMode == "" {
		execMode = existing.ExecutionMode
	}
	projectID := existing.ProjectID
	if src.ProjectID != "" {
		if v, ok := remapProject[src.ProjectID]; ok {
			projectID = v
		}
	}
	if _, err := q.UpdateAutopilot(ctx, db.UpdateAutopilotParams{
		ID:                 existing.ID,
		Title:              pgtype.Text{String: src.Name, Valid: true},
		Description:        existing.Description,
		AssigneeType:       pgtype.Text{String: assigneeType, Valid: true},
		AssigneeID:         assigneeID,
		Status:             pgtype.Text{String: status, Valid: true},
		ExecutionMode:      pgtype.Text{String: execMode, Valid: true},
		IssueTitleTemplate: existing.IssueTitleTemplate,
		ProjectID:          projectID,
	}); err != nil {
		return fmt.Errorf("update autopilot: %w", err)
	}
	return nil
}

// --- Section: create (insert) ---

func createAgent(ctx context.Context, q *db.Queries, workspaceID pgtype.UUID, src backup.BackupAgent, creatorID pgtype.UUID, remapSkill, remapMember map[string]pgtype.UUID) (db.Agent, error) {
	ownerID := creatorID
	if src.OwnerID != "" {
		if v, ok := remapMember[src.OwnerID]; ok {
			ownerID = v
		}
	}
	instructions := src.Instructions
	customEnv := []byte(src.CustomEnv)
	if len(customEnv) == 0 {
		customEnv = []byte("{}")
	}
	customArgs := []byte(src.CustomArgs)
	if len(customArgs) == 0 {
		customArgs = []byte("[]")
	}
	mcpConfig := []byte(src.McpConfig)
	if len(mcpConfig) == 0 {
		mcpConfig = []byte("{}")
	}
	visibility := src.Visibility
	if visibility == "" {
		visibility = "workspace"
	}
	// The runtime binding is instance-scoped: the source workspace's
	// runtime UUID does not exist on the target, so we attach the
	// restored agent to the destination workspace's first available
	// runtime. The agent's own runtime_mode + runtime_config
	// survive the cross-instance trip.
	runtimes, err := q.ListAgentRuntimes(ctx, workspaceID)
	if err != nil {
		return db.Agent{}, fmt.Errorf("list runtimes: %w", err)
	}
	if len(runtimes) == 0 {
		return db.Agent{}, fmt.Errorf("cannot restore agent: target workspace has no agent runtime")
	}
	primary := runtimes[0]
	runtimeMode := src.RuntimeMode
	if runtimeMode == "" {
		runtimeMode = primary.RuntimeMode
	}
	runtimeConfig := []byte(src.RuntimeConfig)
	if len(runtimeConfig) == 0 {
		runtimeConfig = []byte("{}")
	}
	maxConcurrent := int32(1)
	if src.MaxConcurrentTasks != nil {
		maxConcurrent = *src.MaxConcurrentTasks
	}
	a, err := q.CreateAgent(ctx, db.CreateAgentParams{
		WorkspaceID:        workspaceID,
		Name:               src.Name,
		Description:        src.Description,
		AvatarUrl:          textOr(src.AvatarURL, pgtype.Text{}),
		RuntimeMode:        runtimeMode,
		RuntimeConfig:      runtimeConfig,
		RuntimeID:          primary.ID,
		Visibility:         visibility,
		MaxConcurrentTasks: maxConcurrent,
		OwnerID:            ownerID,
		Instructions:       instructions,
		CustomEnv:          customEnv,
		CustomArgs:         customArgs,
		McpConfig:          mcpConfig,
		Model:              textOr(src.Model, pgtype.Text{}),
		ThinkingLevel:      textOr(src.ThinkingLevel, pgtype.Text{}),
	})
	if err != nil {
		return db.Agent{}, fmt.Errorf("create agent: %w", err)
	}
	for _, sid := range src.SkillIDs {
		target, ok := remapSkill[sid]
		if !ok {
			continue
		}
		if err := q.AddAgentSkill(ctx, db.AddAgentSkillParams{
			AgentID: a.ID,
			SkillID: target,
		}); err != nil {
			return db.Agent{}, fmt.Errorf("add agent skill: %w", err)
		}
	}
	return a, nil
}

func createProject(ctx context.Context, q *db.Queries, workspaceID pgtype.UUID, src backup.BackupProject, creatorID pgtype.UUID, remapMember, remapAgent map[string]pgtype.UUID) (db.Project, error) {
	leadType, leadID, err := resolveProjectLead(ctx, q, workspaceID, src.Lead, remapMember, remapAgent)
	if err != nil {
		return db.Project{}, err
	}
	status := src.Status
	if status == "" {
		status = "planned"
	}
	if !isValidProjectStatus(status) {
		status = "planned"
	}
	priority := src.Priority
	if priority == "" {
		priority = "medium"
	}
	p, err := q.CreateProject(ctx, db.CreateProjectParams{
		WorkspaceID: workspaceID,
		Title:       src.Title,
		Description: textOr(src.Description, pgtype.Text{}),
		Icon:        textOr(src.Icon, pgtype.Text{}),
		Status:      status,
		LeadType:    leadType,
		LeadID:      leadID,
		Priority:    priority,
	})
	if err != nil {
		return db.Project{}, fmt.Errorf("create project: %w", err)
	}
	for i, r := range src.Resources {
		ref := r.ResourceRef
		if len(ref) == 0 {
			ref = json.RawMessage("{}")
		}
		pos := int32(i)
		if r.Position != nil {
			pos = *r.Position
		}
		if _, err := q.CreateProjectResource(ctx, db.CreateProjectResourceParams{
			ProjectID:    p.ID,
			WorkspaceID:  workspaceID,
			ResourceType: r.ResourceType,
			ResourceRef:  ref,
			Label:        textOr(r.Label, pgtype.Text{}),
			Position:     pos,
			CreatedBy:    creatorID,
		}); err != nil {
			return db.Project{}, fmt.Errorf("create project resource: %w", err)
		}
	}
	return p, nil
}

func createSquad(ctx context.Context, q *db.Queries, workspaceID pgtype.UUID, src backup.BackupSquad, creatorID pgtype.UUID, remapAgent, remapMember map[string]pgtype.UUID) (db.Squad, error) {
	leaderID, err := resolveSquadLeader(ctx, q, workspaceID, src.Leader, remapAgent, remapMember)
	if err != nil {
		return db.Squad{}, err
	}
	s, err := q.CreateSquad(ctx, db.CreateSquadParams{
		WorkspaceID: workspaceID,
		Name:        src.Name,
		Description: src.Description,
		LeaderID:    leaderID,
		CreatorID:   creatorID,
		AvatarUrl:   textOr(src.AvatarURL, pgtype.Text{}),
	})
	if err != nil {
		return db.Squad{}, fmt.Errorf("create squad: %w", err)
	}
	// CreateSquad leaves instructions empty; UpdateSquad handles
	// the rest of the field set, so apply instructions on the
	// fresh row right after creation.
	if _, err := q.UpdateSquad(ctx, db.UpdateSquadParams{
		ID:           s.ID,
		Name:         pgtype.Text{String: src.Name, Valid: true},
		Description:  pgtype.Text{String: src.Description, Valid: true},
		LeaderID:     s.LeaderID,
		AvatarUrl:    s.AvatarUrl,
		Instructions: pgtype.Text{String: src.Instructions, Valid: true},
	}); err != nil {
		return db.Squad{}, fmt.Errorf("update squad instructions: %w", err)
	}
	for _, m := range src.Members {
		var mid pgtype.UUID
		switch m.MemberType {
		case "agent":
			if v, ok := remapAgent[m.MemberID]; ok {
				mid = v
			} else {
				continue
			}
		case "member":
			if v, ok := remapMember[m.MemberID]; ok {
				mid = v
			} else {
				continue
			}
		default:
			continue
		}
		if _, err := q.AddSquadMember(ctx, db.AddSquadMemberParams{
			SquadID:    s.ID,
			MemberType: m.MemberType,
			MemberID:   mid,
			Role:       m.Role,
		}); err != nil {
			return db.Squad{}, fmt.Errorf("add squad member: %w", err)
		}
	}
	return s, nil
}

func createAutopilot(ctx context.Context, q *db.Queries, workspaceID pgtype.UUID, src backup.BackupAutopilot, creatorID pgtype.UUID, remapAgent, remapSquad, remapProject map[string]pgtype.UUID) (db.Autopilot, error) {
	assigneeType := src.Assignee.Type
	if !isValidAutopilotAssigneeType(assigneeType) {
		assigneeType = "agent"
	}
	var assigneeID pgtype.UUID
	switch assigneeType {
	case "agent":
		if v, ok := remapAgent[src.Assignee.ID]; ok {
			assigneeID = v
		}
	case "squad":
		if v, ok := remapSquad[src.Assignee.ID]; ok {
			assigneeID = v
		}
	}
	if !assigneeID.Valid {
		return db.Autopilot{}, fmt.Errorf("autopilot assignee %q could not be resolved", src.Assignee.ID)
	}
	status := src.Status
	if status == "" {
		status = "active"
	}
	execMode := src.ExecutionMode
	if execMode == "" {
		execMode = "create_issue"
	}
	projectID := pgtype.UUID{}
	if src.ProjectID != "" {
		if v, ok := remapProject[src.ProjectID]; ok {
			projectID = v
		}
	}
	a, err := q.CreateAutopilot(ctx, db.CreateAutopilotParams{
		WorkspaceID:        workspaceID,
		Title:              src.Name,
		AssigneeType:       assigneeType,
		AssigneeID:         assigneeID,
		Status:             status,
		ExecutionMode:      execMode,
		CreatedByType:      "member",
		CreatedByID:        creatorID,
		Description:        textOr(src.Name, pgtype.Text{}),
		IssueTitleTemplate: pgtype.Text{},
		ProjectID:          projectID,
	})
	if err != nil {
		return db.Autopilot{}, fmt.Errorf("create autopilot: %w", err)
	}
	// Critical gap #1 from the Planner's review: autopilot webhook
	// tokens must NEVER be copied from the backup. We don't restore
	// triggers in M-25 (the export shape doesn't carry them yet),
	// and the autopilot create path above never asks the database
	// for a webhook token — so there is no path for a backup token
	// to leak into the new row. This is a structural guarantee
	// rather than a "drop the field" check, so we leave a marker
	// here for reviewers tracing the contract.
	return a, nil
}

func createIssue(ctx context.Context, q *db.Queries, workspaceID pgtype.UUID, src backup.BackupIssue, creatorID pgtype.UUID, remapProject, remapLabel, remapMember, remapAgent map[string]pgtype.UUID) (db.Issue, error) {
	projectID := pgtype.UUID{}
	if src.ProjectID != "" {
		if v, ok := remapProject[src.ProjectID]; ok {
			projectID = v
		}
	}
	assigneeType := textOr(src.Assignee.Type, pgtype.Text{})
	assigneeID := pgtype.UUID{}
	if src.Assignee.Type == "agent" {
		if v, ok := remapAgent[src.Assignee.ID]; ok {
			assigneeID = v
		}
	} else if src.Assignee.Type == "member" {
		if v, ok := remapMember[src.Assignee.ID]; ok {
			assigneeID = v
		}
	}
	creatorType := src.Creator.Type
	if creatorType != "member" && creatorType != "agent" {
		creatorType = "member"
	}
	creatorUUID := creatorID
	if creatorType == "agent" {
		if v, ok := remapAgent[src.Creator.ID]; ok {
			creatorUUID = v
		}
	} else if src.Creator.Type == "member" {
		if v, ok := remapMember[src.Creator.ID]; ok {
			creatorUUID = v
		}
	}
	status := src.Status
	if status == "" {
		status = "todo"
	}
	priority := src.Priority
	if priority == "" {
		priority = "medium"
	}
	// Issue number is allocated from the workspace's counter; we
	// use the executor so the counter and the issue INSERT are
	// atomic with the rest of the restore.
	number, err := q.IncrementIssueCounter(ctx, workspaceID)
	if err != nil {
		return db.Issue{}, fmt.Errorf("increment issue counter: %w", err)
	}
	is, err := q.CreateIssue(ctx, db.CreateIssueParams{
		WorkspaceID:   workspaceID,
		Title:         src.Title,
		Description:   textOr(src.Description, pgtype.Text{}),
		Status:        status,
		Priority:      priority,
		AssigneeType:  assigneeType,
		AssigneeID:    assigneeID,
		CreatorType:   creatorType,
		CreatorID:     creatorUUID,
		ParentIssueID: pgtype.UUID{},
		Position:      src.Position,
		StartDate:     dateOr(src.StartDate, pgtype.Date{}),
		DueDate:       dateOr(src.DueDate, pgtype.Date{}),
		Number:        number,
		ProjectID:     projectID,
	})
	if err != nil {
		return db.Issue{}, fmt.Errorf("create issue: %w", err)
	}
	// Labels: attach via the standard AttachLabelToIssue path.
	for _, lid := range src.LabelIDs {
		target, ok := remapLabel[lid]
		if !ok {
			continue
		}
		if err := q.AttachLabelToIssue(ctx, db.AttachLabelToIssueParams{
			IssueID:     is.ID,
			LabelID:     target,
			WorkspaceID: workspaceID,
		}); err != nil {
			return db.Issue{}, fmt.Errorf("attach label: %w", err)
		}
	}
	// Metadata: set the keys we captured. The export stores the
	// metadata as a JSON object; we replay each key through
	// SetIssueMetadataKey.
	if len(src.Metadata) > 0 {
		var meta map[string]json.RawMessage
		if err := json.Unmarshal(src.Metadata, &meta); err == nil {
			for k, v := range meta {
				if _, err := q.SetIssueMetadataKey(ctx, db.SetIssueMetadataKeyParams{
					Key:         k,
					Value:       []byte(v),
					ID:          is.ID,
					WorkspaceID: workspaceID,
				}); err != nil {
					return db.Issue{}, fmt.Errorf("set issue metadata %q: %w", k, err)
				}
			}
		}
	}
	return is, nil
}

// createComments restores the comments attached to a freshly created
// issue. The function builds a remap of source-comment-id to
// target-comment-id and uses it to thread replies. Reactions are
// restored through the same per-comment remap so they survive the
// issue re-keying.
func createComments(ctx context.Context, q *db.Queries, workspaceID, issueID pgtype.UUID, comments []backup.BackupComment, remapMember, remapAgent map[string]pgtype.UUID) (map[string]pgtype.UUID, error) {
	if len(comments) == 0 {
		return nil, nil
	}
	commentMap := map[string]pgtype.UUID{}
	// First pass: roots (no parent).
	for _, cm := range comments {
		if cm.ParentID != "" {
			continue
		}
		authorID, authorType, err := resolveActor(cm.Author, remapMember, remapAgent)
		if err != nil {
			return nil, err
		}
		c, err := q.CreateComment(ctx, db.CreateCommentParams{
			IssueID:     issueID,
			WorkspaceID: workspaceID,
			AuthorType:  authorType,
			AuthorID:    authorID,
			Content:     cm.Content,
			Type:        commentTypeOr(cm.Type),
			ParentID:    pgtype.UUID{},
		})
		if err != nil {
			return nil, fmt.Errorf("create root comment: %w", err)
		}
		commentMap[cm.ID] = c.ID
		for _, rx := range cm.Reactions {
			rxActorID, rxActorType, err := resolveActor(rx.Actor, remapMember, remapAgent)
			if err != nil {
				return nil, err
			}
			if _, err := q.AddReaction(ctx, db.AddReactionParams{
				CommentID:   c.ID,
				WorkspaceID: workspaceID,
				ActorType:   rxActorType,
				ActorID:     rxActorID,
				Emoji:       rx.Emoji,
			}); err != nil {
				return nil, fmt.Errorf("add reaction: %w", err)
			}
		}
	}
	// Second pass: replies, parent_id resolved through commentMap.
	for _, cm := range comments {
		if cm.ParentID == "" {
			continue
		}
		authorID, authorType, err := resolveActor(cm.Author, remapMember, remapAgent)
		if err != nil {
			return nil, err
		}
		parentID, ok := commentMap[cm.ParentID]
		if !ok {
			return nil, fmt.Errorf("comment %q has unresolved parent %q", cm.ID, cm.ParentID)
		}
		c, err := q.CreateComment(ctx, db.CreateCommentParams{
			IssueID:     issueID,
			WorkspaceID: workspaceID,
			AuthorType:  authorType,
			AuthorID:    authorID,
			Content:     cm.Content,
			Type:        commentTypeOr(cm.Type),
			ParentID:    parentID,
		})
		if err != nil {
			return nil, fmt.Errorf("create reply comment: %w", err)
		}
		commentMap[cm.ID] = c.ID
		for _, rx := range cm.Reactions {
			rxActorID, rxActorType, err := resolveActor(rx.Actor, remapMember, remapAgent)
			if err != nil {
				return nil, err
			}
			if _, err := q.AddReaction(ctx, db.AddReactionParams{
				CommentID:   c.ID,
				WorkspaceID: workspaceID,
				ActorType:   rxActorType,
				ActorID:     rxActorID,
				Emoji:       rx.Emoji,
			}); err != nil {
				return nil, fmt.Errorf("add reaction: %w", err)
			}
		}
	}
	return commentMap, nil
}

// --- Reference resolvers ---

func resolveProjectLead(_ context.Context, _ *db.Queries, _ pgtype.UUID, lead backup.BackupActor, remapMember, remapAgent map[string]pgtype.UUID) (pgtype.Text, pgtype.UUID, error) {
	if lead.Type == "" {
		return pgtype.Text{}, pgtype.UUID{}, nil
	}
	switch lead.Type {
	case "agent":
		if v, ok := remapAgent[lead.ID]; ok {
			return pgtype.Text{String: "agent", Valid: true}, v, nil
		}
	case "member":
		if v, ok := remapMember[lead.ID]; ok {
			return pgtype.Text{String: "member", Valid: true}, v, nil
		}
	}
	return pgtype.Text{}, pgtype.UUID{}, nil
}

func resolveSquadLeader(_ context.Context, _ *db.Queries, _ pgtype.UUID, leader backup.BackupActor, remapAgent, remapMember map[string]pgtype.UUID) (pgtype.UUID, error) {
	if leader.Type == "" {
		return pgtype.UUID{}, nil
	}
	switch leader.Type {
	case "agent":
		if v, ok := remapAgent[leader.ID]; ok {
			return v, nil
		}
	case "member":
		if v, ok := remapMember[leader.ID]; ok {
			return v, nil
		}
	}
	return pgtype.UUID{}, nil
}

func resolveActor(actor backup.BackupActor, remapMember, remapAgent map[string]pgtype.UUID) (pgtype.UUID, string, error) {
	switch actor.Type {
	case "agent":
		if v, ok := remapAgent[actor.ID]; ok {
			return v, "agent", nil
		}
		return pgtype.UUID{}, "", fmt.Errorf("agent %q not resolved", actor.ID)
	case "member":
		if v, ok := remapMember[actor.ID]; ok {
			return v, "member", nil
		}
		return pgtype.UUID{}, "", fmt.Errorf("member %q not resolved", actor.ID)
	default:
		return pgtype.UUID{}, "", fmt.Errorf("actor type %q not supported", actor.Type)
	}
}

// --- Utility helpers ---

// firstWorkspaceOwnerUUID returns the user_id of the workspace's first
// owner; used as the default created_by for restored rows where the
// backup did not capture a member identity.
func firstWorkspaceOwnerUUID(ctx context.Context, q *db.Queries, workspaceID pgtype.UUID) (pgtype.UUID, error) {
	members, err := q.ListMembers(ctx, workspaceID)
	if err != nil {
		return pgtype.UUID{}, err
	}
	for _, m := range members {
		if m.Role == "owner" {
			return m.UserID, nil
		}
	}
	if len(members) > 0 {
		return members[0].UserID, nil
	}
	return pgtype.UUID{}, nil
}

func textOr(s string, fallback pgtype.Text) pgtype.Text {
	if s == "" {
		return fallback
	}
	return pgtype.Text{String: s, Valid: true}
}

func commentTypeOr(s string) string {
	if s == "" {
		return "comment"
	}
	return s
}

func dateOr(t *time.Time, fallback pgtype.Date) pgtype.Date {
	if t == nil {
		return fallback
	}
	return pgtype.Date{Time: *t, Valid: true}
}

func int32Or(p *int32, fallback int32) int32 {
	if p == nil {
		return fallback
	}
	return *p
}

func isValidProjectStatus(s string) bool {
	switch s {
	case "planned", "in_progress", "paused", "completed", "cancelled":
		return true
	}
	return false
}
