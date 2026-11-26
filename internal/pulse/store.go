package pulse

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	_ "modernc.org/sqlite"
)

type Store struct{ DB *sql.DB }

func Open(path string) (*Store, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err = db.ExecContext(ctx, "PRAGMA journal_mode=WAL; PRAGMA busy_timeout=5000; PRAGMA foreign_keys=ON;"); err != nil {
		db.Close()
		return nil, err
	}
	return &Store{db}, nil
}

// Explicit migrate command; startup refuses an uninitialized database.
func (s *Store) Migrate(ctx context.Context) error {
	_, err := s.DB.ExecContext(ctx, schema)
	if err != nil {
		return err
	}
	for _, p := range People {
		if _, err = s.DB.ExecContext(ctx, "INSERT OR IGNORE INTO people VALUES(?,?,?)", p.ID, p.Department, p.MentorID); err != nil {
			return err
		}
	}
	return s.UpgradeHRTasks(ctx)
}

func (s *Store) UpgradeHRTasks(ctx context.Context) error {
	_, err := s.DB.ExecContext(ctx, hrTaskSchema)
	return err
}

const schema = `
CREATE TABLE IF NOT EXISTS schema_version(version INTEGER PRIMARY KEY);
CREATE TABLE IF NOT EXISTS people(id TEXT PRIMARY KEY,department TEXT NOT NULL,mentor_id TEXT NOT NULL);
INSERT OR IGNORE INTO schema_version VALUES(1);
CREATE TABLE IF NOT EXISTS responses (
 event_id TEXT PRIMARY KEY, program_id TEXT NOT NULL, intern_id TEXT NOT NULL,
 month INTEGER NOT NULL CHECK(month BETWEEN 1 AND 6), source TEXT NOT NULL CHECK(source IN ('intern','mentor')),
 hash TEXT NOT NULL, payload TEXT NOT NULL, received_at TEXT NOT NULL,
 UNIQUE(program_id,intern_id,month,source)
);
CREATE TABLE IF NOT EXISTS users(username TEXT PRIMARY KEY, salt TEXT NOT NULL, password_hash TEXT NOT NULL, role TEXT NOT NULL, subject TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS sessions(token_hash TEXT PRIMARY KEY, username TEXT NOT NULL REFERENCES users(username), expires_at INTEGER NOT NULL);
CREATE TABLE IF NOT EXISTS feedback(id TEXT PRIMARY KEY, intern_id TEXT NOT NULL, mentor_id TEXT NOT NULL, month INTEGER NOT NULL CHECK(month BETWEEN 1 AND 6), text TEXT NOT NULL, published_at TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS deliveries(id INTEGER PRIMARY KEY, event_id TEXT NOT NULL, received_at TEXT NOT NULL, status TEXT NOT NULL, source TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS jobs(id TEXT PRIMARY KEY, intern_id TEXT NOT NULL, revision TEXT NOT NULL, state TEXT NOT NULL, attempts INTEGER NOT NULL DEFAULT 0, next_at INTEGER NOT NULL DEFAULT 0, last_error TEXT NOT NULL DEFAULT '', created_at TEXT NOT NULL, UNIQUE(intern_id,revision));
CREATE TABLE IF NOT EXISTS analyses(intern_id TEXT NOT NULL, revision TEXT NOT NULL, payload TEXT NOT NULL, created_at TEXT NOT NULL, PRIMARY KEY(intern_id,revision));
CREATE TABLE IF NOT EXISTS actions(id TEXT PRIMARY KEY, intern_id TEXT NOT NULL, owner TEXT NOT NULL, note TEXT NOT NULL, status TEXT NOT NULL, created_at TEXT NOT NULL, closed_at TEXT);
CREATE INDEX IF NOT EXISTS responses_profile ON responses(intern_id,month,source);
CREATE INDEX IF NOT EXISTS jobs_due ON jobs(state,next_at);
`

func stamp() string { return time.Now().UTC().Format(time.RFC3339Nano) }
func (s *Store) Ready(ctx context.Context) error {
	var n int
	return s.DB.QueryRowContext(ctx, "SELECT version FROM schema_version WHERE version=1").Scan(&n)
}
func (s *Store) Responses(ctx context.Context, id string) ([]Response, error) {
	rows, err := s.DB.QueryContext(ctx, "SELECT payload FROM responses WHERE (?='' OR intern_id=?) ORDER BY intern_id,month,source", id, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Response{}
	for rows.Next() {
		var b string
		var r Response
		if err = rows.Scan(&b); err != nil {
			return nil, err
		}
		if err = json.Unmarshal([]byte(b), &r); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// All rows in an import commit together. Natural-key conflicts never overwrite history.
func (s *Store) Import(ctx context.Context, rs []Response) (int, error) {
	return s.importResponses(ctx, rs, false)
}

// ImportTrusted may register a participant from an authenticated external form.
// Ordinary portal requests and historical imports keep strict registry checks.
func (s *Store) ImportTrusted(ctx context.Context, rs []Response) (int, error) {
	return s.importResponses(ctx, rs, true)
}
func (s *Store) importResponses(ctx context.Context, rs []Response, trusted bool) (int, error) {
	for _, r := range rs {
		if err := r.Validate(); err != nil {
			return 0, fmt.Errorf("%w: %s", ErrValidation, err)
		}
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	count := 0
	for _, r := range rs {
		var department, mentor string
		if trusted {
			if err = autoEnroll(ctx, tx, r); err != nil {
				return 0, err
			}
		}
		if err = tx.QueryRowContext(ctx, "SELECT department,mentor_id FROM people WHERE id=?", r.InternID).Scan(&department, &mentor); errors.Is(err, sql.ErrNoRows) {
			return 0, fmt.Errorf("%w: internId: участник не зарегистрирован", ErrValidation)
		}
		if err != nil {
			return 0, err
		}
		if department != r.Department || (r.Source == "mentor" && r.MentorID != mentor) {
			return 0, fmt.Errorf("%w: подразделение или наставник не соответствуют участнику", ErrValidation)
		}
		var h string
		err = tx.QueryRowContext(ctx, "SELECT hash FROM responses WHERE event_id=?", r.EventID).Scan(&h)
		if err == nil {
			if h != r.Hash() {
				return 0, fmt.Errorf("%w: eventId изменен", ErrConflict)
			}
			continue
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return 0, err
		}
		var existing string
		err = tx.QueryRowContext(ctx, "SELECT event_id FROM responses WHERE program_id=? AND intern_id=? AND month=? AND source=?", r.ProgramID, r.InternID, r.Month, r.Source).Scan(&existing)
		if err == nil {
			return 0, fmt.Errorf("%w: ответ за период уже существует (%s)", ErrConflict, existing)
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return 0, err
		}
		r.ReceivedAt = stamp()
		b, _ := json.Marshal(r)
		if _, err = tx.ExecContext(ctx, "INSERT INTO responses VALUES(?,?,?,?,?,?,?,?)", r.EventID, r.ProgramID, r.InternID, r.Month, r.Source, r.Hash(), string(b), r.ReceivedAt); err != nil {
			return 0, err
		}
		count++
	}
	if err = tx.Commit(); err != nil {
		return 0, err
	}
	return count, nil
}
func (s *Store) People(ctx context.Context) ([]Person, error) {
	rows, err := s.DB.QueryContext(ctx, "SELECT id,department,mentor_id FROM people ORDER BY id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Person{}
	for rows.Next() {
		var p Person
		if err = rows.Scan(&p.ID, &p.Department, &p.MentorID); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}
func (s *Store) Person(ctx context.Context, id string) (Person, error) {
	var p Person
	err := s.DB.QueryRowContext(ctx, "SELECT id,department,mentor_id FROM people WHERE id=?", id).Scan(&p.ID, &p.Department, &p.MentorID)
	return p, err
}
func (s *Store) Allowed(ctx context.Context, who Identity, id string) bool {
	if who.Role == "hr" {
		return true
	}
	if who.Role == "intern" {
		return who.Subject == id
	}
	p, err := s.Person(ctx, id)
	return err == nil && who.Role == "mentor" && who.Subject == p.MentorID
}

type Feedback struct {
	ID          string `json:"id"`
	InternID    string `json:"internId"`
	MentorID    string `json:"mentorId"`
	Month       int    `json:"month"`
	Text        string `json:"text"`
	PublishedAt string `json:"publishedAt"`
}

func (s *Store) Feedback(ctx context.Context, id string) ([]Feedback, error) {
	rows, err := s.DB.QueryContext(ctx, "SELECT id,intern_id,mentor_id,month,text,published_at FROM feedback WHERE intern_id=? ORDER BY month,published_at", id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Feedback{}
	for rows.Next() {
		var f Feedback
		if err = rows.Scan(&f.ID, &f.InternID, &f.MentorID, &f.Month, &f.Text, &f.PublishedAt); err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}
func (s *Store) RecordDelivery(ctx context.Context, event, status, source string) error {
	_, err := s.DB.ExecContext(ctx, "INSERT INTO deliveries(event_id,received_at,status,source) VALUES(?,?,?,?)", event, stamp(), status, source)
	return err
}
func (s *Store) Queue(ctx context.Context, id string) (string, error) {
	rs, err := s.Responses(ctx, id)
	if err != nil {
		return "", err
	}
	if len(rs) == 0 {
		return "", ErrNotFound
	}
	rev := Revision(rs)
	// HTTP headers must be ASCII: participant IDs may contain Cyrillic.
	job := "analysis:" + digest(id+":"+rev)
	_, err = s.DB.ExecContext(ctx, "INSERT OR IGNORE INTO jobs(id,intern_id,revision,state,created_at) VALUES(?,?,?,'pending',?)", job, id, rev, stamp())
	if err != nil {
		return "", err
	}
	err = s.DB.QueryRowContext(ctx, "SELECT id FROM jobs WHERE intern_id=? AND revision=?", id, rev).Scan(&job)
	return job, err
}

// Explicit HR refresh uses a new ASCII request ID, so a result cached by an
// older Python version does not prevent enrichment. Active jobs are reused.
func (s *Store) RequestAnalysis(ctx context.Context, id string) (string, error) {
	job, err := s.Queue(ctx, id)
	if err != nil {
		return "", err
	}
	fresh := "analysis:" + randomID()
	result, err := s.DB.ExecContext(ctx, "UPDATE jobs SET id=?,state='pending',attempts=0,next_at=0,last_error='',created_at=? WHERE id=? AND state IN ('done','failed')", fresh, stamp(), job)
	if err != nil {
		return "", err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return "", err
	}
	if n > 0 {
		return fresh, nil
	}
	return job, nil
}
