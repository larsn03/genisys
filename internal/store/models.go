package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
)

// ErrNotFound is returned by lookups without a row.
var ErrNotFound = errors.New("store: not found")

// Project maps the projects table (SPEC §6.2).
type Project struct {
	ID            string `yaml:"-" json:"id"`
	Name          string `yaml:"name" json:"name"`
	Kind          string `yaml:"kind" json:"kind"` // work | code
	WorkspacePath string `yaml:"workspace_path" json:"workspace_path"`
	AllowCoding   bool   `yaml:"allow_coding" json:"allow_coding"`
	CreatedAt     string `yaml:"-" json:"created_at"`
	SettingsJSON  string `yaml:"-" json:"settings_json,omitempty"`
}

// Session maps the sessions table.
type Session struct {
	ID         string `yaml:"-" json:"id"`
	ProjectID  string `yaml:"-" json:"project_id,omitempty"` // "" = free chat
	Title      string `yaml:"title" json:"title"`
	Persona    string `yaml:"persona" json:"persona"`
	ModelRef   string `yaml:"model_ref" json:"model_ref"`
	Status     string `yaml:"-" json:"status"`
	Pinned     bool   `yaml:"pinned" json:"pinned"`
	ForkedFrom string `yaml:"-" json:"forked_from,omitempty"`
	CreatedAt  string `yaml:"-" json:"created_at"`
	UpdatedAt  string `yaml:"-" json:"updated_at"`
}

// Session statuses (SPEC §6.2).
const (
	StatusIdle              = "idle"
	StatusRunning           = "running"
	StatusWaitingPermission = "waiting_permission"
	StatusDone              = "done"
	StatusError             = "error"
	StatusArchived          = "archived"
)

// CreateProject inserts a project (fills ID/CreatedAt when empty).
func (db *DB) CreateProject(p *Project) error {
	if p.ID == "" {
		p.ID = NewID()
	}
	if p.CreatedAt == "" {
		p.CreatedAt = nowStamp()
	}
	if p.SettingsJSON == "" {
		p.SettingsJSON = "{}"
	}
	_, err := db.h.Exec(
		`INSERT INTO projects(id,name,kind,workspace_path,allow_coding,created_at,settings_json)
		 VALUES(?,?,?,?,?,?,?)`,
		p.ID, p.Name, p.Kind, p.WorkspacePath, p.AllowCoding, p.CreatedAt, p.SettingsJSON)
	return err
}

// GetProject loads a project by id.
func (db *DB) GetProject(id string) (*Project, error) {
	row := db.h.QueryRow(`SELECT id,name,kind,workspace_path,allow_coding,created_at,settings_json FROM projects WHERE id=?`, id)
	var p Project
	var ac int
	if err := row.Scan(&p.ID, &p.Name, &p.Kind, &p.WorkspacePath, &ac, &p.CreatedAt, &p.SettingsJSON); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	p.AllowCoding = ac != 0
	return &p, nil
}

// CreateSession inserts a session.
func (db *DB) CreateSession(s *Session) error {
	if s.ID == "" {
		s.ID = NewID()
	}
	if s.CreatedAt == "" {
		s.CreatedAt = nowStamp()
	}
	s.UpdatedAt = s.CreatedAt
	if s.Status == "" {
		s.Status = StatusIdle
	}
	if s.Title == "" {
		s.Title = "New session"
	}
	var pid any
	if s.ProjectID != "" {
		pid = s.ProjectID
	}
	_, err := db.h.Exec(
		`INSERT INTO sessions(id,project_id,title,persona,model_ref,status,pinned,forked_from,created_at,updated_at)
		 VALUES(?,?,?,?,?,?,?,?,?,?)`,
		s.ID, pid, s.Title, nilIfEmpty(s.Persona), s.ModelRef, s.Status, s.Pinned, nilIfEmpty(s.ForkedFrom), s.CreatedAt, s.UpdatedAt)
	return err
}

// GetSession loads a session by id.
func (db *DB) GetSession(id string) (*Session, error) {
	row := db.h.QueryRow(`SELECT id,COALESCE(project_id,''),title,COALESCE(persona,''),model_ref,status,pinned,COALESCE(forked_from,''),created_at,updated_at FROM sessions WHERE id=?`, id)
	var s Session
	var pin int
	if err := row.Scan(&s.ID, &s.ProjectID, &s.Title, &s.Persona, &s.ModelRef, &s.Status, &pin, &s.ForkedFrom, &s.CreatedAt, &s.UpdatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	s.Pinned = pin != 0
	return &s, nil
}

// SetSessionStatus updates status + updated_at.
func (db *DB) SetSessionStatus(id, status string) error {
	_, err := db.h.Exec(`UPDATE sessions SET status=?, updated_at=? WHERE id=?`, status, nowStamp(), id)
	return err
}

// SetSessionTitle renames a session.
// DeleteSession hard-deletes a session and its rows (SPEC §5.2 Delete).
func (db *DB) DeleteSession(id string) error {
	for _, q := range []string{
		"DELETE FROM messages WHERE session_id = ?",
		"DELETE FROM events WHERE session_id = ?",
		"DELETE FROM permissions WHERE session_id = ?",
		"DELETE FROM checkpoints WHERE session_id = ?",
		"DELETE FROM sessions WHERE id = ?",
	} {
		if _, err := db.h.Exec(q, id); err != nil {
			return err
		}
	}
	return nil
}

// CopyMessages duplicates a session's message rows onto a fresh session
// (fork, SPEC §5.2).
func (db *DB) CopyMessages(src, dst string) error {
	rows, err := db.h.Query("SELECT role, content_json, step_id FROM messages WHERE session_id = ? ORDER BY id", src)
	if err != nil {
		return err
	}
	defer rows.Close()
	var buf []struct {
		role string
		cont []byte
		step sql.NullString
	}
	for rows.Next() {
		var r struct {
			role string
			cont []byte
			step sql.NullString
		}
		if err := rows.Scan(&r.role, &r.cont, &r.step); err != nil {
			return err
		}
		buf = append(buf, r)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for _, r := range buf {
		if _, err := db.AppendMessage(dst, r.role, r.cont, r.step.String); err != nil {
			return err
		}
	}
	return nil
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func (db *DB) SetSessionTitle(id, title string) error {
	_, err := db.h.Exec(`UPDATE sessions SET title=?, updated_at=? WHERE id=?`, title, nowStamp(), id)
	return err
}

// ListSessions returns sessions newest-first (archived last).
func (db *DB) ListSessions() ([]*Session, error) {
	rows, err := db.h.Query(`SELECT id,COALESCE(project_id,''),title,COALESCE(persona,''),model_ref,status,pinned,COALESCE(forked_from,''),created_at,updated_at
		FROM sessions ORDER BY pinned DESC, updated_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Session
	for rows.Next() {
		var s Session
		var pin int
		if err := rows.Scan(&s.ID, &s.ProjectID, &s.Title, &s.Persona, &s.ModelRef, &s.Status, &pin, &s.ForkedFrom, &s.CreatedAt, &s.UpdatedAt); err != nil {
			return nil, err
		}
		s.Pinned = pin != 0
		out = append(out, &s)
	}
	return out, rows.Err()
}

// MarkOrphansInterrupted flips sessions stuck in running/waiting_permission
// from a previous process to error, returning the count (SPEC §23 boot step,
// P2-9).
func (db *DB) MarkOrphansInterrupted() (int, error) {
	res, err := db.h.Exec(
		`UPDATE sessions SET status=?, updated_at=? WHERE status IN ('running','waiting_permission')`,
		StatusError, nowStamp())
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}

// --- messages ----------------------------------------------------------------

// AppendMessage stores one OpenAI-shaped message (content_json).
func (db *DB) AppendMessage(sessionID, role string, contentJSON []byte, stepID string) (int64, error) {
	var sid any
	if stepID != "" {
		sid = stepID
	}
	res, err := db.h.Exec(
		`INSERT INTO messages(session_id,role,content_json,step_id,created_at) VALUES(?,?,?,?,?)`,
		sessionID, role, string(contentJSON), sid, nowStamp())
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// ListMessages returns messages in insertion order.
func (db *DB) ListMessages(sessionID string) ([]MessageRow, error) {
	rows, err := db.h.Query(`SELECT id,role,content_json,COALESCE(step_id,''),created_at FROM messages WHERE session_id=? ORDER BY id`, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []MessageRow
	for rows.Next() {
		var m MessageRow
		if err := rows.Scan(&m.ID, &m.Role, &m.ContentJSON, &m.StepID, &m.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// MessageRow is one messages-table entry.
type MessageRow struct {
	ID          int64  `json:"id"`
	Role        string `json:"role"`
	ContentJSON string `json:"content_json"`
	StepID      string `json:"step_id,omitempty"`
	CreatedAt   string `json:"created_at"`
}

// --- events ledger (SPEC §21) --------------------------------------------------

// AppendEvent writes the audit ledger; payload is marshaled to JSON.
func (db *DB) AppendEvent(sessionID, stepID, typ string, payload any) error {
	pj := "{}"
	if payload != nil {
		b, err := json.Marshal(payload)
		if err == nil {
			pj = string(b)
		}
	}
	var sid any
	if stepID != "" {
		sid = stepID
	}
	_, err := db.h.Exec(`INSERT INTO events(session_id,step_id,type,payload_json,created_at) VALUES(?,?,?,?,?)`,
		sessionID, sid, typ, pj, nowStamp())
	return err
}

// ListEvents returns the ledger for a session (UI replay).
func (db *DB) ListEvents(sessionID string) ([]EventRow, error) {
	rows, err := db.h.Query(`SELECT id,COALESCE(step_id,''),type,payload_json,created_at FROM events WHERE session_id=? ORDER BY id`, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []EventRow
	for rows.Next() {
		var e EventRow
		if err := rows.Scan(&e.ID, &e.StepID, &e.Type, &e.PayloadJSON, &e.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// EventRow is one events-table entry.
type EventRow struct {
	ID          int64  `json:"id"`
	StepID      string `json:"step_id,omitempty"`
	Type        string `json:"type"`
	PayloadJSON string `json:"payload_json"`
	CreatedAt   string `json:"created_at"`
}

// --- checkpoints index ----------------------------------------------------------

// AppendCheckpoint records a shadow-repo commit (SPEC §11).
func (db *DB) AppendCheckpoint(projectID, sessionID, stepID, sha, label string) (int64, error) {
	res, err := db.h.Exec(
		`INSERT INTO checkpoints(project_id,session_id,step_id,commit_sha,label,created_at) VALUES(?,?,?,?,?,?)`,
		nilIfEmpty(projectID), nilIfEmpty(sessionID), nilIfEmpty(stepID), sha, label, nowStamp())
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// CheckpointRow is one checkpoints-table entry.
type CheckpointRow struct {
	ID        int64  `json:"id"`
	ProjectID string `json:"project_id,omitempty"`
	SessionID string `json:"session_id,omitempty"`
	StepID    string `json:"step_id,omitempty"`
	SHA       string `json:"sha"`
	Label     string `json:"label"`
	CreatedAt string `json:"created_at"`
}

// ListCheckpoints returns a project's checkpoint history, newest first.
func (db *DB) ListCheckpoints(projectID string) ([]CheckpointRow, error) {
	rows, err := db.h.Query(`SELECT id,COALESCE(project_id,''),COALESCE(session_id,''),COALESCE(step_id,''),commit_sha,label,created_at
		FROM checkpoints WHERE project_id=? ORDER BY id DESC`, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []CheckpointRow
	for rows.Next() {
		var c CheckpointRow
		if err := rows.Scan(&c.ID, &c.ProjectID, &c.SessionID, &c.StepID, &c.SHA, &c.Label, &c.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// DeleteCheckpointsOlderThan prunes ledger rows beyond keep (SPEC §11 GC).
func (db *DB) DeleteCheckpointsOlderThan(projectID string, keep int) error {
	_, err := db.h.Exec(`DELETE FROM checkpoints WHERE project_id=? AND id NOT IN
		(SELECT id FROM checkpoints WHERE project_id=? ORDER BY id DESC LIMIT ?)`, projectID, projectID, keep)
	return err
}

// --- permissions -----------------------------------------------------------------

// RequestPermission inserts a pending grant request (granted NULL).
func (db *DB) RequestPermission(sessionID, path, mode string) (int64, error) {
	res, err := db.h.Exec(`INSERT INTO permissions(session_id,path,mode,granted,created_at) VALUES(?,?,?,NULL,?)`,
		sessionID, path, mode, nowStamp())
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// ResolvePermission answers a pending request.
func (db *DB) ResolvePermission(id int64, granted bool) error {
	_, err := db.h.Exec(`UPDATE permissions SET granted=? WHERE id=?`, granted, id)
	return err
}

// GrantedPaths lists paths already granted to a session (SPEC §10: grant
// table is session-scoped).
func (db *DB) GrantedPaths(sessionID string) (map[string]string, error) {
	rows, err := db.h.Query(`SELECT path,mode FROM permissions WHERE session_id=? AND granted=1`, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var p, m string
		if err := rows.Scan(&p, &m); err != nil {
			return nil, err
		}
		out[p] = m // rw wins only if last; callers store strongest per path
	}
	return out, rows.Err()
}

// PendingPermissions lists unanswered requests (resume, P2-9).
func (db *DB) PendingPermissions(sessionID string) ([]int64, error) {
	rows, err := db.h.Query(`SELECT id FROM permissions WHERE session_id=? AND granted IS NULL`, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

func nilIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}

var _ = fmt.Sprintf

// GetProjectByWorkspacePath finds the project bound to a workspace folder
// (the CLI auto-creates one per --cwd).
func (db *DB) GetProjectByWorkspacePath(path string) (*Project, error) {
	row := db.h.QueryRow(`SELECT id,name,kind,workspace_path,allow_coding,created_at,settings_json FROM projects WHERE workspace_path=? ORDER BY created_at LIMIT 1`, path)
	var p Project
	var ac int
	if err := row.Scan(&p.ID, &p.Name, &p.Kind, &p.WorkspacePath, &ac, &p.CreatedAt, &p.SettingsJSON); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	p.AllowCoding = ac != 0
	return &p, nil
}

// LatestSession returns the most recently updated non-archived session of a
// project ("" project = any).
func (db *DB) LatestSession(projectID string) (*Session, error) {
	q := `SELECT id,COALESCE(project_id,''),title,COALESCE(persona,''),model_ref,status,pinned,COALESCE(forked_from,''),created_at,updated_at
	      FROM sessions WHERE status != 'archived'`
	var args []any
	if projectID != "" {
		q += ` AND project_id=?`
		args = append(args, projectID)
	}
	q += ` ORDER BY updated_at DESC LIMIT 1`
	row := db.h.QueryRow(q, args...)
	var s Session
	var pin int
	if err := row.Scan(&s.ID, &s.ProjectID, &s.Title, &s.Persona, &s.ModelRef, &s.Status, &pin, &s.ForkedFrom, &s.CreatedAt, &s.UpdatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	s.Pinned = pin != 0
	return &s, nil
}

// DenyAllPending answers every pending permission request with deny
// (boot cleanup after a crash: nobody was there to answer, SPEC §23).
func (db *DB) DenyAllPending() error {
	_, err := db.h.Exec(`UPDATE permissions SET granted=0 WHERE granted IS NULL`)
	return err
}

// FindCheckpoint locates the newest checkpoint row for a session+step.
func (db *DB) FindCheckpoint(projectID, sessionID, stepID string) (*CheckpointRow, error) {
	row := db.h.QueryRow(`SELECT id,COALESCE(project_id,''),COALESCE(session_id,''),COALESCE(step_id,''),commit_sha,label,created_at
		FROM checkpoints WHERE project_id=? AND (?='' OR session_id=?) AND step_id=? ORDER BY id DESC LIMIT 1`,
		projectID, sessionID, sessionID, stepID)
	var c CheckpointRow
	if err := row.Scan(&c.ID, &c.ProjectID, &c.SessionID, &c.StepID, &c.SHA, &c.Label, &c.CreatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &c, nil
}

// ListEventsAfter returns the ledger for a session with id > after
// (WS reconnect replay, SPEC §21).
func (db *DB) ListEventsAfter(sessionID string, after int64) ([]EventRow, error) {
	rows, err := db.h.Query(`SELECT id,COALESCE(step_id,''),type,payload_json,created_at
		FROM events WHERE session_id=? AND id>? ORDER BY id`, sessionID, after)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []EventRow
	for rows.Next() {
		var e EventRow
		if err := rows.Scan(&e.ID, &e.StepID, &e.Type, &e.PayloadJSON, &e.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// ListProjects returns all projects, newest first.
func (db *DB) ListProjects() ([]*Project, error) {
	rows, err := db.h.Query(`SELECT id,name,kind,workspace_path,allow_coding,created_at,settings_json FROM projects ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Project
	for rows.Next() {
		var p Project
		var ac int
		if err := rows.Scan(&p.ID, &p.Name, &p.Kind, &p.WorkspacePath, &ac, &p.CreatedAt, &p.SettingsJSON); err != nil {
			return nil, err
		}
		p.AllowCoding = ac != 0
		out = append(out, &p)
	}
	return out, rows.Err()
}

// DeleteProject removes a project (cascades sessions; transcript dirs are
// swept by the caller).
func (db *DB) DeleteProject(id string) error {
	res, err := db.h.Exec(`DELETE FROM projects WHERE id=?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// SetSessionPinned pins/unpins a session.
func (db *DB) SetSessionPinned(id string, pinned bool) error {
	_, err := db.h.Exec(`UPDATE sessions SET pinned=?, updated_at=? WHERE id=?`, boolInt(pinned), nowStamp(), id)
	return err
}

// SetSessionModel switches the model reference of a session (model
// picker; takes effect on the next turn).
func (db *DB) SetSessionModel(id, ref string) error {
	_, err := db.h.Exec(`UPDATE sessions SET model_ref=?, updated_at=? WHERE id=?`, ref, nowStamp(), id)
	return err
}
