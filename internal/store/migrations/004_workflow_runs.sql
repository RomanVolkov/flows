CREATE TABLE workflow_runs (
    run_id TEXT PRIMARY KEY REFERENCES run_roots(id),
    root_node_id TEXT NOT NULL REFERENCES nodes(id) ON DELETE CASCADE,
    definition_revision_id TEXT NOT NULL REFERENCES definition_revisions(id),
    workflow_id TEXT NOT NULL,
    workflow_digest TEXT NOT NULL,
    package_dir TEXT NOT NULL,
    work_dir TEXT NOT NULL,
    inputs_json TEXT NOT NULL CHECK (json_valid(inputs_json)),
    outputs_json TEXT CHECK (json_valid(outputs_json)),
    snapshot_json TEXT NOT NULL CHECK (json_valid(snapshot_json)),
    status TEXT NOT NULL,
    error TEXT NOT NULL DEFAULT '',
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
) STRICT;

CREATE TABLE workflow_steps (
    node_id TEXT PRIMARY KEY REFERENCES nodes(id) ON DELETE CASCADE,
    run_id TEXT NOT NULL REFERENCES workflow_runs(run_id) ON DELETE CASCADE,
    parent_node_id TEXT REFERENCES nodes(id) ON DELETE CASCADE,
    step_key TEXT NOT NULL,
    step_id TEXT NOT NULL,
    node_type TEXT NOT NULL,
    item_json TEXT CHECK (json_valid(item_json)),
    iteration INTEGER NOT NULL DEFAULT 0,
    pinned_tool_id TEXT NOT NULL DEFAULT '',
    pinned_cache_key TEXT NOT NULL DEFAULT '',
    pinned_binary_digest TEXT NOT NULL DEFAULT '',
    invocation_id TEXT NOT NULL,
    in_flight_unknown INTEGER NOT NULL DEFAULT 0 CHECK (in_flight_unknown IN (0, 1)),
    outputs_json TEXT CHECK (json_valid(outputs_json)),
    milestones_json TEXT CHECK (json_valid(milestones_json)),
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL,
    UNIQUE(run_id, step_key)
) STRICT;
CREATE INDEX workflow_steps_run_idx ON workflow_steps(run_id);
