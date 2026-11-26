CREATE TABLE usage_receipts (
    id TEXT PRIMARY KEY,
    provider TEXT NOT NULL,
    model TEXT NOT NULL,
    conversation_id TEXT NOT NULL DEFAULT '',
    source_key TEXT NOT NULL,
    session_id TEXT NOT NULL DEFAULT '',
    attempt_id TEXT NOT NULL DEFAULT '',
    run_id TEXT NOT NULL DEFAULT '',
    node_id TEXT NOT NULL DEFAULT '',
    event_time INTEGER NOT NULL,
    input_tokens INTEGER NOT NULL DEFAULT 0,
    cache_read_tokens INTEGER NOT NULL DEFAULT 0,
    cache_write_tokens INTEGER NOT NULL DEFAULT 0,
    output_tokens INTEGER NOT NULL DEFAULT 0,
    reasoning_tokens INTEGER NOT NULL DEFAULT 0,
    context_tokens INTEGER NOT NULL DEFAULT 0,
    context_window INTEGER NOT NULL DEFAULT 0,
    coverage TEXT NOT NULL CHECK (coverage IN ('live','final','unavailable')),
    attributed_at_completion INTEGER NOT NULL DEFAULT 0,
    ambiguous INTEGER NOT NULL DEFAULT 0,
    created_at INTEGER NOT NULL,
    UNIQUE (provider, source_key)
) STRICT;
CREATE INDEX usage_receipts_time_idx ON usage_receipts(event_time);
CREATE INDEX usage_receipts_attempt_idx ON usage_receipts(attempt_id);
CREATE INDEX usage_receipts_run_idx ON usage_receipts(run_id);

CREATE TABLE usage_offsets (
    source_path TEXT PRIMARY KEY,
    byte_offset INTEGER NOT NULL DEFAULT 0,
    last_input INTEGER NOT NULL DEFAULT 0,
    last_cache_read INTEGER NOT NULL DEFAULT 0,
    last_output INTEGER NOT NULL DEFAULT 0,
    last_reasoning INTEGER NOT NULL DEFAULT 0,
    updated_at INTEGER NOT NULL
) STRICT;
