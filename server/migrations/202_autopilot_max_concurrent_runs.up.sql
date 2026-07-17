-- Per-autopilot concurrency cap (M-87). 0 = unlimited (default), preserving
-- the prior unbounded behaviour for every existing row. When > 0 the dispatch
-- admission gate skips a new run while active runs (issue_created / running)
-- already meet the limit. This is the cleaner re-implementation of the
-- concurrency_policy column dropped in migration 043.
ALTER TABLE autopilot
    ADD COLUMN IF NOT EXISTS max_concurrent_runs INTEGER NOT NULL DEFAULT 0;
