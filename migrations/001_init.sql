-- GENERATED from internal/state/sqlite.go by scripts/check-schema-drift.sh.
-- Do not edit by hand: run `bash scripts/check-schema-drift.sh --write`.
-- CI fails when this file and the Go schema disagree.

CREATE TABLE IF NOT EXISTS requirements (
    id TEXT PRIMARY KEY,
    title TEXT NOT NULL,
    description TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'pending',
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS stories (
    id TEXT PRIMARY KEY,
    req_id TEXT NOT NULL,
    title TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    acceptance_criteria TEXT NOT NULL DEFAULT '',
    complexity INTEGER NOT NULL DEFAULT 0,
    status TEXT NOT NULL DEFAULT 'draft',
    agent_id TEXT NOT NULL DEFAULT '',
    branch TEXT NOT NULL DEFAULT '',
    pr_url TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS agents (
    id TEXT PRIMARY KEY,
    type TEXT NOT NULL,
    model TEXT NOT NULL DEFAULT '',
    runtime TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL DEFAULT 'idle',
    current_story_id TEXT NOT NULL DEFAULT '',
    session_name TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS story_deps (
    story_id TEXT NOT NULL,
    depends_on_id TEXT NOT NULL,
    PRIMARY KEY (story_id, depends_on_id)
);

CREATE TABLE IF NOT EXISTS escalations (
    id TEXT PRIMARY KEY,
    story_id TEXT NOT NULL DEFAULT '',
    from_agent TEXT NOT NULL,
    reason TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'pending',
    resolution TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    resolved_at TIMESTAMP
);

CREATE TABLE IF NOT EXISTS agent_scores (
    id TEXT PRIMARY KEY,
    agent_id TEXT NOT NULL,
    story_id TEXT NOT NULL,
    quality INTEGER NOT NULL DEFAULT 0,
    reliability INTEGER NOT NULL DEFAULT 0,
    duration_s INTEGER NOT NULL DEFAULT 0,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS story_databases (
    story_id          TEXT NOT NULL,
    db_id             TEXT NOT NULL,
    db_name           TEXT NOT NULL,
    provider          TEXT NOT NULL DEFAULT '',
    status            TEXT NOT NULL,
    template          TEXT NOT NULL DEFAULT '',
    conn_string_hash  TEXT NOT NULL DEFAULT '',
    error             TEXT NOT NULL DEFAULT '',
    created_at        TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    deleted_at        TIMESTAMP,
    duration_seconds  REAL DEFAULT 0,
    bytes_used        INTEGER DEFAULT 0,
    PRIMARY KEY (story_id, db_id)
);
CREATE INDEX IF NOT EXISTS idx_story_databases_status ON story_databases(status);

-- projection_meta tracks how far the projection has been reconciled with the
-- event log. applied_event_count is bumped on every successful Project; on
-- open, loadStores compares it against the event-log length and rebuilds the
-- projection when it has fallen behind (the Append-ok / Project-failed desync
-- that engine.emitEventOrLog documents).
CREATE TABLE IF NOT EXISTS projection_meta (
    key   TEXT PRIMARY KEY,
    value INTEGER NOT NULL
);
INSERT OR IGNORE INTO projection_meta (key, value) VALUES ('applied_event_count', 0);

-- Column upgrades NewSQLiteStore applies on every open, in source order.
-- SQLite has no ADD COLUMN IF NOT EXISTS: the Go code ignores the
-- 'duplicate column name' error each statement raises on an up-to-date
-- database, so external tooling must run these with errors ignored too.
ALTER TABLE stories ADD COLUMN acceptance_criteria TEXT NOT NULL DEFAULT '';
ALTER TABLE stories ADD COLUMN owned_files TEXT NOT NULL DEFAULT '[]';
ALTER TABLE stories ADD COLUMN wave_hint TEXT NOT NULL DEFAULT 'parallel';
ALTER TABLE requirements ADD COLUMN repo_path TEXT NOT NULL DEFAULT '';
ALTER TABLE stories ADD COLUMN wave INTEGER NOT NULL DEFAULT 0;
ALTER TABLE stories ADD COLUMN pr_number INTEGER NOT NULL DEFAULT 0;
ALTER TABLE stories ADD COLUMN escalation_tier INTEGER DEFAULT 0;
ALTER TABLE stories ADD COLUMN split_depth INTEGER DEFAULT 0;
ALTER TABLE escalations ADD COLUMN from_tier INTEGER DEFAULT 0;
ALTER TABLE escalations ADD COLUMN to_tier INTEGER DEFAULT 0;
ALTER TABLE requirements ADD COLUMN req_type TEXT NOT NULL DEFAULT '';
ALTER TABLE requirements ADD COLUMN is_existing BOOLEAN NOT NULL DEFAULT 0;
ALTER TABLE requirements ADD COLUMN investigation_report_json TEXT NOT NULL DEFAULT '';
ALTER TABLE stories ADD COLUMN merged_at TIMESTAMP;
