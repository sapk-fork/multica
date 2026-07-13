package handler

import (
	"log/slog"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/logger"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

type ExportCommentResponse struct {
	ID             string               `json:"id"`
	AuthorType     string               `json:"author_type"`
	AuthorID       string               `json:"author_id"`
	Content        string               `json:"content"`
	Type           string               `json:"type"`
	ParentID       *string              `json:"parent_id"`
	CreatedAt      string               `json:"created_at"`
	UpdatedAt      string               `json:"updated_at"`
	ResolvedAt     *string              `json:"resolved_at"`
	ResolvedByType *string              `json:"resolved_by_type"`
	ResolvedByID   *string              `json:"resolved_by_id"`
	Reactions      []ReactionResponse   `json:"reactions"`
	Attachments    []AttachmentResponse `json:"attachments"`
}

type ExportIssueResponse struct {
	ID            string                  `json:"id"`
	Number        int32                   `json:"number"`
	Identifier    string                  `json:"identifier"`
	Title         string                  `json:"title"`
	Description   *string                 `json:"description"`
	Status        string                  `json:"status"`
	Priority      string                  `json:"priority"`
	AssigneeType  *string                 `json:"assignee_type"`
	AssigneeID    *string                 `json:"assignee_id"`
	CreatorType   string                  `json:"creator_type"`
	CreatorID     string                  `json:"creator_id"`
	ParentIssueID *string                 `json:"parent_issue_id"`
	ProjectID     *string                 `json:"project_id"`
	Position      float64                 `json:"position"`
	Stage         *int32                  `json:"stage"`
	StartDate     *string                 `json:"start_date"`
	DueDate       *string                 `json:"due_date"`
	CreatedAt     string                  `json:"created_at"`
	UpdatedAt     string                  `json:"updated_at"`
	Metadata      map[string]any          `json:"metadata"`
	Labels        []LabelResponse         `json:"labels"`
	Comments      []ExportCommentResponse `json:"comments"`
	Children      []ExportIssueResponse   `json:"children"`
}

type ExportResponse struct {
	Issue ExportIssueResponse `json:"issue"`
}

func (h *Handler) ExportIssue(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	issue, ok := h.loadIssueForUser(w, r, id)
	if !ok {
		return
	}

	prefix := h.getIssuePrefix(r.Context(), issue.WorkspaceID)
	exported := h.exportIssueRecursive(r, issue, prefix)
	writeJSON(w, http.StatusOK, ExportResponse{Issue: exported})
}

func (h *Handler) exportIssueRecursive(r *http.Request, issue db.Issue, prefix string) ExportIssueResponse {
	ctx := r.Context()
	resp := ExportIssueResponse{
		ID:            uuidToString(issue.ID),
		Number:        issue.Number,
		Identifier:    prefix + "-" + strconv.Itoa(int(issue.Number)),
		Title:         issue.Title,
		Description:   textToPtr(issue.Description),
		Status:        issue.Status,
		Priority:      issue.Priority,
		AssigneeType:  textToPtr(issue.AssigneeType),
		AssigneeID:    uuidToPtr(issue.AssigneeID),
		CreatorType:   issue.CreatorType,
		CreatorID:     uuidToString(issue.CreatorID),
		ParentIssueID: uuidToPtr(issue.ParentIssueID),
		ProjectID:     uuidToPtr(issue.ProjectID),
		Position:      issue.Position,
		Stage:         int4ToPtr(issue.Stage),
		StartDate:     dateToPtr(issue.StartDate),
		DueDate:       dateToPtr(issue.DueDate),
		CreatedAt:     timestampToString(issue.CreatedAt),
		UpdatedAt:     timestampToString(issue.UpdatedAt),
		Metadata:      parseIssueMetadata(issue.Metadata),
	}

	labels := h.labelsByIssue(ctx, issue.WorkspaceID, []pgtype.UUID{issue.ID})[uuidToString(issue.ID)]
	if labels != nil {
		resp.Labels = labels
	} else {
		resp.Labels = []LabelResponse{}
	}

	comments, err := h.Queries.ListCommentsForIssue(ctx, db.ListCommentsForIssueParams{
		IssueID:     issue.ID,
		WorkspaceID: issue.WorkspaceID,
		Limit:       commentHardCap,
	})
	if err != nil {
		slog.Warn("export: failed to list comments", append(logger.RequestAttrs(r), "error", err, "issue_id", uuidToString(issue.ID))...)
		resp.Comments = []ExportCommentResponse{}
	} else if len(comments) == 0 {
		resp.Comments = []ExportCommentResponse{}
	} else {
		commentIDs := make([]pgtype.UUID, len(comments))
		for i, c := range comments {
			commentIDs[i] = c.ID
		}

		reactionsByComment := map[string][]ReactionResponse{}
		reactions, rerr := h.Queries.ListReactionsByCommentIDs(ctx, commentIDs)
		if rerr == nil {
			for _, rx := range reactions {
				cid := uuidToString(rx.CommentID)
				reactionsByComment[cid] = append(reactionsByComment[cid], reactionToResponse(rx))
			}
		}

		attachmentsByComment := map[string][]AttachmentResponse{}
		workspaceIDStr := uuidToString(issue.WorkspaceID)
		atts, aerr := h.Queries.ListAttachmentsByCommentIDs(ctx, db.ListAttachmentsByCommentIDsParams{
			Column1:     commentIDs,
			WorkspaceID: parseUUID(workspaceIDStr),
		})
		if aerr == nil {
			for _, a := range atts {
				cid := uuidToString(a.CommentID)
				attachmentsByComment[cid] = append(attachmentsByComment[cid], h.attachmentToResponse(a))
			}
		}

		resp.Comments = make([]ExportCommentResponse, len(comments))
		for i, c := range comments {
			cid := uuidToString(c.ID)
			resp.Comments[i] = ExportCommentResponse{
				ID:             cid,
				AuthorType:     c.AuthorType,
				AuthorID:       uuidToString(c.AuthorID),
				Content:        c.Content,
				Type:           c.Type,
				ParentID:       uuidToPtr(c.ParentID),
				CreatedAt:      timestampToString(c.CreatedAt),
				UpdatedAt:      timestampToString(c.UpdatedAt),
				ResolvedAt:     timestampToPtr(c.ResolvedAt),
				ResolvedByType: textToPtr(c.ResolvedByType),
				ResolvedByID:   uuidToPtr(c.ResolvedByID),
				Reactions:      reactionsByComment[cid],
				Attachments:    attachmentsByComment[cid],
			}
			if resp.Comments[i].Reactions == nil {
				resp.Comments[i].Reactions = []ReactionResponse{}
			}
			if resp.Comments[i].Attachments == nil {
				resp.Comments[i].Attachments = []AttachmentResponse{}
			}
		}
	}

	children, err := h.Queries.ListChildIssues(ctx, issue.ID)
	if err == nil && len(children) > 0 {
		resp.Children = make([]ExportIssueResponse, len(children))
		for i, child := range children {
			resp.Children[i] = h.exportIssueRecursive(r, child, prefix)
		}
	} else {
		resp.Children = []ExportIssueResponse{}
	}

	return resp
}
