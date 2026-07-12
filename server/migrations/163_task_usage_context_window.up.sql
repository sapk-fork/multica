-- Track context-window occupancy per task run, distinct from the cumulative
-- token counters already on task_usage.
--
-- input/output/cache_* accumulate across every API call in a run, so they
-- answer "how many tokens did this task burn". They cannot answer "how full
-- was the context window", which is a per-call gauge: the size of the prompt
-- sent on a single call (input + cache tokens), peaking as conversation
-- history accumulates. context_window_tokens records that peak; the frontend
-- gauge compares it against context_window_max_tokens (the model's window) to
-- warn about context rot before it degrades output.
--
-- Both default to 0, meaning "not reported". Not every agent CLI exposes
-- context-window data, and max is often unknown at write time (the frontend
-- can fall back to a model->window lookup when max is 0).
ALTER TABLE task_usage
    ADD COLUMN context_window_tokens BIGINT NOT NULL DEFAULT 0,
    ADD COLUMN context_window_max_tokens BIGINT NOT NULL DEFAULT 0;
