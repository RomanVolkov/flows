CREATE TABLE definition_revisions (
    id TEXT PRIMARY KEY,
    definition_id TEXT NOT NULL,
    digest TEXT NOT NULL,
    compiled_json TEXT NOT NULL CHECK (json_valid(compiled_json)),
    created_at INTEGER NOT NULL,
    UNIQUE (definition_id, digest)
) STRICT;

CREATE TABLE run_roots (
    id TEXT PRIMARY KEY,
    definition_revision_id TEXT REFERENCES definition_revisions(id),
    request_id TEXT UNIQUE,
    graph_revision INTEGER NOT NULL DEFAULT 0 CHECK (graph_revision >= 0),
    created_at INTEGER NOT NULL
) STRICT;

CREATE TABLE nodes (
    id TEXT PRIMARY KEY,
    run_id TEXT NOT NULL REFERENCES run_roots(id),
    parent_id TEXT,
    kind TEXT NOT NULL CHECK (kind IN ('group','session','tool','watch')),
    name TEXT NOT NULL,
    status TEXT NOT NULL CHECK (status IN (
        'waiting_for_dependencies','ready','running','waiting_for_input','stopping',
        'succeeded','failed','interrupted','skipped','cancelled'
    )),
    wait_kind TEXT CHECK (wait_kind IN ('edge','timer','human')),
    wait_ref TEXT,
    wait_until INTEGER,
    active_attempt_id TEXT,
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL,
    UNIQUE (run_id, id),
    FOREIGN KEY (run_id, parent_id) REFERENCES nodes(run_id, id),
    FOREIGN KEY (id, active_attempt_id) REFERENCES node_attempts(node_id, id),
    CHECK ((wait_kind IS NOT NULL) = (status IN ('waiting_for_dependencies','waiting_for_input'))),
    CHECK ((wait_until IS NOT NULL) = (wait_kind IS 'timer'))
) STRICT;
CREATE INDEX nodes_parent_idx ON nodes(run_id, parent_id);

CREATE TABLE edges (
    id TEXT PRIMARY KEY,
    run_id TEXT NOT NULL REFERENCES run_roots(id),
    from_id TEXT NOT NULL,
    to_id TEXT NOT NULL,
    milestone TEXT NOT NULL DEFAULT '',
    FOREIGN KEY (run_id, from_id) REFERENCES nodes(run_id, id),
    FOREIGN KEY (run_id, to_id) REFERENCES nodes(run_id, id),
    UNIQUE (from_id, to_id, milestone),
    CHECK (from_id <> to_id)
) STRICT;
CREATE INDEX edges_run_idx ON edges(run_id);
CREATE INDEX edges_target_idx ON edges(to_id);

CREATE TABLE node_attempts (
    id TEXT PRIMARY KEY,
    node_id TEXT NOT NULL REFERENCES nodes(id),
    number INTEGER NOT NULL CHECK (number > 0),
    status TEXT NOT NULL CHECK (status IN (
        'running','waiting_for_input','stopping','succeeded','failed','interrupted','cancelled'
    )),
    started_at INTEGER NOT NULL,
    finished_at INTEGER,
    UNIQUE (node_id, id),
    UNIQUE (node_id, number)
) STRICT;

CREATE TABLE milestones (
    attempt_id TEXT NOT NULL REFERENCES node_attempts(id),
    name TEXT NOT NULL,
    reached_at INTEGER NOT NULL,
    PRIMARY KEY (attempt_id, name)
) STRICT;

CREATE TABLE sessions (
    id TEXT PRIMARY KEY,
    attempt_id TEXT NOT NULL REFERENCES node_attempts(id),
    harness TEXT NOT NULL,
    conversation_id TEXT,
    started_at INTEGER NOT NULL,
    ended_at INTEGER
) STRICT;
CREATE INDEX sessions_attempt_idx ON sessions(attempt_id);

CREATE TABLE activities (
    id TEXT PRIMARY KEY,
    attempt_id TEXT NOT NULL REFERENCES node_attempts(id),
    operation TEXT NOT NULL,
    status TEXT NOT NULL,
    started_at INTEGER NOT NULL,
    finished_at INTEGER
) STRICT;
CREATE INDEX activities_attempt_idx ON activities(attempt_id);

CREATE TABLE artifacts (
    id TEXT PRIMARY KEY,
    attempt_id TEXT NOT NULL REFERENCES node_attempts(id),
    kind TEXT NOT NULL,
    relative_path TEXT NOT NULL,
    digest TEXT NOT NULL,
    size INTEGER NOT NULL CHECK (size >= 0),
    created_at INTEGER NOT NULL
) STRICT;
CREATE INDEX artifacts_attempt_idx ON artifacts(attempt_id);

CREATE TABLE credential_refs (
    id TEXT PRIMARY KEY,
    provider TEXT NOT NULL,
    local_ref TEXT NOT NULL,
    UNIQUE (provider, local_ref)
) STRICT;

CREATE TABLE events (
    sequence INTEGER PRIMARY KEY AUTOINCREMENT,
    run_id TEXT NOT NULL REFERENCES run_roots(id),
    node_id TEXT,
    attempt_id TEXT REFERENCES node_attempts(id),
    kind TEXT NOT NULL,
    payload_json TEXT NOT NULL CHECK (json_valid(payload_json)),
    created_at INTEGER NOT NULL,
    FOREIGN KEY (run_id, node_id) REFERENCES nodes(run_id, id)
) STRICT;
CREATE INDEX events_run_idx ON events(run_id, sequence);
CREATE INDEX events_attempt_idx ON events(attempt_id, sequence);

CREATE TABLE completion_dispositions (
    attempt_id TEXT NOT NULL REFERENCES node_attempts(id),
    event_id TEXT NOT NULL,
    payload_digest TEXT NOT NULL,
    disposition TEXT NOT NULL CHECK (disposition IN ('accepted','rejected')),
    reason TEXT,
    created_at INTEGER NOT NULL,
    PRIMARY KEY (attempt_id, event_id)
) STRICT;
