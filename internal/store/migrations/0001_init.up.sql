-- 0001_init.sql — SPEC §6.2 core schema.

CREATE TABLE projects (
  id TEXT PRIMARY KEY,            -- ULID
  name TEXT NOT NULL,
  kind TEXT NOT NULL CHECK (kind IN ('work','code')),
  workspace_path TEXT NOT NULL,
  allow_coding INTEGER NOT NULL DEFAULT 0,
  created_at TEXT NOT NULL,       -- ISO-8601 UTC
  settings_json TEXT NOT NULL DEFAULT '{}'
);

CREATE TABLE sessions (
  id TEXT PRIMARY KEY,
  project_id TEXT REFERENCES projects(id) ON DELETE CASCADE,
  title TEXT NOT NULL DEFAULT 'New session',
  persona TEXT CHECK (persona IN ('coder','researcher','chat')),
  model_ref TEXT NOT NULL DEFAULT '',
  status TEXT NOT NULL DEFAULT 'idle' CHECK (status IN ('idle','running','waiting_permission','done','error','archived')),
  pinned INTEGER NOT NULL DEFAULT 0,
  forked_from TEXT,
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);

CREATE TABLE messages (
  id INTEGER PRIMARY KEY,
  session_id TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
  role TEXT NOT NULL CHECK (role IN ('user','assistant','tool','system')),
  content_json TEXT NOT NULL,
  step_id TEXT,
  created_at TEXT NOT NULL
);

CREATE TABLE events (
  id INTEGER PRIMARY KEY,
  session_id TEXT NOT NULL,
  step_id TEXT,
  type TEXT NOT NULL,
  payload_json TEXT NOT NULL DEFAULT '{}',
  created_at TEXT NOT NULL
);

CREATE TABLE checkpoints (
  id INTEGER PRIMARY KEY,
  project_id TEXT,
  session_id TEXT,
  step_id TEXT,
  commit_sha TEXT NOT NULL,
  label TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL
);

CREATE TABLE permissions (
  id INTEGER PRIMARY KEY,
  session_id TEXT NOT NULL,
  path TEXT NOT NULL,
  mode TEXT NOT NULL CHECK (mode IN ('ro','rw')),
  granted INTEGER,                -- NULL = pending request
  created_at TEXT NOT NULL
);

CREATE INDEX idx_messages_session ON messages(session_id, id);
CREATE INDEX idx_events_session ON events(session_id, id);
CREATE INDEX idx_checkpoints_project ON checkpoints(project_id, id);
CREATE INDEX idx_permissions_session ON permissions(session_id);
