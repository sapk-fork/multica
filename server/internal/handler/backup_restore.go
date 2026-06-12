package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/backup"
	skillpkg "github.com/multica-ai/multica/server/internal/skill"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// restoreAction is the per-item outcome of a restore pass.
type restoreAction string

const (
	restoreActionCreate restoreAction = "create"
	restoreActionUpdate restoreAction = "update"
	restoreActionSkip   restoreAction = "skip"
	restoreActionError  restoreAction = "error"
)

// restoreItemType is the JSON section label for a restore item.
type restoreItemType string

const (
	restoreTypeSkill     restoreItemType = "skill"
	restoreTypeLabel     restoreItemType = "label"
	restoreTypeAgent     restoreItemType = "agent"
	restoreTypeProject   restoreItemType = "project"
	restoreTypeIssue     restoreItemType = "issue"
	restoreTypeSquad     restoreItemType = "squad"
	restoreTypeAutopilot restoreItemType = "autopilot"
)

// restoreRequest is the body for both /api/backup/restore/preview and
// /api/backup/restore. The Backup field carries the raw backup file; the
// server runs the same plan-shape for both endpoints, only the "do it"
// flag differs.
type restoreRequest struct {
	Backup           json.RawMessage `json:"backup"`
	WorkspaceID      string          `json:"workspace_id"`
	Overwrite        bool            `json:"overwrite"`
	SelectedItems    []string        `json:"selected_items,omitempty"`
	SelectedIDs      []string        `json:"selected_ids,omitempty"`
	IncludeWorkspace bool            `json:"include_workspace"`
}

// restoreMissingDep describes a dependency that an item needs but which
// the request did not select and which is not already present in the
// target workspace. Surfaced in the preview response per-item so the UI
// can show "this agent needs skill X but you didn't select it".
type restoreMissingDep struct {
	Type       restoreItemType `json:"type"`
	Identifier string          `json:"identifier"`
	Reason     string          `json:"reason"`
}

// restoreItem is one entry in the response items list. SourceID is the
// backup's local UUID (meaningful only inside the source workspace);
// TargetID is the row in the destination workspace.
type restoreItem struct {
	Type        restoreItemType     `json:"type"`
	SourceID    string              `json:"source_id"`
	Identifier  string              `json:"identifier"`
	Action      restoreAction       `json:"action"`
	Reason      string              `json:"reason,omitempty"`
	TargetID    string              `json:"target_id,omitempty"`
	MissingDeps []restoreMissingDep `json:"missing_deps,omitempty"`
}

// restoreResponse is the body returned by both preview and execute.
type restoreResponse struct {
	Items     []restoreItem           `json:"items"`
	Sections  map[string]int          `json:"section_summary"`
	Errors    []string                `json:"errors,omitempty"`
	Workspace *restoreWorkspaceResult `json:"workspace,omitempty"`
}

type restoreWorkspaceResult struct {
	Applied bool   `json:"applied"`
	Skipped bool   `json:"skipped"`
	Reason  string `json:"reason,omitempty"`
	// Changes lists every field that the restore overwrote on the
	// target workspace, in the form {field, before, after}. The
	// IssuePrefix in particular is worth surfacing because it
	// changes the public identifier scheme for every new issue
	// created in the workspace after the restore.
	Changes []restoreWorkspaceChange `json:"changes,omitempty"`
}

// restoreWorkspaceChange is a single before/after pair for an
// overwritten workspace field. The Before/After strings are the
// post-stringification view (empty for unset, raw text otherwise)
// so the caller can render a clear diff without re-parsing the
// raw json.RawMessage values.
type restoreWorkspaceChange struct {
	Field  string `json:"field"`
	Before string `json:"before"`
	After  string `json:"after"`
}

// restorePlanner carries the per-restore state: the target workspace,
// the loaded backup, the cross-section remap tables, and the
// section/item selections. It is a value type — methods mutate copies
// of the maps because we want each planner to be independent when the
// handler is called concurrently.
type restorePlanner struct {
	workspaceID        pgtype.UUID
	workspace          db.Workspace
	workspaceOwnerUUID pgtype.UUID
	backup             *backup.BackupFile
	overwrite          bool
	includeWorkspace   bool
	selectedItems      map[restoreItemType]bool
	selectedIDs        map[string]bool
	doIt               bool

	// cross-section remap. Keyed by the source ID from the backup
	// (whatever string the caller put in `id`); value is the target
	// workspace's UUID. Populated as each section commits. When a
	// later section needs a reference to a row that was not part of
	// the request (or that was skipped), the planner falls back to
	// looking the row up by name in the target workspace.
	remapSkill   map[string]pgtype.UUID
	remapLabel   map[string]pgtype.UUID
	remapAgent   map[string]pgtype.UUID
	remapProject map[string]pgtype.UUID
	remapIssue   map[string]pgtype.UUID
	remapSquad   map[string]pgtype.UUID
	remapMember  map[string]pgtype.UUID

	// items accumulates per-row results as each section runs.
	items []restoreItem
	// errors collects section-level errors (e.g. transaction failure).
	errors []string

	// workspaceApplied is set by applyWorkspaceSettings when the
	// caller opted in and the workspace section was processed.
	workspaceApplied bool
	// workspaceChanges captures the per-field before/after diff
	// produced by applyWorkspaceSettings so the response can show
	// exactly which fields the restore overwrote.
	workspaceChanges []restoreWorkspaceChange
}

// --- Handlers ---

// RestoreBackupPreview runs the full restore plan without writing and
// returns the per-item decision + dependency warnings. Read-only against
// the database (no INSERTs, no UPDATEs).
func (h *Handler) RestoreBackupPreview(w http.ResponseWriter, r *http.Request) {
	h.handleRestore(w, r, false)
}

// RestoreBackup applies the plan in a single transaction. On any
// per-item error the whole restore is rolled back and the response
// surfaces the first failure with the partial plan.
func (h *Handler) RestoreBackup(w http.ResponseWriter, r *http.Request) {
	h.handleRestore(w, r, true)
}

func (h *Handler) handleRestore(w http.ResponseWriter, r *http.Request, doIt bool) {
	var req restoreRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.WorkspaceID == "" {
		writeError(w, http.StatusBadRequest, "workspace_id is required")
		return
	}
	wsUUID, ok := parseUUIDOrBadRequest(w, req.WorkspaceID, "workspace_id")
	if !ok {
		return
	}

	if _, ok := h.requireWorkspaceRole(w, r, req.WorkspaceID, "workspace not found", "owner", "admin"); !ok {
		return
	}

	ws, err := h.Queries.GetWorkspace(r.Context(), wsUUID)
	if err != nil {
		writeError(w, http.StatusNotFound, "workspace not found")
		return
	}
	ownerUUID, err := firstWorkspaceOwnerUUID(r.Context(), h.Queries, wsUUID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to resolve workspace owner: "+err.Error())
		return
	}

	bf, err := backup.Unmarshal(req.Backup)
	if err != nil {
		writeError(w, http.StatusBadRequest, "backup is invalid: "+err.Error())
		return
	}

	planner := &restorePlanner{
		workspaceID:        wsUUID,
		workspace:          ws,
		workspaceOwnerUUID: ownerUUID,
		backup:             bf,
		overwrite:          req.Overwrite,
		includeWorkspace:   req.IncludeWorkspace,
		selectedItems:      parseRestoreItemSelection(req.SelectedItems),
		selectedIDs:        stringSet(req.SelectedIDs),
		doIt:               doIt,
		remapSkill:         map[string]pgtype.UUID{},
		remapLabel:         map[string]pgtype.UUID{},
		remapAgent:         map[string]pgtype.UUID{},
		remapProject:       map[string]pgtype.UUID{},
		remapIssue:         map[string]pgtype.UUID{},
		remapSquad:         map[string]pgtype.UUID{},
		remapMember:        map[string]pgtype.UUID{},
	}

	// On execute, wrap the whole pipeline in a single transaction
	// so an infrastructure-level failure (DB error, aborted tx)
	// rolls every written row back together. The pipeline's own
	// per-item write failures are NOT rolled back at this level
	// — they are recorded on the item as action="error" and the
	// section continues, so a partial restore surfaces every
	// failure to the operator instead of stopping at the first.
	// On preview we use the live Queries handle so any error
	// reflects the real state but no row is committed.
	if doIt {
		tx, err := h.TxStarter.Begin(r.Context())
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to start transaction")
			return
		}
		defer tx.Rollback(r.Context())
		qtx := h.Queries.WithTx(tx)
		if err := planner.run(r.Context(), qtx); err != nil {
			writeJSON(w, http.StatusInternalServerError, planner.response(err))
			return
		}
		if err := tx.Commit(r.Context()); err != nil {
			writeJSON(w, http.StatusInternalServerError, planner.response(err))
			return
		}
	} else {
		if err := planner.run(r.Context(), h.Queries); err != nil {
			writeJSON(w, http.StatusInternalServerError, planner.response(err))
			return
		}
	}

	writeJSON(w, http.StatusOK, planner.response(nil))
}

// run is the section pipeline. Sections run in dependency order so
// remap tables are populated before the sections that reference them.
//
// Atomicity model: this pipeline runs inside a single DB transaction
// at the caller (handleRestore), but the atomicity boundary inside
// the pipeline is per-section. Per-item write failures inside a
// section are recorded as action="error" on the item and the section
// continues with the next item — that lets a partial restore
// surface every failure to the operator instead of stopping at the
// first one. Only infrastructure-level errors (DB failure, find*
// query failure, transaction-aborted) propagate up and roll the
// whole restore back.
func (p *restorePlanner) run(ctx context.Context, q *db.Queries) error {
	// Member resolution must run before any section that may
	// reference a member actor — projects (lead), agents (owner),
	// squads (leader, members), and issues (creator, comment
	// authors). It is gated on the backup actually carrying a
	// members section so an empty backup does not pay the
	// ListMembersWithUser round-trip.
	if len(p.backup.Members) > 0 {
		if err := p.resolveMembers(ctx, q); err != nil {
			return err
		}
	}
	if err := p.restoreSkills(ctx, q); err != nil {
		return err
	}
	if err := p.restoreLabels(ctx, q); err != nil {
		return err
	}
	if err := p.restoreAgents(ctx, q); err != nil {
		return err
	}
	if err := p.restoreProjects(ctx, q); err != nil {
		return err
	}
	if err := p.restoreIssues(ctx, q); err != nil {
		return err
	}
	if err := p.restoreSquads(ctx, q); err != nil {
		return err
	}
	if err := p.restoreAutopilots(ctx, q); err != nil {
		return err
	}
	if p.backup.Workspace != nil && p.doIt && p.includeWorkspace {
		if err := p.applyWorkspaceSettings(ctx, q); err != nil {
			return err
		}
	}
	return nil
}

// response is the final shape returned by preview/execute.
func (p *restorePlanner) response(topErr error) restoreResponse {
	sections := map[string]int{}
	for _, it := range p.items {
		sections[string(it.Type)+":"+string(it.Action)]++
	}
	out := restoreResponse{
		Items:    p.items,
		Sections: sections,
		Errors:   p.errors,
	}
	if p.backup != nil && p.backup.Workspace != nil {
		ws := &restoreWorkspaceResult{}
		if !p.doIt {
			ws.Skipped = true
			ws.Reason = "preview does not apply workspace settings"
		} else if !p.workspaceApplied {
			ws.Skipped = true
			ws.Reason = "include_workspace flag was not set"
		} else {
			ws.Applied = true
			ws.Changes = p.workspaceChanges
		}
		out.Workspace = ws
	}
	if topErr != nil {
		out.Errors = append(out.Errors, topErr.Error())
	}
	return out
}

// --- Section runners ---
// Each section follows the same shape:
//
//  1. Filter by SelectedItems / SelectedIDs.
//  2. Compute missing_deps (used in the preview response).
//  3. Resolve any conflict against the target workspace (by name).
//  4. Apply the section's create/update/skip/error decision.
//  5. Append the resulting restoreItem to the planner.
//
// All writes happen on the supplied *db.Queries (which may be
// transaction-scoped); the planner is the single owner of cross-section
// state.

// restoreSkills restores skill rows AND their attached files. The
// files are re-created through qtx.UpsertSkillFile — the same code
// path the live CreateSkill endpoint uses, not a raw INSERT.
func (p *restorePlanner) restoreSkills(ctx context.Context, q *db.Queries) error {
	if !p.sectionEnabled(restoreTypeSkill) {
		return nil
	}
	for _, sk := range p.backup.Skills {
		if !p.itemEnabled(sk.ID) {
			continue
		}
		item := restoreItem{
			Type:       restoreTypeSkill,
			SourceID:   sk.ID,
			Identifier: sk.Name,
		}

		existing, found, err := findSkillByName(ctx, q, p.workspaceID, sk.Name)
		if err != nil {
			return err
		}

		switch {
		case found && !p.overwrite:
			item.Action = restoreActionSkip
			item.Reason = "skill with same name already exists"
			item.TargetID = uuidToString(existing.ID)
			p.remapSkill[sk.ID] = existing.ID
		case found && p.overwrite:
			if p.doIt {
				if err := updateSkill(ctx, q, p.workspaceID, existing, sk); err != nil {
					return err
				}
			}
			item.Action = restoreActionUpdate
			item.TargetID = uuidToString(existing.ID)
			p.remapSkill[sk.ID] = existing.ID
		default:
			if p.doIt {
				files := make([]CreateSkillFileRequest, 0, len(sk.Files))
				for _, f := range sk.Files {
					if !validateFilePath(f.Path) {
						continue
					}
					if skillpkg.IsReservedContentPath(f.Path) {
						continue
					}
					files = append(files, CreateSkillFileRequest{Path: f.Path, Content: f.Content})
				}
				created, err := createSkillWithFilesInTx(ctx, q, skillCreateInput{
					WorkspaceID: p.workspaceID,
					CreatorID:   p.workspaceOwnerUUID,
					Name:        sk.Name,
					Description: sk.Description,
					Content:     sk.Content,
					Config:      sk.Config,
					Files:       files,
				})
				if err != nil {
					item.Action = restoreActionError
					item.Reason = err.Error()
					p.recordItem(item)
					return nil
				}
				createdID := parseUUID(created.ID)
				item.TargetID = created.ID
				p.remapSkill[sk.ID] = createdID
			}
			item.Action = restoreActionCreate
		}
		p.recordItem(item)
	}
	return nil
}

func (p *restorePlanner) restoreLabels(ctx context.Context, q *db.Queries) error {
	if !p.sectionEnabled(restoreTypeLabel) {
		return nil
	}
	for _, lb := range p.backup.Labels {
		if !p.itemEnabled(lb.ID) {
			continue
		}
		item := restoreItem{
			Type:       restoreTypeLabel,
			SourceID:   lb.ID,
			Identifier: lb.Name,
		}
		existing, found, err := findLabelByName(ctx, q, p.workspaceID, lb.Name)
		if err != nil {
			return err
		}
		switch {
		case found && !p.overwrite:
			item.Action = restoreActionSkip
			item.Reason = "label with same name already exists"
			item.TargetID = uuidToString(existing.ID)
			p.remapLabel[lb.ID] = existing.ID
		case found && p.overwrite:
			if p.doIt {
				if err := updateLabel(ctx, q, p.workspaceID, existing, lb); err != nil {
					return err
				}
			}
			item.Action = restoreActionUpdate
			item.TargetID = uuidToString(existing.ID)
			p.remapLabel[lb.ID] = existing.ID
		default:
			if p.doIt {
				color := lb.Color
				if color == "" {
					color = "#808080"
				}
				created, err := q.CreateLabel(ctx, db.CreateLabelParams{
					WorkspaceID: p.workspaceID,
					Name:        lb.Name,
					Color:       color,
				})
				if err != nil {
					item.Action = restoreActionError
					item.Reason = err.Error()
					p.recordItem(item)
					return nil
				}
				item.TargetID = uuidToString(created.ID)
				p.remapLabel[lb.ID] = created.ID
			}
			item.Action = restoreActionCreate
		}
		p.recordItem(item)
	}
	return nil
}

func (p *restorePlanner) restoreAgents(ctx context.Context, q *db.Queries) error {
	if !p.sectionEnabled(restoreTypeAgent) {
		return nil
	}
	for _, ag := range p.backup.Agents {
		if !p.itemEnabled(ag.ID) {
			continue
		}
		item := restoreItem{
			Type:       restoreTypeAgent,
			SourceID:   ag.ID,
			Identifier: ag.Name,
		}
		// missing_deps is computed before the action so preview
		// always reports the dependency view, not the post-decision
		// view.
		missing := p.agentMissingDeps(ag)

		existing, found, err := findAgentByName(ctx, q, p.workspaceID, ag.Name)
		if err != nil {
			return err
		}
		switch {
		case found && !p.overwrite:
			item.Action = restoreActionSkip
			item.Reason = "agent with same name already exists"
			item.TargetID = uuidToString(existing.ID)
			item.MissingDeps = missing
			p.remapAgent[ag.ID] = existing.ID
		case found && p.overwrite:
			if p.doIt {
				if err := updateAgent(ctx, q, p.workspaceID, existing, ag, p.remapSkill); err != nil {
					return err
				}
			}
			item.Action = restoreActionUpdate
			item.TargetID = uuidToString(existing.ID)
			p.remapAgent[ag.ID] = existing.ID
		default:
			if len(missing) > 0 && p.doIt {
				// Refuse to create an agent whose deps would be
				// missing — surface the gap, do not write.
				item.Action = restoreActionSkip
				item.Reason = "missing dependencies"
				item.MissingDeps = missing
				p.recordItem(item)
				continue
			}
			if p.doIt {
				created, err := createAgent(ctx, q, p.workspaceID, ag, p.workspaceOwnerUUID, p.remapSkill, p.remapMember)
				if err != nil {
					item.Action = restoreActionError
					item.Reason = err.Error()
					p.recordItem(item)
					return nil
				}
				item.TargetID = uuidToString(created.ID)
				p.remapAgent[ag.ID] = created.ID
			}
			item.Action = restoreActionCreate
			item.MissingDeps = missing
		}
		p.recordItem(item)
	}
	return nil
}

func (p *restorePlanner) restoreProjects(ctx context.Context, q *db.Queries) error {
	if !p.sectionEnabled(restoreTypeProject) {
		return nil
	}
	for _, pr := range p.backup.Projects {
		if !p.itemEnabled(pr.ID) {
			continue
		}
		item := restoreItem{
			Type:       restoreTypeProject,
			SourceID:   pr.ID,
			Identifier: pr.Title,
		}
		existing, found, err := findProjectByTitle(ctx, q, p.workspaceID, pr.Title)
		if err != nil {
			return err
		}
		switch {
		case found && !p.overwrite:
			item.Action = restoreActionSkip
			item.Reason = "project with same title already exists"
			item.TargetID = uuidToString(existing.ID)
			p.remapProject[pr.ID] = existing.ID
		case found && p.overwrite:
			if p.doIt {
				if err := updateProject(ctx, q, p.workspaceID, existing, pr, p.remapMember, p.remapAgent); err != nil {
					return err
				}
			}
			item.Action = restoreActionUpdate
			item.TargetID = uuidToString(existing.ID)
			p.remapProject[pr.ID] = existing.ID
		default:
			if p.doIt {
				created, err := createProject(ctx, q, p.workspaceID, pr, p.workspaceOwnerUUID, p.remapMember, p.remapAgent)
				if err != nil {
					item.Action = restoreActionError
					item.Reason = err.Error()
					p.recordItem(item)
					return nil
				}
				item.TargetID = uuidToString(created.ID)
				p.remapProject[pr.ID] = created.ID
			}
			item.Action = restoreActionCreate
		}
		p.recordItem(item)
	}
	return nil
}

func (p *restorePlanner) restoreIssues(ctx context.Context, q *db.Queries) error {
	if !p.sectionEnabled(restoreTypeIssue) {
		return nil
	}
	// Member remap is now built once in run() before any section
	// runs, so the issues section can rely on it being populated
	// when the backup carried any members.
	for _, is := range p.backup.Issues {
		if !p.itemEnabled(is.ID) {
			continue
		}
		item := restoreItem{
			Type:       restoreTypeIssue,
			SourceID:   is.ID,
			Identifier: is.Title,
		}
		missing := p.issueMissingDeps(is)

		// Match issues by title for conflict detection. The brief
		// suggests matching by number, but the source workspace's
		// issue numbers are not stable across instances (the target
		// workspace's counter advances independently), so a
		// title-based match is both more useful in practice and
		// consistent with the rest of the conflict detection in
		// this handler.
		existing, found, err := findIssueByTitle(ctx, q, p.workspaceID, is.Title)
		if err != nil {
			return err
		}

		if found && !p.overwrite {
			item.Action = restoreActionSkip
			item.Reason = "issue with same title already exists"
			item.TargetID = uuidToString(existing.ID)
			item.MissingDeps = missing
			p.remapIssue[is.ID] = existing.ID
			p.recordItem(item)
			continue
		}
		if found && p.overwrite {
			// Overwriting an issue is a no-op for the row body
			// because issues carry comments, reactions, and
			// status history that we don't reset; the planner
			// only re-attaches labels. This keeps the surface
			// small while still letting a re-run refresh the
			// label set.
			if p.doIt && p.overwrite {
				if err := overwriteIssueLabels(ctx, q, p.workspaceID, existing.ID, is.LabelIDs, p.remapLabel); err != nil {
					return err
				}
			}
			item.Action = restoreActionUpdate
			item.TargetID = uuidToString(existing.ID)
			item.MissingDeps = missing
			p.remapIssue[is.ID] = existing.ID
			p.recordItem(item)
			continue
		}

		if len(missing) > 0 && p.doIt {
			item.Action = restoreActionSkip
			item.Reason = "missing dependencies"
			item.MissingDeps = missing
			p.recordItem(item)
			continue
		}

		if p.doIt {
			created, err := createIssue(ctx, q, p.workspaceID, is, p.workspaceOwnerUUID, p.remapProject, p.remapLabel, p.remapMember, p.remapAgent)
			if err != nil {
				item.Action = restoreActionError
				item.Reason = err.Error()
				p.recordItem(item)
				return nil
			}
			if _, err := createComments(ctx, q, p.workspaceID, created.ID, is.Comments, p.remapMember, p.remapAgent); err != nil {
				item.Action = restoreActionError
				item.Reason = "comments: " + err.Error()
				p.recordItem(item)
				return nil
			}
			item.TargetID = uuidToString(created.ID)
			p.remapIssue[is.ID] = created.ID
		}
		item.Action = restoreActionCreate
		item.MissingDeps = missing
		p.recordItem(item)
	}
	return nil
}

func (p *restorePlanner) restoreSquads(ctx context.Context, q *db.Queries) error {
	if !p.sectionEnabled(restoreTypeSquad) {
		return nil
	}
	for _, sq := range p.backup.Squads {
		if !p.itemEnabled(sq.ID) {
			continue
		}
		item := restoreItem{
			Type:       restoreTypeSquad,
			SourceID:   sq.ID,
			Identifier: sq.Name,
		}
		existing, found, err := findSquadByName(ctx, q, p.workspaceID, sq.Name)
		if err != nil {
			return err
		}
		switch {
		case found && !p.overwrite:
			item.Action = restoreActionSkip
			item.Reason = "squad with same name already exists"
			item.TargetID = uuidToString(existing.ID)
			p.remapSquad[sq.ID] = existing.ID
		case found && p.overwrite:
			if p.doIt {
				if err := updateSquad(ctx, q, p.workspaceID, existing, sq, p.workspaceOwnerUUID, p.remapAgent, p.remapMember); err != nil {
					return err
				}
			}
			item.Action = restoreActionUpdate
			item.TargetID = uuidToString(existing.ID)
			p.remapSquad[sq.ID] = existing.ID
		default:
			missing := p.squadMissingDeps(sq)
			if len(missing) > 0 && p.doIt {
				item.Action = restoreActionSkip
				item.Reason = "missing dependencies"
				item.MissingDeps = missing
				p.recordItem(item)
				continue
			}
			if p.doIt {
				created, err := createSquad(ctx, q, p.workspaceID, sq, p.workspaceOwnerUUID, p.remapAgent, p.remapMember)
				if err != nil {
					item.Action = restoreActionError
					item.Reason = err.Error()
					p.recordItem(item)
					return nil
				}
				item.TargetID = uuidToString(created.ID)
				p.remapSquad[sq.ID] = created.ID
			}
			item.Action = restoreActionCreate
		}
		p.recordItem(item)
	}
	return nil
}

func (p *restorePlanner) restoreAutopilots(ctx context.Context, q *db.Queries) error {
	if !p.sectionEnabled(restoreTypeAutopilot) {
		return nil
	}
	for _, ap := range p.backup.Autopilots {
		if !p.itemEnabled(ap.ID) {
			continue
		}
		item := restoreItem{
			Type:       restoreTypeAutopilot,
			SourceID:   ap.ID,
			Identifier: ap.Name,
		}
		missing := p.autopilotMissingDeps(ap)

		existing, found, err := findAutopilotByTitle(ctx, q, p.workspaceID, ap.Name)
		if err != nil {
			return err
		}
		switch {
		case found && !p.overwrite:
			item.Action = restoreActionSkip
			item.Reason = "autopilot with same name already exists"
			item.TargetID = uuidToString(existing.ID)
		case found && p.overwrite:
			if p.doIt {
				if err := updateAutopilot(ctx, q, p.workspaceID, existing, ap, p.remapAgent, p.remapSquad, p.remapProject); err != nil {
					return err
				}
			}
			item.Action = restoreActionUpdate
			item.TargetID = uuidToString(existing.ID)
		default:
			if len(missing) > 0 && p.doIt {
				item.Action = restoreActionSkip
				item.Reason = "missing dependencies"
				item.MissingDeps = missing
				p.recordItem(item)
				continue
			}
			if p.doIt {
				created, err := createAutopilot(ctx, q, p.workspaceID, ap, p.workspaceOwnerUUID, p.remapAgent, p.remapSquad, p.remapProject)
				if err != nil {
					item.Action = restoreActionError
					item.Reason = err.Error()
					p.recordItem(item)
					return nil
				}
				item.TargetID = uuidToString(created.ID)
			}
			item.Action = restoreActionCreate
			item.MissingDeps = missing
		}
		p.recordItem(item)
	}
	return nil
}

// applyWorkspaceSettings overwrites the destination workspace's
// mutable settings with the values from the backup. Guarded by the
// IncludeWorkspace flag at the handler boundary — this only runs when
// the caller explicitly opted in.
//
// The per-field diff is recorded on p.workspaceChanges so the
// response can show the operator exactly what changed. The
// IssuePrefix in particular is worth surfacing because it changes
// the public identifier scheme for every new issue created in the
// workspace after the restore.
func (p *restorePlanner) applyWorkspaceSettings(ctx context.Context, q *db.Queries) error {
	if p.backup.Workspace == nil {
		return nil
	}
	src := p.backup.Workspace

	settings := src.Settings
	if len(settings) == 0 {
		settings = json.RawMessage("{}")
	}
	repos := src.Repos
	if len(repos) == 0 {
		repos = json.RawMessage("[]")
	}
	prefix := src.IssuePrefix
	if prefix == "" {
		prefix = p.workspace.IssuePrefix
	}
	newDescription := nonEmpty(src.Description, p.workspace.Description.String)
	newContext := nonEmpty(src.Context, p.workspace.Context.String)
	newAvatar := nonEmptyText(src.AvatarURL, p.workspace.AvatarUrl)

	p.recordWorkspaceChange("description", p.workspace.Description.String, newDescription)
	p.recordWorkspaceChange("context", p.workspace.Context.String, newContext)
	p.recordWorkspaceChange("issue_prefix", p.workspace.IssuePrefix, prefix)
	if newAvatar.Valid != p.workspace.AvatarUrl.Valid || newAvatar.String != p.workspace.AvatarUrl.String {
		before := ""
		if p.workspace.AvatarUrl.Valid {
			before = p.workspace.AvatarUrl.String
		}
		after := ""
		if newAvatar.Valid {
			after = newAvatar.String
		}
		p.recordWorkspaceChange("avatar_url", before, after)
	}

	if _, err := q.UpdateWorkspace(ctx, db.UpdateWorkspaceParams{
		ID:          p.workspace.ID,
		Name:        pgtype.Text{String: p.workspace.Name, Valid: true},
		Description: pgtype.Text{String: newDescription, Valid: true},
		Context:     pgtype.Text{String: newContext, Valid: true},
		Settings:    settings,
		Repos:       repos,
		IssuePrefix: pgtype.Text{String: prefix, Valid: true},
		AvatarUrl:   newAvatar,
	}); err != nil {
		return fmt.Errorf("update workspace: %w", err)
	}
	p.workspaceApplied = true
	return nil
}

// recordWorkspaceChange appends a diff row IF the field actually
// changed. Equal before/after pairs are dropped so the response
// stays compact.
func (p *restorePlanner) recordWorkspaceChange(field, before, after string) {
	if before == after {
		return
	}
	p.workspaceChanges = append(p.workspaceChanges, restoreWorkspaceChange{
		Field:  field,
		Before: before,
		After:  after,
	})
}

// resolveMembers loads the target workspace's members and populates
// remapMember keyed by the backup's local member id. The actual
// cross-instance identity match is by email, since email is the only
// stable identifier across instances.
func (p *restorePlanner) resolveMembers(ctx context.Context, q *db.Queries) error {
	if len(p.remapMember) > 0 {
		return nil
	}
	rows, err := q.ListMembersWithUser(ctx, p.workspaceID)
	if err != nil {
		return err
	}
	byEmail := map[string]pgtype.UUID{}
	for _, m := range rows {
		if m.UserEmail == "" {
			continue
		}
		byEmail[strings.ToLower(strings.TrimSpace(m.UserEmail))] = m.UserID
	}
	for _, mb := range p.backup.Members {
		email := strings.ToLower(strings.TrimSpace(mb.Email))
		if email == "" {
			continue
		}
		if id, ok := byEmail[email]; ok {
			p.remapMember[mb.ID] = id
		}
	}
	return nil
}

func (p *restorePlanner) recordItem(it restoreItem) {
	p.items = append(p.items, it)
}

// sectionEnabled returns true if the section should run. An empty
// selection map means "all sections enabled".
func (p *restorePlanner) sectionEnabled(t restoreItemType) bool {
	if p.selectedItems == nil {
		return true
	}
	return p.selectedItems[t]
}

func (p *restorePlanner) itemEnabled(sourceID string) bool {
	if p.selectedIDs == nil || len(p.selectedIDs) == 0 {
		return true
	}
	return p.selectedIDs[sourceID]
}

// --- Dependency analysis (used by preview's missing_deps) ---

func (p *restorePlanner) agentMissingDeps(ag backup.BackupAgent) []restoreMissingDep {
	var out []restoreMissingDep
	for _, sid := range ag.SkillIDs {
		if _, ok := p.remapSkill[sid]; ok {
			continue
		}
		out = append(out, restoreMissingDep{Type: restoreTypeSkill, Identifier: sid, Reason: "skill will not be restored"})
	}
	if ag.OwnerID != "" {
		if _, ok := p.remapMember[ag.OwnerID]; !ok {
			out = append(out, restoreMissingDep{Type: restoreTypeAgent, Identifier: ag.OwnerID, Reason: "owner member not found by email"})
		}
	}
	return out
}

func (p *restorePlanner) issueMissingDeps(is backup.BackupIssue) []restoreMissingDep {
	var out []restoreMissingDep
	if is.ProjectID != "" {
		if _, ok := p.remapProject[is.ProjectID]; !ok {
			out = append(out, restoreMissingDep{Type: restoreTypeProject, Identifier: is.ProjectID, Reason: "project not selected for restore"})
		}
	}
	for _, lid := range is.LabelIDs {
		if _, ok := p.remapLabel[lid]; !ok {
			out = append(out, restoreMissingDep{Type: restoreTypeLabel, Identifier: lid, Reason: "label not selected for restore"})
		}
	}
	if is.Assignee.Type == "agent" && is.Assignee.ID != "" {
		if _, ok := p.remapAgent[is.Assignee.ID]; !ok {
			out = append(out, restoreMissingDep{Type: restoreTypeAgent, Identifier: is.Assignee.ID, Reason: "agent not selected for restore"})
		}
	}
	if is.Creator.Type == "agent" && is.Creator.ID != "" {
		if _, ok := p.remapAgent[is.Creator.ID]; !ok {
			out = append(out, restoreMissingDep{Type: restoreTypeAgent, Identifier: is.Creator.ID, Reason: "agent not selected for restore"})
		}
	}
	return out
}

func (p *restorePlanner) squadMissingDeps(sq backup.BackupSquad) []restoreMissingDep {
	var out []restoreMissingDep
	if sq.Leader.Type == "agent" && sq.Leader.ID != "" {
		if _, ok := p.remapAgent[sq.Leader.ID]; !ok {
			out = append(out, restoreMissingDep{Type: restoreTypeAgent, Identifier: sq.Leader.ID, Reason: "agent not selected for restore"})
		}
	}
	for _, m := range sq.Members {
		if m.MemberType != "agent" {
			continue
		}
		if _, ok := p.remapAgent[m.MemberID]; !ok {
			out = append(out, restoreMissingDep{Type: restoreTypeAgent, Identifier: m.MemberID, Reason: "agent not selected for restore"})
		}
	}
	return out
}

func (p *restorePlanner) autopilotMissingDeps(ap backup.BackupAutopilot) []restoreMissingDep {
	var out []restoreMissingDep
	if ap.Assignee.Type == "agent" && ap.Assignee.ID != "" {
		if _, ok := p.remapAgent[ap.Assignee.ID]; !ok {
			out = append(out, restoreMissingDep{Type: restoreTypeAgent, Identifier: ap.Assignee.ID, Reason: "agent not selected for restore"})
		}
	}
	if ap.Assignee.Type == "squad" && ap.Assignee.ID != "" {
		if _, ok := p.remapSquad[ap.Assignee.ID]; !ok {
			out = append(out, restoreMissingDep{Type: restoreTypeSquad, Identifier: ap.Assignee.ID, Reason: "squad not selected for restore"})
		}
	}
	if ap.ProjectID != "" {
		if _, ok := p.remapProject[ap.ProjectID]; !ok {
			out = append(out, restoreMissingDep{Type: restoreTypeProject, Identifier: ap.ProjectID, Reason: "project not selected for restore"})
		}
	}
	return out
}

// --- Selection helpers ---

func parseRestoreItemSelection(items []string) map[restoreItemType]bool {
	if len(items) == 0 {
		return nil
	}
	out := map[restoreItemType]bool{}
	for _, raw := range items {
		switch strings.ToLower(strings.TrimSpace(raw)) {
		case "skills", "skill":
			out[restoreTypeSkill] = true
		case "labels", "label":
			out[restoreTypeLabel] = true
		case "agents", "agent":
			out[restoreTypeAgent] = true
		case "projects", "project":
			out[restoreTypeProject] = true
		case "issues", "issue":
			out[restoreTypeIssue] = true
		case "squads", "squad":
			out[restoreTypeSquad] = true
		case "autopilots", "autopilot":
			out[restoreTypeAutopilot] = true
		}
	}
	return out
}

func stringSet(items []string) map[string]bool {
	if len(items) == 0 {
		return nil
	}
	out := make(map[string]bool, len(items))
	for _, raw := range items {
		trimmed := strings.TrimSpace(raw)
		if trimmed != "" {
			out[trimmed] = true
		}
	}
	return out
}

// nonEmpty returns s if non-empty, otherwise fallback.
func nonEmpty(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}

// nonEmptyText returns the source text if non-empty, otherwise the
// existing pgtype.Text. Used to keep fields like AvatarUrl stable when
// the backup did not include them.
func nonEmptyText(s string, fallback pgtype.Text) pgtype.Text {
	if s == "" {
		return fallback
	}
	return pgtype.Text{String: s, Valid: true}
}
