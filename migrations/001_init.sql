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
