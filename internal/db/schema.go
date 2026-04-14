package db

const schemaSQL = `
CREATE TABLE IF NOT EXISTS workspaces (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    path        TEXT NOT NULL UNIQUE,
    name        TEXT,
    created_at  DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS messages (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    workspace_id    INTEGER NOT NULL REFERENCES workspaces(id),
    session_id      TEXT NOT NULL,
    seq             INTEGER NOT NULL,
    role            TEXT NOT NULL,
    content         TEXT NOT NULL,
    token_count     INTEGER,
    created_at      DATETIME DEFAULT CURRENT_TIMESTAMP,
    UNIQUE(session_id, seq)
);
CREATE INDEX IF NOT EXISTS idx_messages_session ON messages(session_id, seq);
CREATE INDEX IF NOT EXISTS idx_messages_workspace ON messages(workspace_id, created_at);

CREATE TABLE IF NOT EXISTS summaries (
    id              TEXT PRIMARY KEY,
    workspace_id    INTEGER NOT NULL REFERENCES workspaces(id),
    kind            TEXT NOT NULL CHECK(kind IN ('leaf', 'condensed')),
    depth           INTEGER NOT NULL DEFAULT 0,
    content         TEXT NOT NULL,
    token_count     INTEGER,
    earliest_at     DATETIME,
    latest_at       DATETIME,
    model           TEXT,
    created_at      DATETIME DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_summaries_workspace_depth ON summaries(workspace_id, depth, created_at);

CREATE TABLE IF NOT EXISTS summary_messages (
    summary_id  TEXT NOT NULL REFERENCES summaries(id),
    message_id  INTEGER NOT NULL REFERENCES messages(id),
    ordinal     INTEGER NOT NULL,
    PRIMARY KEY (summary_id, message_id)
);

CREATE TABLE IF NOT EXISTS summary_parents (
    summary_id          TEXT NOT NULL REFERENCES summaries(id),
    parent_summary_id   TEXT NOT NULL REFERENCES summaries(id),
    ordinal             INTEGER NOT NULL,
    PRIMARY KEY (summary_id, parent_summary_id)
);

CREATE TABLE IF NOT EXISTS ingest_state (
    session_id              TEXT PRIMARY KEY,
    transcript_path         TEXT NOT NULL,
    last_processed_offset   INTEGER NOT NULL DEFAULT 0,
    updated_at              DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE VIRTUAL TABLE IF NOT EXISTS messages_fts USING fts5(
    content,
    content=messages,
    content_rowid=id
);

CREATE VIRTUAL TABLE IF NOT EXISTS summaries_fts USING fts5(
    content,
    content=summaries,
    content_rowid=rowid
);

CREATE VIRTUAL TABLE IF NOT EXISTS messages_fts_cjk USING fts5(
    content,
    content=messages,
    content_rowid=id,
    tokenize='trigram'
);

CREATE VIRTUAL TABLE IF NOT EXISTS summaries_fts_cjk USING fts5(
    content,
    content=summaries,
    content_rowid=rowid,
    tokenize='trigram'
);
`
