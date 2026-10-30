package resident

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite" // pure Go: the Mac app is built without cgo
)

// SchemaVersion is the database layout this build writes. Older layouts are
// migrated forward on open; a newer one is refused rather than guessed at.
const SchemaVersion = 1

var migrations = []string{
	// 1: initial layout.
	`
CREATE TABLE tasks (
	id TEXT PRIMARY KEY,
	settings TEXT NOT NULL,
	enabled INTEGER NOT NULL,
	version INTEGER NOT NULL,
	created_at INTEGER NOT NULL,
	updated_at INTEGER NOT NULL,
	trusted_at INTEGER,
	deleted_at INTEGER,
	read_through INTEGER NOT NULL DEFAULT 0
);
CREATE TABLE memory (
	task_id TEXT PRIMARY KEY REFERENCES tasks(id),
	version INTEGER NOT NULL,
	summary TEXT NOT NULL,
	state TEXT NOT NULL,
	updated_at INTEGER NOT NULL,
	updated_by TEXT NOT NULL
);
CREATE TABLE messages (
	seq INTEGER PRIMARY KEY AUTOINCREMENT,
	id TEXT NOT NULL UNIQUE,
	task_id TEXT NOT NULL REFERENCES tasks(id),
	role TEXT NOT NULL,
	kind TEXT NOT NULL,
	text TEXT NOT NULL,
	run_id TEXT,
	handled_by TEXT,
	outcome TEXT,
	question_id TEXT,
	created_at INTEGER NOT NULL
);
CREATE INDEX messages_task ON messages(task_id, seq);
CREATE TABLE runs (
	seq INTEGER PRIMARY KEY AUTOINCREMENT,
	id TEXT NOT NULL UNIQUE,
	task_id TEXT NOT NULL REFERENCES tasks(id),
	status TEXT NOT NULL,
	triggers TEXT NOT NULL,
	reason TEXT NOT NULL DEFAULT '',
	interactive INTEGER NOT NULL DEFAULT 0,
	queued_at INTEGER NOT NULL,
	scheduled_for INTEGER,
	started_at INTEGER,
	ended_at INTEGER,
	deadline_at INTEGER,
	outcome TEXT NOT NULL DEFAULT '',
	summary TEXT NOT NULL DEFAULT '',
	report TEXT NOT NULL DEFAULT '',
	error_kind TEXT NOT NULL DEFAULT '',
	error TEXT NOT NULL DEFAULT '',
	settings TEXT,
	settings_version INTEGER NOT NULL DEFAULT 0,
	memory_version INTEGER NOT NULL DEFAULT 0,
	memory_snapshot TEXT,
	provider TEXT NOT NULL DEFAULT '',
	model TEXT NOT NULL DEFAULT '',
	effort TEXT NOT NULL DEFAULT '',
	usage TEXT,
	warnings TEXT,
	notification_key TEXT NOT NULL DEFAULT '',
	notify_decision TEXT NOT NULL DEFAULT '',
	next_wake_at INTEGER,
	checkpoint TEXT NOT NULL DEFAULT ''
);
CREATE INDEX runs_task ON runs(task_id, seq);
CREATE INDEX runs_status ON runs(status, seq);
CREATE TABLE attempts (
	id TEXT PRIMARY KEY,
	run_id TEXT NOT NULL REFERENCES runs(id),
	phase TEXT NOT NULL,
	provider TEXT NOT NULL,
	native_id TEXT NOT NULL DEFAULT '',
	model TEXT NOT NULL DEFAULT '',
	started_at INTEGER NOT NULL,
	ended_at INTEGER,
	error TEXT NOT NULL DEFAULT '',
	raw_result TEXT,
	raw_meta TEXT
);
CREATE INDEX attempts_run ON attempts(run_id);
CREATE TABLE wakeups (
	id TEXT PRIMARY KEY,
	task_id TEXT NOT NULL REFERENCES tasks(id),
	kind TEXT NOT NULL,
	due_at INTEGER NOT NULL,
	status TEXT NOT NULL,
	settings_version INTEGER NOT NULL,
	reason TEXT NOT NULL DEFAULT '',
	run_id TEXT NOT NULL DEFAULT '',
	created_at INTEGER NOT NULL
);
CREATE INDEX wakeups_pending ON wakeups(status, due_at);
CREATE UNIQUE INDEX wakeups_one_pending ON wakeups(task_id, kind) WHERE status = 'pending';
CREATE TABLE questions (
	id TEXT PRIMARY KEY,
	task_id TEXT NOT NULL REFERENCES tasks(id),
	run_id TEXT NOT NULL,
	text TEXT NOT NULL,
	options TEXT NOT NULL,
	status TEXT NOT NULL,
	created_at INTEGER NOT NULL,
	expires_at INTEGER NOT NULL,
	answer TEXT NOT NULL DEFAULT ''
);
CREATE TABLE artifacts (
	id TEXT PRIMARY KEY,
	task_id TEXT NOT NULL REFERENCES tasks(id),
	run_id TEXT NOT NULL,
	path TEXT NOT NULL,
	title TEXT NOT NULL DEFAULT '',
	created_at INTEGER NOT NULL
);
CREATE INDEX artifacts_task ON artifacts(task_id, created_at);
CREATE TABLE notifications (
	seq INTEGER PRIMARY KEY AUTOINCREMENT,
	id TEXT NOT NULL UNIQUE,
	task_id TEXT NOT NULL,
	run_id TEXT NOT NULL,
	kind TEXT NOT NULL,
	title TEXT NOT NULL,
	body TEXT NOT NULL,
	key TEXT NOT NULL DEFAULT '',
	status TEXT NOT NULL,
	reason TEXT NOT NULL DEFAULT '',
	created_at INTEGER NOT NULL,
	not_before INTEGER NOT NULL,
	attempts INTEGER NOT NULL DEFAULT 0,
	next_attempt_at INTEGER NOT NULL,
	sent_at INTEGER,
	last_error TEXT NOT NULL DEFAULT '',
	delivered TEXT NOT NULL DEFAULT '[]'
);
CREATE INDEX notifications_due ON notifications(status, next_attempt_at);
CREATE TABLE notified_keys (
	task_id TEXT NOT NULL,
	key TEXT NOT NULL,
	notified_at INTEGER NOT NULL,
	PRIMARY KEY (task_id, key)
);
CREATE TABLE requests (
	id TEXT PRIMARY KEY,
	kind TEXT NOT NULL,
	fingerprint TEXT NOT NULL,
	result TEXT NOT NULL,
	created_at INTEGER NOT NULL
);
`,
}

// Store is the resident database. One connection serializes every access,
// so transactions never contend; the workload is a single user's.
type Store struct {
	db   *sql.DB
	path string
}

// Open opens (creating with mode 0600) and migrates the database at path.
func Open(path string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	f.Close()
	os.Chmod(path, 0o600)
	dsn := "file:" + path + "?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)&_pragma=synchronous(FULL)&_txlock=immediate"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	db.SetConnMaxIdleTime(0)
	s := &Store{db: db, path: path}
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) Close() error { return s.db.Close() }

func (s *Store) migrate() error {
	var v int
	if err := s.db.QueryRow(`PRAGMA user_version`).Scan(&v); err != nil {
		return err
	}
	if v > len(migrations) {
		return fmt.Errorf("%s has schema version %d, newer than this gateway supports (%d)", s.path, v, len(migrations))
	}
	for i := v; i < len(migrations); i++ {
		tx, err := s.db.Begin()
		if err != nil {
			return err
		}
		if _, err := tx.Exec(migrations[i]); err != nil {
			tx.Rollback()
			return fmt.Errorf("migrate resident db to %d: %w", i+1, err)
		}
		if _, err := tx.Exec(fmt.Sprintf(`PRAGMA user_version = %d`, i+1)); err != nil {
			tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	return nil
}

// tx runs fn in one immediate transaction.
func (s *Store) tx(ctx context.Context, fn func(*sql.Tx) error) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if err := fn(tx); err != nil {
		tx.Rollback()
		return err
	}
	return tx.Commit()
}

// --- encoding helpers ---

func ms(t time.Time) int64 { return t.UTC().UnixMilli() }

func msPtr(t *time.Time) any {
	if t == nil || t.IsZero() {
		return nil
	}
	return ms(*t)
}

func fromMs(v int64) time.Time { return time.UnixMilli(v).UTC() }

func fromNull(v sql.NullInt64) *time.Time {
	if !v.Valid {
		return nil
	}
	t := fromMs(v.Int64)
	return &t
}

func mustJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return string(b)
}

func newID(prefix string) string {
	b := make([]byte, 12)
	rand.Read(b)
	return prefix + "_" + hex.EncodeToString(b)
}

type scanner interface{ Scan(...any) error }

var errNoRows = sql.ErrNoRows

func isNoRows(err error) bool { return errors.Is(err, sql.ErrNoRows) }
