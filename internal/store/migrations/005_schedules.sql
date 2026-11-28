CREATE TABLE workflow_schedules (
    id TEXT PRIMARY KEY,
    schedule_json TEXT NOT NULL CHECK (json_valid(schedule_json)),
    updated_at INTEGER NOT NULL
) STRICT;
