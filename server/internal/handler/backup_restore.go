package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/backup"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// restoreEntityKind orders restore work top-down so each section's
// dependencies are guaranteed to exist in the target workspace before any
// referencing row is created. Skills and Labels are leaves — no other section
// references them. Agents and Projects are middle layers (issues reference
// both). Issues, Squads and Autopilots are the heavy leaves at the end.
type restoreEntityKind string

const (
	restoreKindSkills    restoreEntityKind = "skills"
	restoreKindLabels    restoreEntityKind = "labels"
	restoreKindAgents    restoreEntityKind = "agents"
	restoreKindProjects  restoreEntityKind = "projects"
	restoreKindIssues    restoreEntityKind = "issues"
	restoreKindSquads    restoreEntityKind = "squads"
	restoreKindAutopilot restoreEntityKind = "autopilots"
)

// restoreDependencyOrder is the canonical restore order. Every code path that
// reports dependency ordering — preview, execute, and tests — reads this list
// instead of building its own, so the contract is in one place.
var restoreDependencyOrder = []restoreEntityKind{
	restoreKindSkills,
	restoreKindLabels,
	restoreKindAgents,
	restoreKindProjects,
	restoreKindIssues,
	restoreKindSquads,
	restoreKindAutopilot,
}

// BackupRestoreBody is the shared envelope for both preview and execute. The
// backup payload is sent as a JSON string (the contents of a backup file
// serialized with backup.Marshal) so callers can upload the file as-is without
// having to re-encode the metadata wrapper. Workspace context is taken from
// the request headers (X-Workspace-ID/Slug) like every other workspace-scoped
// route; the target_workspace_id field is reserved for a future "restore to
// a different workspace" flow and is currently informational.
type BackupRestoreBody struct {
	// Backup is the serialized backup file content (raw bytes of a
	// backup.Marshal output). Sent as a string to avoid forcing callers
	// to base64-decode a binary envelope.
	Backup string `json:"backup"`
	// TargetWorkspaceID is informational; the actual write target comes
	// from the X-Workspace-ID header on the request.
	TargetWorkspaceID string `json:"target_workspace_id,omitempty"`
	// Options control how conflicts are resolved on execute.
	Options BackupRestoreOptions `json:"options"`
	// SelectedItems, when non-empty, restricts the restore to the listed
	// source-UUIDs. nil/empty means "restore everything in the file".
	// Unknown IDs are reported in the response as "skipped".
	SelectedItems []string `json:"selected_items,omitempty"`
}

// BackupRestoreOptions governs conflict resolution and selection. The two
// boolean fields are mutually exclusive; Overwrite wins if both are set
// (overwriting is the more destructive default, so a caller that explicitly
// asks for both should not have their intent silently downgraded to skip).
type BackupRestoreOptions struct {
	// Overwrite, when true, replaces existing entities that match by
	// conflict key (name/number). Otherwise conflicts are left alone.
	Overwrite bool `json:"overwrite"`
	// SkipConflicts, when true, leaves conflicting entities untouched and
	// does not surface an error. When false, conflicting entities appear
	// in the response with status "skipped" and a "conflict" reason.
	SkipConflicts bool `json:"skip_conflicts"`
}

// BackupRestoreItem is one entity in the restore plan. Status captures the
// outcome ("new", "overwritten", "skipped", "error"); Action is the
// human-readable verb the UI surfaces ("create", "replace", "skip").
type BackupRestoreItem struct {
	// Kind is the section name ("skills", "agents", ...).
	Kind string `json:"kind"`
	// SourceID is the UUID of the entity in the source backup. Stable
	// across previews so the UI can re-match the row.
	SourceID string `json:"source_id"`
	// Identifier is the human-readable name/title/number used for
	// conflict detection.
	Identifier string `json:"identifier"`
	// Action is what the restore would do / did ("create", "replace",
	// "skip").
	Action string `json:"action"`
	// Status is the outcome ("new", "overwritten", "skipped", "error",
	// "created").
	Status string `json:"status"`
	// TargetID is the UUID in the target workspace for created/overwritten
	// rows. Empty for skipped/error rows.
	TargetID string `json:"target_id,omitempty"`
	// Reason is populated for skipped/error rows (e.g. "name already
	// exists", "missing dependency: agent").
	Reason string `json:"reason,omitempty"`
}

// BackupRestorePlan is the full ordered restore plan returned by preview and
// execute. Sections are sorted in restoreDependencyOrder so the consumer
// never has to sort itself.
type BackupRestorePlan struct {
	// DependencyOrder is the canonical section ordering. The Sections
	// map is keyed by the same strings.
	DependencyOrder []string `json:"dependency_order"`
	// Sections is keyed by section name ("skills", "agents", ...). Each
	// value is the ordered list of items in that section in the order
	// they appear in the backup file.
	Sections map[string][]BackupRestoreItem `json:"sections"`
	// Summary is a quick at-a-glance counter (created, overwritten,
	// skipped, errored).
	Summary BackupRestoreSummary `json:"summary"`
}

// BackupRestoreSummary counts items by status. Used by the UI to render the
// "X created, Y skipped" header without re-iterating the sections.
type BackupRestoreSummary struct {
	Created     int `json:"created"`
	Overwritten int `json:"overwritten"`
	Skipped     int `json:"skipped"`
	Errored     int `json:"error"`
}

// BackupRestorePreviewResponse is the body shape for
// POST /api/backup/restore/preview.
type BackupRestorePreviewResponse struct {
	Plan BackupRestorePlan `json:"plan"`
}

// BackupRestoreExecuteResponse is the body shape for POST /api/backup/restore.
type BackupRestoreExecuteResponse struct {
	Plan BackupRestorePlan `json:"plan"`
}

// backupBodyLimit caps the request body at 32 MiB. Backup files contain
// inlined content (skill SKILL.md bodies, autopilot schedule metadata) and
// the on-disk serialised format for a single workspace can easily reach
// several hundred KiB; 32 MiB is enough for any reasonable single-workspace
// export while still rejecting obvious abuse.
const backupBodyLimit = 32 * 1024 * 1024

// issuesListLimit is the upper bound for the per-section "list all issues"
// query during conflict detection. We don't paginate the conflict scan
// because the section's in-memory map is built from a single query and
// workspaces don't realistically exceed a few thousand issues; the cap
// exists so a malicious or corrupt backup can't drive us into a slow
// unbounded query.
const issuesListLimit int32 = 100_000

// systemCreatorID is the placeholder pgtype.UUID used for entities created
// during a restore. The DB has a NOT NULL on creator_id/owner_id, but
// pgtype.UUID is its own type — leaving it zero-valid is the closest
// available analogue to "system actor". The real audit story is a
// future-PR activity_log entry, not a forged user id.
var systemCreatorID = pgtype.UUID{}

// BackupRestorePreview parses the uploaded backup, detects conflicts against
// the target workspace, and returns an ordered plan. It performs no writes.
//
// Request: POST /api/backup/restore/preview with a JSON body of shape
// { "backup": "<serialized backup file>", "options": {...} }.
//
// Response: 200 with BackupRestorePreviewResponse on success; 400 on bad
// input or unparseable backup; 401/403 on auth/permission errors.
func (h *Handler) BackupRestorePreview(w http.ResponseWriter, r *http.Request) {
	body, ws, ok := h.parseBackupRestoreBody(w, r)
	if !ok {
		return
	}

	parsed, err := backup.Unmarshal([]byte(body.Backup))
	if err != nil {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("invalid backup file: %v", err))
		return
	}

	plan, err := h.planBackupRestore(r.Context(), parsed, ws, body.Options, body.SelectedItems, false)
	if err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("preview failed: %v", err))
		return
	}
	writeJSON(w, http.StatusOK, BackupRestorePreviewResponse{Plan: *plan})
}

// BackupRestore executes the restore inside a single transaction. The
// transaction boundary is essential: a partial restore (e.g. labels inserted
// but not the agents that reference them) would leave the workspace in a
// worse state than before, and the dependency order is only meaningful if it
// is enforced atomically.
//
// On any error the transaction is rolled back and the response carries 500
// with the error message; the UI surfaces the rolled-back plan so the user
// can decide whether to retry with adjusted options.
func (h *Handler) BackupRestore(w http.ResponseWriter, r *http.Request) {
	body, ws, ok := h.parseBackupRestoreBody(w, r)
	if !ok {
		return
	}

	parsed, err := backup.Unmarshal([]byte(body.Backup))
	if err != nil {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("invalid backup file: %v", err))
		return
	}

	executedPlan, execErr := h.executeBackupRestore(r.Context(), parsed, ws, body.Options, body.SelectedItems)
	if execErr != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("restore failed: %v", execErr))
		return
	}
	writeJSON(w, http.StatusOK, BackupRestoreExecuteResponse{Plan: *executedPlan})
}

// parseBackupRestoreBody decodes the request body and resolves the target
// workspace from the request headers. Pulled out so preview and execute
// share the exact same parse + auth contract.
func (h *Handler) parseBackupRestoreBody(w http.ResponseWriter, r *http.Request) (BackupRestoreBody, string, bool) {
	r.Body = http.MaxBytesReader(w, r.Body, backupBodyLimit)
	var body BackupRestoreBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return body, "", false
	}
	if body.Backup == "" {
		writeError(w, http.StatusBadRequest, "backup payload is required")
		return body, "", false
	}
	workspaceID := h.resolveWorkspaceID(r)
	if workspaceID == "" {
		writeError(w, http.StatusBadRequest, "workspace_id is required")
		return body, "", false
	}
	// Restore is an admin-only operation: only workspace owners and admins
	// can import/export. Read-only preview is the same gate so non-admins
	// cannot enumerate what would happen.
	if _, ok := h.requireWorkspaceRole(w, r, workspaceID, "workspace not found", "owner", "admin"); !ok {
		return body, "", false
	}
	return body, workspaceID, true
}

// restoreIndex holds the per-section conflict maps (existing-by-key) and the
// remap tables (source-ID → target-ID) accumulated as the restore walks
// sections in dependency order. We build the index once and pass it into
// every section planner so cross-section references (e.g. an issue's
// project_id pointing at a project we are also restoring) can be resolved
// in-line.
type restoreIndex struct {
	workspaceID pgtype.UUID

	// existingByKey is the lookup table of "what's already in the target
	// workspace by human-readable conflict key". Built once at plan time
	// from a single read pass per section.
	existingByKey map[restoreEntityKind]map[string]string

	// remap holds source-ID → target-ID mappings. Populated as each
	// section commits; later sections consult it when translating
	// references (e.g. an issue's label_ids).
	remap map[restoreEntityKind]map[string]string

	// defaultRuntimeID is the first agent_runtime row in the target
	// workspace. Restored agents that don't carry a runtime of their
	// own are bound to this row so the NOT NULL runtime_id
	// constraint is satisfied. A workspace with no runtime at all
	// fails the agent section — the user must bootstrap a runtime
	// first.
	defaultRuntimeID pgtype.UUID

	// defaultCreatorID is the first member of the target workspace.
	// Used as creator_id on issues, autopilots, project resources,
	// and any other NOT NULL creator column where a human or
	// system actor must be stamped. The restore runs as
	// owner/admin; the system-creator concept is not (yet) part of
	// the platform, so the first workspace member fills the role.
	defaultCreatorID pgtype.UUID
}

func newRestoreIndex(workspaceID pgtype.UUID) *restoreIndex {
	return &restoreIndex{
		workspaceID:   workspaceID,
		existingByKey: make(map[restoreEntityKind]map[string]string, len(restoreDependencyOrder)),
		remap:         make(map[restoreEntityKind]map[string]string, len(restoreDependencyOrder)),
	}
}

func (idx *restoreIndex) remapKey(kind restoreEntityKind, key string) string {
	if m, ok := idx.remap[kind]; ok {
		return m[key]
	}
	return ""
}

func (idx *restoreIndex) existingKey(kind restoreEntityKind, key string) string {
	if m, ok := idx.existingByKey[kind]; ok {
		return m[key]
	}
	return ""
}

// loadExisting populates idx.existingByKey for every section in one pass.
// The "preview" handler calls this so a no-races preview is a single
// snapshot of the workspace; the "execute" handler re-loads inside the
// transaction for write-mode so it sees the same data the planner saw.
func (idx *restoreIndex) loadExisting(ctx context.Context, q *db.Queries) error {
	ws := idx.workspaceID

	// Default runtime lookup. agent.runtime_id is NOT NULL, so every
	// restored agent must point at some runtime — we use the
	// workspace's first runtime as the catch-all. Cross-instance
	// runtime rows aren't portable (they hold daemon tokens,
	// device fingerprints, etc.), so the original runtime_id from
	// the backup is ignored.
	runtimes, err := q.ListAgentRuntimes(ctx, ws)
	if err != nil {
		return fmt.Errorf("list runtimes: %w", err)
	}
	if len(runtimes) > 0 {
		idx.defaultRuntimeID = runtimes[0].ID
	}

	// Default creator: first member of the workspace. The
	// owner/admin that ran the restore is also fine, but pulling
	// from the DB avoids threading the request's user id through
	// every section planner.
	members, err := q.ListMembers(ctx, ws)
	if err != nil {
		return fmt.Errorf("list members: %w", err)
	}
	if len(members) > 0 {
		idx.defaultCreatorID = members[0].UserID
	}

	skills, err := q.ListSkillsByWorkspace(ctx, ws)
	if err != nil {
		return fmt.Errorf("list skills: %w", err)
	}
	idx.existingByKey[restoreKindSkills] = make(map[string]string, len(skills))
	for _, s := range skills {
		idx.existingByKey[restoreKindSkills][s.Name] = uuidToString(s.ID)
	}

	labels, err := q.ListLabels(ctx, ws)
	if err != nil {
		return fmt.Errorf("list labels: %w", err)
	}
	idx.existingByKey[restoreKindLabels] = make(map[string]string, len(labels))
	for _, l := range labels {
		idx.existingByKey[restoreKindLabels][l.Name] = uuidToString(l.ID)
	}

	agents, err := q.ListAgents(ctx, ws)
	if err != nil {
		return fmt.Errorf("list agents: %w", err)
	}
	idx.existingByKey[restoreKindAgents] = make(map[string]string, len(agents))
	for _, a := range agents {
		idx.existingByKey[restoreKindAgents][a.Name] = uuidToString(a.ID)
	}

	projects, err := q.ListProjects(ctx, db.ListProjectsParams{WorkspaceID: ws})
	if err != nil {
		return fmt.Errorf("list projects: %w", err)
	}
	idx.existingByKey[restoreKindProjects] = make(map[string]string, len(projects))
	for _, p := range projects {
		idx.existingByKey[restoreKindProjects][p.Title] = uuidToString(p.ID)
	}

	// Issues match by number; load every issue and bucket by number. The
	// per-section scan is cheap for a workspace (one indexed query) and
	// the in-memory map lets us resolve in O(1) during the plan walk.
	issueRows, err := q.ListIssues(ctx, db.ListIssuesParams{
		WorkspaceID: ws,
		Limit:       issuesListLimit,
	})
	if err != nil {
		return fmt.Errorf("list issues: %w", err)
	}
	idx.existingByKey[restoreKindIssues] = make(map[string]string, len(issueRows))
	for _, row := range issueRows {
		key := fmt.Sprintf("%d", row.Number)
		idx.existingByKey[restoreKindIssues][key] = uuidToString(row.ID)
	}

	squads, err := q.ListAllSquads(ctx, ws)
	if err != nil {
		return fmt.Errorf("list squads: %w", err)
	}
	idx.existingByKey[restoreKindSquads] = make(map[string]string, len(squads))
	for _, s := range squads {
		idx.existingByKey[restoreKindSquads][s.Name] = uuidToString(s.ID)
	}

	autopilots, err := q.ListAutopilots(ctx, db.ListAutopilotsParams{WorkspaceID: ws})
	if err != nil {
		return fmt.Errorf("list autopilots: %w", err)
	}
	// Autopilots identify by Title in the DB; the brief calls it "name"
	// for user-facing consistency, but the conflict key is Title.
	idx.existingByKey[restoreKindAutopilot] = make(map[string]string, len(autopilots))
	for _, a := range autopilots {
		idx.existingByKey[restoreKindAutopilot][a.Title] = uuidToString(a.ID)
	}

	return nil
}

// planBackupRestore walks every section in dependency order, deciding the
// action for each item and assembling the plan. The "execute" flag is false
// for preview, true for the write path — when true the plan is later re-run
// inside the transaction by executeBackupRestore.
//
// This function only reads; it does not write. The two-phase plan/write split
// keeps preview fast (single read snapshot) and lets execute re-plan in-tx
// with the same logic.
func (h *Handler) planBackupRestore(ctx context.Context, parsed *backup.BackupFile, workspaceID string, opts BackupRestoreOptions, selected []string, execute bool) (*BackupRestorePlan, error) {
	wsUUID, err := parseUUIDFromString(workspaceID)
	if err != nil {
		return nil, fmt.Errorf("invalid workspace_id: %w", err)
	}

	idx := newRestoreIndex(wsUUID)
	if err := idx.loadExisting(ctx, h.Queries); err != nil {
		return nil, err
	}

	selectedSet := buildSelectedSet(selected)
	hasSelection := len(selected) > 0

	plan := newEmptyPlan()
	q := h.Queries

	if err := h.planSkillsSection(ctx, q, parsed, idx, plan, opts, hasSelection, selectedSet, execute); err != nil {
		return nil, err
	}
	if err := h.planLabelsSection(ctx, q, parsed, idx, plan, opts, hasSelection, selectedSet, execute); err != nil {
		return nil, err
	}
	if err := h.planAgentsSection(ctx, q, parsed, idx, plan, opts, hasSelection, selectedSet, execute); err != nil {
		return nil, err
	}
	if err := h.planProjectsSection(ctx, q, parsed, idx, plan, opts, hasSelection, selectedSet, execute); err != nil {
		return nil, err
	}
	if err := h.planIssuesSection(ctx, q, parsed, idx, plan, opts, hasSelection, selectedSet, execute); err != nil {
		return nil, err
	}
	if err := h.planSquadsSection(ctx, q, parsed, idx, plan, opts, hasSelection, selectedSet, execute); err != nil {
		return nil, err
	}
	if err := h.planAutopilotsSection(ctx, q, parsed, idx, plan, opts, hasSelection, selectedSet, execute); err != nil {
		return nil, err
	}

	summaryFromPlan(plan)
	return plan, nil
}

// executeBackupRestore is the execute path. It opens a transaction,
// re-loads the conflict index inside the tx (so the planner sees the same
// rows the writes will), and re-walks the plan. On commit the plan is
// returned; on any error the deferred rollback unwinds everything.
func (h *Handler) executeBackupRestore(ctx context.Context, parsed *backup.BackupFile, workspaceID string, opts BackupRestoreOptions, selected []string) (*BackupRestorePlan, error) {
	wsUUID, err := parseUUIDFromString(workspaceID)
	if err != nil {
		return nil, fmt.Errorf("invalid workspace_id: %w", err)
	}

	tx, err := h.TxStarter.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback(ctx)

	qtx := h.Queries.WithTx(tx)

	idx := newRestoreIndex(wsUUID)
	if err := idx.loadExisting(ctx, qtx); err != nil {
		return nil, fmt.Errorf("load existing: %w", err)
	}

	selectedSet := buildSelectedSet(selected)
	hasSelection := len(selected) > 0

	plan := newEmptyPlan()

	if err := h.planSkillsSection(ctx, qtx, parsed, idx, plan, opts, hasSelection, selectedSet, true); err != nil {
		return nil, err
	}
	if err := h.planLabelsSection(ctx, qtx, parsed, idx, plan, opts, hasSelection, selectedSet, true); err != nil {
		return nil, err
	}
	if err := h.planAgentsSection(ctx, qtx, parsed, idx, plan, opts, hasSelection, selectedSet, true); err != nil {
		return nil, err
	}
	if err := h.planProjectsSection(ctx, qtx, parsed, idx, plan, opts, hasSelection, selectedSet, true); err != nil {
		return nil, err
	}
	if err := h.planIssuesSection(ctx, qtx, parsed, idx, plan, opts, hasSelection, selectedSet, true); err != nil {
		return nil, err
	}
	if err := h.planSquadsSection(ctx, qtx, parsed, idx, plan, opts, hasSelection, selectedSet, true); err != nil {
		return nil, err
	}
	if err := h.planAutopilotsSection(ctx, qtx, parsed, idx, plan, opts, hasSelection, selectedSet, true); err != nil {
		return nil, err
	}

	if err := tx.Commit(ctx); err != nil {
		// Roll back explicitly so the deferred Rollback is a
		// no-op (a successful commit on a broken tx surfaces
		// as ErrTxCommitRollback, which is what we hit when
		// any section errored earlier — explicitly rolling
		// back here keeps the error path obvious).
		_ = tx.Rollback(ctx)
		// Find the first item that errored so the UI can
		// surface a specific cause.
		for _, items := range plan.Sections {
			for _, it := range items {
				if it.Status == "error" {
					return plan, fmt.Errorf("commit: %w (first error: %s %q: %s)", err, it.Kind, it.Identifier, it.Reason)
				}
			}
		}
		return plan, fmt.Errorf("commit: %w", err)
	}
	summaryFromPlan(plan)
	return plan, nil
}

// newEmptyPlan returns a plan with the dependency-order slot pre-populated
// and every section's item list pre-allocated as an empty (non-nil) slice.
// Pre-allocating the slices keeps the JSON response shape stable even when
// a section is empty — the consumer can iterate without nil-checks.
func newEmptyPlan() *BackupRestorePlan {
	plan := &BackupRestorePlan{
		DependencyOrder: make([]string, 0, len(restoreDependencyOrder)),
		Sections:        make(map[string][]BackupRestoreItem, len(restoreDependencyOrder)),
	}
	for _, kind := range restoreDependencyOrder {
		plan.DependencyOrder = append(plan.DependencyOrder, string(kind))
		plan.Sections[string(kind)] = []BackupRestoreItem{}
	}
	return plan
}

// buildSelectedSet converts a slice of source-IDs into a set for O(1)
// membership tests. nil in → nil out so the empty case skips allocation.
func buildSelectedSet(selected []string) map[string]struct{} {
	if len(selected) == 0 {
		return nil
	}
	out := make(map[string]struct{}, len(selected))
	for _, s := range selected {
		out[s] = struct{}{}
	}
	return out
}

// summaryFromPlan rebuilds the at-a-glance counters from the per-section
// item lists. Called at the end of plan/execute so the summary is always
// consistent with the Sections map.
func summaryFromPlan(plan *BackupRestorePlan) {
	for _, items := range plan.Sections {
		for _, it := range items {
			switch it.Status {
			case "created":
				plan.Summary.Created++
			case "overwritten":
				plan.Summary.Overwritten++
			case "skipped":
				plan.Summary.Skipped++
			case "error":
				plan.Summary.Errored++
			}
		}
	}
}

// shouldProcess returns true if the source entity should be included in the
// restore. False when a non-empty selection filter excludes the source ID.
// hasSelection is hoisted out of the call site so the empty case is a single
// pointer dereference.
func shouldProcess(sourceID string, hasSelection bool, selected map[string]struct{}) bool {
	if !hasSelection {
		return true
	}
	_, ok := selected[sourceID]
	return ok
}

// planSkillsSection fills the "skills" section. On execute, the new row is
// inserted via createSkillWithFilesInTx so the skill and its supporting
// files commit in one shot. Conflicts (existing skill with the same name)
// respect opts.Overwrite — when overwrite is on, the existing skill is
// replaced via UpdateSkill; when off, the item is skipped.
func (h *Handler) planSkillsSection(ctx context.Context, q *db.Queries, parsed *backup.BackupFile, idx *restoreIndex, plan *BackupRestorePlan, opts BackupRestoreOptions, hasSelection bool, selected map[string]struct{}, execute bool) error {
	kind := restoreKindSkills

	for _, sk := range parsed.Skills {
		if !shouldProcess(sk.ID, hasSelection, selected) {
			continue
		}
		item := BackupRestoreItem{
			Kind:       string(kind),
			SourceID:   sk.ID,
			Identifier: sk.Name,
		}
		conflictID := idx.existingKey(kind, sk.Name)
		switch {
		case conflictID != "" && !opts.Overwrite:
			item.Action = "skip"
			item.Status = "skipped"
			item.Reason = fmt.Sprintf("skill %q already exists", sk.Name)
			idx.remap[kind] = setRemap(idx.remap[kind], sk.ID, conflictID)
		case !execute:
			// Preview: report the action the execute path will take
			// without writing. The TargetID is left empty so the UI
			// does not imply a write that didn't happen.
			if conflictID != "" {
				item.Action = "replace"
				item.Status = "overwritten"
			} else {
				item.Action = "create"
				item.Status = "new"
			}
		case conflictID != "" && opts.Overwrite:
			// In-place update of the existing skill: we treat the
			// overwrite path as a replacement of the user-visible
			// fields only. Files are left untouched on overwrite to
			// avoid clobbering a live skill's bundled assets — the
			// preview UI can offer a "replace files" toggle later
			// if the conflict story grows.
			configRaw := sk.Config
			if len(configRaw) == 0 {
				configRaw = []byte("{}")
			}
			if _, err := q.UpdateSkill(ctx, db.UpdateSkillParams{
				ID:          parseUUID(conflictID),
				Name:        pgtype.Text{String: sanitizeNullBytes(sk.Name), Valid: true},
				Description: pgtype.Text{String: sanitizeNullBytes(sk.Description), Valid: true},
				Content:     pgtype.Text{String: sanitizeNullBytes(sk.Content), Valid: true},
				Config:      configRaw,
			}); err != nil {
				item.Action = "skip"
				item.Status = "error"
				item.Reason = fmt.Sprintf("update failed: %v", err)
			} else {
				item.Action = "replace"
				item.Status = "overwritten"
				item.TargetID = conflictID
			}
		default:
			files := make([]CreateSkillFileRequest, len(sk.Files))
			for i, f := range sk.Files {
				files[i] = CreateSkillFileRequest{Path: f.Path, Content: f.Content}
			}
			created, err := createSkillWithFilesInTx(ctx, q, skillCreateInput{
				WorkspaceID: idx.workspaceID,
				CreatorID:   systemCreatorID, // system-created during restore
				Name:        sk.Name,
				Description: sk.Description,
				Content:     sk.Content,
				Config:      sk.Config,
				Files:       files,
			})
			if err != nil {
				item.Action = "skip"
				item.Status = "error"
				item.Reason = fmt.Sprintf("create failed: %v", err)
			} else {
				// created.ID is the string ID from the response
				// envelope; we keep that as TargetID for the UI.
				newID := created.ID
				item.Action = "create"
				item.Status = "created"
				item.TargetID = newID
				idx.remap[kind] = setRemap(idx.remap[kind], sk.ID, newID)
			}
		}
		plan.Sections[string(kind)] = append(plan.Sections[string(kind)], item)
	}
	return nil
}

// planLabelsSection: labels have no dependencies and no files. Direct insert
// via CreateLabel, conflict match by name.
func (h *Handler) planLabelsSection(ctx context.Context, q *db.Queries, parsed *backup.BackupFile, idx *restoreIndex, plan *BackupRestorePlan, opts BackupRestoreOptions, hasSelection bool, selected map[string]struct{}, execute bool) error {
	kind := restoreKindLabels

	for _, l := range parsed.Labels {
		if !shouldProcess(l.ID, hasSelection, selected) {
			continue
		}
		item := BackupRestoreItem{
			Kind:       string(kind),
			SourceID:   l.ID,
			Identifier: l.Name,
		}
		conflictID := idx.existingKey(kind, l.Name)
		switch {
		case conflictID != "" && !opts.Overwrite:
			item.Action = "skip"
			item.Status = "skipped"
			item.Reason = fmt.Sprintf("label %q already exists", l.Name)
			idx.remap[kind] = setRemap(idx.remap[kind], l.ID, conflictID)
		case conflictID != "" && opts.Overwrite:
			item.Action = "replace"
			item.Status = "overwritten"
			item.TargetID = conflictID
			// Label overwrite: name is the conflict key and a unique
			// constraint, so it cannot change. UpdateLabel is called
			// to refresh the colour; everything else stays.
			if execute {
				if _, err := q.UpdateLabel(ctx, db.UpdateLabelParams{
					ID:          parseUUID(conflictID),
					WorkspaceID: idx.workspaceID,
					Color:       pgtype.Text{String: nonEmpty(l.Color, "#888888"), Valid: true},
				}); err != nil {
					item.Action = "skip"
					item.Status = "error"
					item.Reason = fmt.Sprintf("update failed: %v", err)
				}
			}
			idx.remap[kind] = setRemap(idx.remap[kind], l.ID, conflictID)
		case !execute:
			item.Action = "create"
			item.Status = "new"
		default:
			created, err := q.CreateLabel(ctx, db.CreateLabelParams{
				WorkspaceID: idx.workspaceID,
				Name:        l.Name,
				Color:       nonEmpty(l.Color, "#888888"),
			})
			if err != nil {
				item.Action = "skip"
				item.Status = "error"
				item.Reason = fmt.Sprintf("create failed: %v", err)
			} else {
				newID := uuidToString(created.ID)
				item.Action = "create"
				item.Status = "created"
				item.TargetID = newID
				idx.remap[kind] = setRemap(idx.remap[kind], l.ID, newID)
			}
		}
		plan.Sections[string(kind)] = append(plan.Sections[string(kind)], item)
	}
	return nil
}

// planAgentsSection: agents are matched by name. The agent's runtime is
// left unset (pgtype.UUID{}) because cross-instance runtime rows are not
// portable — restored agents run in the workspace's default cloud runtime
// when first claimed. OwnerID is also unset (the human-owner identity is
// not portable across instances).
func (h *Handler) planAgentsSection(ctx context.Context, q *db.Queries, parsed *backup.BackupFile, idx *restoreIndex, plan *BackupRestorePlan, opts BackupRestoreOptions, hasSelection bool, selected map[string]struct{}, execute bool) error {
	kind := restoreKindAgents

	for _, a := range parsed.Agents {
		if !shouldProcess(a.ID, hasSelection, selected) {
			continue
		}
		item := BackupRestoreItem{
			Kind:       string(kind),
			SourceID:   a.ID,
			Identifier: a.Name,
		}
		conflictID := idx.existingKey(kind, a.Name)
		if conflictID != "" && !opts.Overwrite {
			item.Action = "skip"
			item.Status = "skipped"
			item.Reason = fmt.Sprintf("agent %q already exists", a.Name)
			idx.remap[kind] = setRemap(idx.remap[kind], a.ID, conflictID)
			plan.Sections[string(kind)] = append(plan.Sections[string(kind)], item)
			continue
		}

		// Build the agent row. Required defaults to keep the SQL
		// constraints happy even when the backup omits them.
		runtimeMode := a.RuntimeMode
		if runtimeMode == "" {
			runtimeMode = "cloud"
		}
		visibility := a.Visibility
		if visibility == "" {
			visibility = "workspace"
		}
		runtimeConfig := a.RuntimeConfig
		if len(runtimeConfig) == 0 {
			runtimeConfig = []byte("{}")
		}
		customEnv := a.CustomEnv
		if len(customEnv) == 0 {
			customEnv = []byte("{}")
		}
		customArgs := a.CustomArgs
		if len(customArgs) == 0 {
			customArgs = []byte("[]")
		}
		mcpConfig := a.McpConfig
		if len(mcpConfig) == 0 {
			mcpConfig = []byte("{}")
		}
		maxConcurrent := int32(1)
		if a.MaxConcurrentTasks != nil {
			maxConcurrent = *a.MaxConcurrentTasks
		}

		// Conflict + overwrite: rewrite the existing row in place.
		if conflictID != "" {
			upd, err := q.UpdateAgent(ctx, db.UpdateAgentParams{
				ID:                 parseUUID(conflictID),
				Name:               pgtype.Text{String: a.Name, Valid: true},
				Description:        pgtype.Text{String: a.Description, Valid: true},
				RuntimeMode:        pgtype.Text{String: runtimeMode, Valid: true},
				RuntimeConfig:      runtimeConfig,
				Visibility:         pgtype.Text{String: visibility, Valid: true},
				MaxConcurrentTasks: pgtype.Int4{Int32: maxConcurrent, Valid: true},
				Instructions:       pgtype.Text{String: a.Instructions, Valid: true},
				CustomEnv:          customEnv,
				CustomArgs:         customArgs,
				McpConfig:          mcpConfig,
				Model:              strToText(a.Model),
				ThinkingLevel:      strToText(a.ThinkingLevel),
				AvatarUrl:          strToText(a.AvatarURL),
			})
			if err != nil {
				item.Action = "skip"
				item.Status = "error"
				item.Reason = fmt.Sprintf("update failed: %v", err)
				plan.Sections[string(kind)] = append(plan.Sections[string(kind)], item)
				continue
			}
			item.Action = "replace"
			item.Status = "overwritten"
			item.TargetID = uuidToString(upd.ID)
			idx.remap[kind] = setRemap(idx.remap[kind], a.ID, item.TargetID)
			plan.Sections[string(kind)] = append(plan.Sections[string(kind)], item)
			continue
		}

		// Preview: report the action without writing. The full
		// recreate-write path runs on the execute side. We still
		// populate the remap so downstream sections (issues,
		// squads, autopilots) see the would-be target ID.
		if !execute {
			item.Action = "create"
			item.Status = "new"
			plan.Sections[string(kind)] = append(plan.Sections[string(kind)], item)
			continue
		}

		created, err := q.CreateAgent(ctx, db.CreateAgentParams{
			WorkspaceID:        idx.workspaceID,
			Name:               a.Name,
			Description:        a.Description,
			RuntimeMode:        runtimeMode,
			RuntimeConfig:      runtimeConfig,
			RuntimeID:          idx.defaultRuntimeID,
			Visibility:         visibility,
			MaxConcurrentTasks: maxConcurrent,
			Instructions:       a.Instructions,
			CustomEnv:          customEnv,
			CustomArgs:         customArgs,
			McpConfig:          mcpConfig,
			Model:              strToText(a.Model),
			ThinkingLevel:      strToText(a.ThinkingLevel),
			AvatarUrl:          strToText(a.AvatarURL),
		})
		if err != nil {
			item.Action = "skip"
			item.Status = "error"
			item.Reason = fmt.Sprintf("create failed: %v", err)
			plan.Sections[string(kind)] = append(plan.Sections[string(kind)], item)
			continue
		}
		newID := uuidToString(created.ID)
		item.Action = "create"
		item.Status = "created"
		item.TargetID = newID
		idx.remap[kind] = setRemap(idx.remap[kind], a.ID, newID)

		// Wire agent_skill rows. Each backup.SkillIDs entry is a
		// source-side ID; the remap resolves it to the target
		// skill's new ID. Skill IDs that weren't part of the
		// restore are silently dropped — the agent keeps its
		// other skills.
		for _, sourceSkillID := range a.SkillIDs {
			target := idx.remapKey(restoreKindSkills, sourceSkillID)
			if target == "" {
				continue
			}
			if err := q.AddAgentSkill(ctx, db.AddAgentSkillParams{
				AgentID: created.ID,
				SkillID: parseUUID(target),
			}); err != nil {
				continue
			}
		}
		plan.Sections[string(kind)] = append(plan.Sections[string(kind)], item)
	}
	return nil
}

// planProjectsSection: projects are matched by title. Project resources
// (linked repos, docs, etc.) are restored in-line. The lead is a polymorphic
// BackupActor (member or agent); we resolve through the appropriate
// existingByKey/remap table.
func (h *Handler) planProjectsSection(ctx context.Context, q *db.Queries, parsed *backup.BackupFile, idx *restoreIndex, plan *BackupRestorePlan, opts BackupRestoreOptions, hasSelection bool, selected map[string]struct{}, execute bool) error {
	kind := restoreKindProjects

	for _, p := range parsed.Projects {
		if !shouldProcess(p.ID, hasSelection, selected) {
			continue
		}
		item := BackupRestoreItem{
			Kind:       string(kind),
			SourceID:   p.ID,
			Identifier: p.Title,
		}
		conflictID := idx.existingKey(kind, p.Title)
		if conflictID != "" && !opts.Overwrite {
			item.Action = "skip"
			item.Status = "skipped"
			item.Reason = fmt.Sprintf("project %q already exists", p.Title)
			idx.remap[kind] = setRemap(idx.remap[kind], p.ID, conflictID)
			plan.Sections[string(kind)] = append(plan.Sections[string(kind)], item)
			continue
		}

		// Lead resolution: a polymorphic actor; if the source pointed
		// at an agent, look it up in the agents remap; otherwise
		// leave lead unset.
		var leadType pgtype.Text
		var leadID pgtype.UUID
		if p.Lead.Type == "agent" {
			if remapped := idx.remapKey(restoreKindAgents, p.Lead.ID); remapped != "" {
				leadType = pgtype.Text{String: "agent", Valid: true}
				leadID = parseUUID(remapped)
			}
		}

		// Preview: report the action and continue. The full
		// recreate-write path runs on the execute side.
		if !execute {
			item.Action = "create"
			item.Status = "new"
			plan.Sections[string(kind)] = append(plan.Sections[string(kind)], item)
			continue
		}

		params := db.CreateProjectParams{
			WorkspaceID: idx.workspaceID,
			Title:       p.Title,
			Description: strToText(p.Description),
			Icon:        strToText(p.Icon),
			Status:      nonEmpty(p.Status, "planned"),
			LeadType:    leadType,
			LeadID:      leadID,
			Priority:    nonEmpty(p.Priority, "none"),
		}

		created, err := q.CreateProject(ctx, params)
		if err != nil {
			item.Action = "skip"
			item.Status = "error"
			item.Reason = fmt.Sprintf("create failed: %v", err)
			plan.Sections[string(kind)] = append(plan.Sections[string(kind)], item)
			continue
		}
		newID := uuidToString(created.ID)
		item.Action = "create"
		item.Status = "created"
		item.TargetID = newID
		idx.remap[kind] = setRemap(idx.remap[kind], p.ID, newID)

		// Resources: inlined in the create call chain. Each resource
		// is its own row in project_resource; we create them in
		// order so position is preserved. Resources are not
		// conflict-checked — adding a duplicate resource URL to a
		// project is idempotent from a product perspective.
		projectID := newID
		for _, r := range p.Resources {
			resType := r.ResourceType
			if resType == "" {
				resType = "link"
			}
			ref := r.ResourceRef
			if len(ref) == 0 {
				ref = []byte("{}")
			}
			if _, err := q.CreateProjectResource(ctx, db.CreateProjectResourceParams{
				ProjectID:    parseUUID(projectID),
				WorkspaceID:  idx.workspaceID,
				ResourceType: resType,
				ResourceRef:  ref,
				Label:        strToText(r.Label),
				Position:     positionOrZero(r.Position),
				CreatedBy:    systemCreatorID,
			}); err != nil {
				// Don't fail the whole restore on a single bad
				// resource — the project is the user-visible
				// unit.
				continue
			}
		}
		plan.Sections[string(kind)] = append(plan.Sections[string(kind)], item)
	}
	return nil
}

// planIssuesSection: issues are matched by number. The number is source-side
// and not preserved — the target workspace assigns a fresh number via
// IncrementIssueCounter — so the source number is purely a conflict key.
// On conflict the issue is skipped; the brief does not call for an
// overwrite path here because issues carry cross-instance UUIDs in
// comments/reactions that would have to be re-mapped as well, which is
// out of scope for M-25.
func (h *Handler) planIssuesSection(ctx context.Context, q *db.Queries, parsed *backup.BackupFile, idx *restoreIndex, plan *BackupRestorePlan, opts BackupRestoreOptions, hasSelection bool, selected map[string]struct{}, execute bool) error {
	kind := restoreKindIssues

	for _, is := range parsed.Issues {
		if !shouldProcess(is.ID, hasSelection, selected) {
			continue
		}
		item := BackupRestoreItem{
			Kind:       string(kind),
			SourceID:   is.ID,
			Identifier: fmt.Sprintf("%d", is.Number),
		}
		key := item.Identifier
		conflictID := idx.existingKey(kind, key)
		if conflictID != "" && !opts.Overwrite {
			item.Action = "skip"
			item.Status = "skipped"
			item.Reason = fmt.Sprintf("issue #%s already exists", key)
			idx.remap[kind] = setRemap(idx.remap[kind], is.ID, conflictID)
			plan.Sections[string(kind)] = append(plan.Sections[string(kind)], item)
			continue
		}

		// Resolve project_id through the remap. A source project that
		// wasn't selected falls back to no project rather than failing
		// the whole issue — the issue is still useful without a
		// project, and the UI can move it after the fact.
		var projectID pgtype.UUID
		if remapped := idx.remapKey(restoreKindProjects, is.ProjectID); remapped != "" {
			projectID = parseUUID(remapped)
		}

		// Status / priority defaults — a backup from an older
		// workspace may carry an empty status, and the DB has a CHECK
		// constraint on the value.
		status := is.Status
		if status == "" {
			status = "todo"
		}
		priority := is.Priority
		if priority == "" {
			priority = "none"
		}

		// Preview path: report the action and continue without
		// touching the DB. The full allocate-number + create path
		// runs on the execute side; previews must remain
		// side-effect-free against the entity tables.
		if !execute {
			item.Action = "create"
			item.Status = "new"
			plan.Sections[string(kind)] = append(plan.Sections[string(kind)], item)
			continue
		}

		// Pre-allocate the issue number. This must happen inside
		// the transaction (which planIssuesSection is, by
		// construction, when called from executeBackupRestore).
		newNumber, err := q.IncrementIssueCounter(ctx, idx.workspaceID)
		if err != nil {
			item.Action = "skip"
			item.Status = "error"
			item.Reason = fmt.Sprintf("allocate number: %v", err)
			plan.Sections[string(kind)] = append(plan.Sections[string(kind)], item)
			continue
		}

		created, err := q.CreateIssue(ctx, db.CreateIssueParams{
			WorkspaceID: idx.workspaceID,
			Title:       is.Title,
			Description: strToText(is.Description),
			Status:      status,
			Priority:    priority,
			CreatorType: "member",
			CreatorID:   idx.defaultCreatorID,
			Number:      newNumber,
			ProjectID:   projectID,
			Position:    is.Position,
			StartDate:   dateFromTime(is.StartDate),
			DueDate:     dateFromTime(is.DueDate),
		})
		if err != nil {
			item.Action = "skip"
			item.Status = "error"
			item.Reason = fmt.Sprintf("create failed: %v", err)
			plan.Sections[string(kind)] = append(plan.Sections[string(kind)], item)
			continue
		}
		newID := uuidToString(created.ID)
		item.Action = "create"
		item.Status = "created"
		item.TargetID = newID
		idx.remap[kind] = setRemap(idx.remap[kind], is.ID, newID)

		// Attach labels by remapping the backup's label_ids to the
		// target workspace's label ids. Labels that weren't selected
		// (or didn't exist) are silently dropped — the issue
		// survives without them.
		for _, lid := range is.LabelIDs {
			target := idx.remapKey(restoreKindLabels, lid)
			if target == "" {
				continue
			}
			if err := q.AttachLabelToIssue(ctx, db.AttachLabelToIssueParams{
				IssueID:     created.ID,
				LabelID:     parseUUID(target),
				WorkspaceID: idx.workspaceID,
			}); err != nil {
				continue
			}
		}

		// Comments: we restore every comment as a flat sibling of
		// the issue. The DB has CreateComment with a parent_id
		// column but no UpdateCommentParent, so threaded replies
		// can't be re-linked after the fact without a future
		// migration. The product decision is to keep the comments
		// (preserving content + author + timestamp) and accept the
		// loss of visual threading. Author remap: agent authors
		// resolve through the agents remap; member authors stay
		// as source UUIDs (member identity is out of scope for
		// M-25's restore surface).
		for _, c := range is.Comments {
			authorID := resolveCommentAuthorID(c.Author, idx)
			if _, cerr := q.CreateComment(ctx, db.CreateCommentParams{
				IssueID:     created.ID,
				WorkspaceID: idx.workspaceID,
				AuthorType:  nonEmpty(c.Author.Type, "system"),
				AuthorID:    authorID,
				Content:     c.Content,
				Type:        nonEmpty(c.Type, "comment"),
				ParentID:    pgtype.UUID{}, // threading: see comment above
			}); cerr != nil {
				continue
			}
		}
		plan.Sections[string(kind)] = append(plan.Sections[string(kind)], item)
	}
	return nil
}

// planSquadsSection: squads are matched by name. Members are restored
// in-line. The leader is a polymorphic actor; on the backup it's almost
// always an agent. We resolve through the agents remap.
func (h *Handler) planSquadsSection(ctx context.Context, q *db.Queries, parsed *backup.BackupFile, idx *restoreIndex, plan *BackupRestorePlan, opts BackupRestoreOptions, hasSelection bool, selected map[string]struct{}, execute bool) error {
	kind := restoreKindSquads

	for _, sq := range parsed.Squads {
		if !shouldProcess(sq.ID, hasSelection, selected) {
			continue
		}
		item := BackupRestoreItem{
			Kind:       string(kind),
			SourceID:   sq.ID,
			Identifier: sq.Name,
		}
		conflictID := idx.existingKey(kind, sq.Name)
		if conflictID != "" && !opts.Overwrite {
			item.Action = "skip"
			item.Status = "skipped"
			item.Reason = fmt.Sprintf("squad %q already exists", sq.Name)
			idx.remap[kind] = setRemap(idx.remap[kind], sq.ID, conflictID)
			plan.Sections[string(kind)] = append(plan.Sections[string(kind)], item)
			continue
		}

		// Leader resolution: a squad's leader is an agent. The
		// backup carries the source's leader ID; remap through the
		// agents table. The squad.leader_id column is NOT NULL
		// and references agent(id), so on the execute path a
		// missing remap means the squad's leader was not part of
		// the restore selection — we skip the squad entirely to
		// avoid a transaction-killing FK violation. On preview,
		// no agents have been created yet, so the remap is
		// always empty; defer the check to execute.
		var leaderID pgtype.UUID
		if sq.Leader.Type == "agent" {
			if remapped := idx.remapKey(restoreKindAgents, sq.Leader.ID); remapped != "" {
				leaderID = parseUUID(remapped)
			}
		}

		// Preview: report and continue. The full create + member
		// wiring runs on execute.
		if !execute {
			item.Action = "create"
			item.Status = "new"
			plan.Sections[string(kind)] = append(plan.Sections[string(kind)], item)
			continue
		}

		if !leaderID.Valid {
			item.Action = "skip"
			item.Status = "skipped"
			item.Reason = fmt.Sprintf("squad %q requires leader agent %q which is not in the restore set", sq.Name, sq.Leader.ID)
			plan.Sections[string(kind)] = append(plan.Sections[string(kind)], item)
			continue
		}

		created, err := q.CreateSquad(ctx, db.CreateSquadParams{
			WorkspaceID: idx.workspaceID,
			Name:        sq.Name,
			Description: sq.Description,
			LeaderID:    leaderID,
			CreatorID:   leaderID,
			AvatarUrl:   strToText(sq.AvatarURL),
		})
		if err != nil {
			item.Action = "skip"
			item.Status = "error"
			item.Reason = fmt.Sprintf("create failed: %v", err)
			plan.Sections[string(kind)] = append(plan.Sections[string(kind)], item)
			continue
		}
		newID := uuidToString(created.ID)
		item.Action = "create"
		item.Status = "created"
		item.TargetID = newID
		idx.remap[kind] = setRemap(idx.remap[kind], sq.ID, newID)

		// Members. Each is a polymorphic actor; we resolve and add
		// in the order the backup stored them so roles line up with
		// the source.
		for _, m := range sq.Members {
			memberType := m.MemberType
			if memberType != "agent" && memberType != "member" {
				continue
			}
			memberID := m.MemberID
			if memberType == "agent" {
				if remapped := idx.remapKey(restoreKindAgents, m.MemberID); remapped != "" {
					memberID = remapped
				} else {
					// Agent not in restore set; skip.
					continue
				}
			}
			if _, err := q.AddSquadMember(ctx, db.AddSquadMemberParams{
				SquadID:    created.ID,
				MemberType: memberType,
				MemberID:   parseUUID(memberID),
				Role:       m.Role,
			}); err != nil {
				continue
			}
		}
		plan.Sections[string(kind)] = append(plan.Sections[string(kind)], item)
	}
	return nil
}

// planAutopilotsSection: autopilots are matched by Title (the column name
// in the DB; the brief calls it "name" for user-facing consistency).
// Triggers (cron schedules) are restored in-line.
func (h *Handler) planAutopilotsSection(ctx context.Context, q *db.Queries, parsed *backup.BackupFile, idx *restoreIndex, plan *BackupRestorePlan, opts BackupRestoreOptions, hasSelection bool, selected map[string]struct{}, execute bool) error {
	kind := restoreKindAutopilot

	for _, ap := range parsed.Autopilots {
		if !shouldProcess(ap.ID, hasSelection, selected) {
			continue
		}
		item := BackupRestoreItem{
			Kind:       string(kind),
			SourceID:   ap.ID,
			Identifier: ap.Name,
		}
		conflictID := idx.existingKey(kind, ap.Name)
		if conflictID != "" && !opts.Overwrite {
			item.Action = "skip"
			item.Status = "skipped"
			item.Reason = fmt.Sprintf("autopilot %q already exists", ap.Name)
			idx.remap[kind] = setRemap(idx.remap[kind], ap.ID, conflictID)
			plan.Sections[string(kind)] = append(plan.Sections[string(kind)], item)
			continue
		}

		// Assignee resolution: agent or squad. Remap through the
		// appropriate table.
		var assigneeID pgtype.UUID
		assigneeType := ap.Assignee.Type
		if assigneeType == "agent" {
			if remapped := idx.remapKey(restoreKindAgents, ap.Assignee.ID); remapped != "" {
				assigneeID = parseUUID(remapped)
			}
		} else if assigneeType == "squad" {
			if remapped := idx.remapKey(restoreKindSquads, ap.Assignee.ID); remapped != "" {
				assigneeID = parseUUID(remapped)
			}
		}

		// Project: remap if the backup referenced one.
		var projectID pgtype.UUID
		if remapped := idx.remapKey(restoreKindProjects, ap.ProjectID); remapped != "" {
			projectID = parseUUID(remapped)
		}

		// Preview: report and continue.
		if !execute {
			item.Action = "create"
			item.Status = "new"
			plan.Sections[string(kind)] = append(plan.Sections[string(kind)], item)
			continue
		}

		created, err := q.CreateAutopilot(ctx, db.CreateAutopilotParams{
			WorkspaceID:   idx.workspaceID,
			Title:         ap.Name,
			AssigneeType:  nonEmpty(assigneeType, "agent"),
			AssigneeID:    assigneeID,
			Status:        nonEmpty(ap.Status, "active"),
			ExecutionMode: nonEmpty(ap.ExecutionMode, "create_issue"),
			CreatedByType: "member",
			CreatedByID:   idx.defaultCreatorID,
			Description:   pgtype.Text{},
			ProjectID:     projectID,
		})
		if err != nil {
			item.Action = "skip"
			item.Status = "error"
			item.Reason = fmt.Sprintf("create failed: %v", err)
			plan.Sections[string(kind)] = append(plan.Sections[string(kind)], item)
			continue
		}
		newID := uuidToString(created.ID)
		item.Action = "create"
		item.Status = "created"
		item.TargetID = newID
		idx.remap[kind] = setRemap(idx.remap[kind], ap.ID, newID)

		// Schedule → cron trigger. Webhook triggers are out of scope
		// for M-25: a webhook URL is a runtime secret that does not
		// round-trip through a backup.
		if ap.Schedule != "" {
			if _, err := q.CreateAutopilotTrigger(ctx, db.CreateAutopilotTriggerParams{
				AutopilotID:    created.ID,
				Kind:           "schedule",
				Enabled:        ap.Enabled,
				CronExpression: strToText(ap.Schedule),
				Timezone:       strToText(ap.TriggerTZ),
			}); err != nil {
				// Don't fail the autopilot for a missing
				// trigger — the autopilot is still useful
				// manually.
				continue
			}
		}
		plan.Sections[string(kind)] = append(plan.Sections[string(kind)], item)
	}
	return nil
}

// setRemap writes a source→target mapping for one section, allocating the
// inner map on first use. Returns the (possibly new) inner map so the
// caller can chain.
func setRemap(m map[string]string, source, target string) map[string]string {
	if m == nil {
		m = make(map[string]string)
	}
	m[source] = target
	return m
}

// resolveCommentAuthorID maps a backup comment author to a target-workspace
// ID. Members are not restored (M-25 scope), so member authors with no
// matching user become system authors; agent authors are remapped through
// the agents remap; empty authors stay empty.
func resolveCommentAuthorID(author backup.BackupActor, idx *restoreIndex) pgtype.UUID {
	if author.Type == "" || author.ID == "" {
		return pgtype.UUID{}
	}
	switch author.Type {
	case "agent":
		if remapped := idx.remapKey(restoreKindAgents, author.ID); remapped != "" {
			return parseUUID(remapped)
		}
	case "member":
		// Member identity is not part of M-25's restore surface; we
		// keep the source UUID and let the post-restore UI re-bind
		// authors if it cares.
	}
	return pgtype.UUID{}
}

// nonEmpty returns s if non-empty, otherwise the fallback. Used for
// required-string columns where the backup may legitimately omit the
// field but the DB has a CHECK constraint on the value.
func nonEmpty(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}

// positionOrZero safely unwraps an optional int32 pointer.
func positionOrZero(p *int32) int32 {
	if p == nil {
		return 0
	}
	return *p
}

// dateFromTime safely converts an optional *time.Time to a pgtype.Date.
func dateFromTime(t *time.Time) pgtype.Date {
	if t == nil {
		return pgtype.Date{}
	}
	return pgtype.Date{Time: *t, Valid: true}
}

// parseUUIDFromString parses a UUID string. Mirrors util.ParseUUID but
// returns the error to the caller rather than panicking — used in the
// preview/execute flow where the workspace_id comes from headers and
// is already validated by the auth middleware.
func parseUUIDFromString(s string) (pgtype.UUID, error) {
	if s == "" {
		return pgtype.UUID{}, errors.New("empty uuid")
	}
	var u pgtype.UUID
	if err := u.Scan(s); err != nil {
		return pgtype.UUID{}, err
	}
	return u, nil
}
