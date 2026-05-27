-- artifact stores a versioned, typed reference to an attachment that has been
-- promoted to a "first-class" asset on an issue (e.g. a design doc, a
-- generated report, a code snippet). Each artifact is backed by exactly one
-- attachment (UNIQUE FK) so the binary content is stored once.
CREATE TABLE artifact (
  id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  workspace_id  UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
  issue_id      UUID NOT NULL REFERENCES issue(id) ON DELETE CASCADE,
  attachment_id UUID NOT NULL UNIQUE REFERENCES attachment(id) ON DELETE CASCADE,
  name          TEXT NOT NULL,
  description   TEXT,
  artifact_type TEXT NOT NULL DEFAULT 'other'
                  CHECK (artifact_type IN ('document','image','code','report','data','other')),
  status        TEXT NOT NULL DEFAULT 'draft'
                  CHECK (status IN ('draft','final','archived')),
  created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_artifact_issue_id      ON artifact(issue_id);
CREATE INDEX idx_artifact_workspace_id  ON artifact(workspace_id);
