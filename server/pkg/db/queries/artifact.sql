-- name: CreateArtifact :one
INSERT INTO artifact (
  workspace_id, issue_id, attachment_id, name, description, artifact_type, status
) VALUES (
  $1, $2, $3, $4, sqlc.narg('description'), $5, $6
) RETURNING *;

-- name: GetArtifact :one
SELECT * FROM artifact
WHERE id = $1 AND workspace_id = $2;

-- name: ListArtifactsByIssue :many
SELECT * FROM artifact
WHERE issue_id = $1 AND workspace_id = $2
ORDER BY created_at ASC;

-- name: UpdateArtifact :one
UPDATE artifact SET
  name          = COALESCE(sqlc.narg('name'), name),
  description   = sqlc.narg('description'),
  artifact_type = COALESCE(sqlc.narg('artifact_type'), artifact_type),
  status        = COALESCE(sqlc.narg('status'), status),
  updated_at    = now()
WHERE id = $1 AND workspace_id = $2
RETURNING *;

-- name: DeleteArtifact :exec
DELETE FROM artifact WHERE id = $1 AND workspace_id = $2;

-- name: GetArtifactByAttachmentID :one
SELECT * FROM artifact
WHERE attachment_id = $1 AND workspace_id = $2;
