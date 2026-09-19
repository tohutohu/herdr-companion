package opencode

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/tohutohu/herdr-android-client/gateway/internal/providers"
	_ "modernc.org/sqlite"
)

func defaultPath(env, fallback string) string {
	base := os.Getenv(env)
	if base == "" {
		home, _ := os.UserHomeDir()
		base = filepath.Join(home, fallback)
	}
	return filepath.Join(base, "opencode")
}

func defaultDatabase() string {
	path := os.Getenv("OPENCODE_DB")
	if path == "" {
		path = "opencode.db"
	}
	if filepath.IsAbs(path) || path == ":memory:" {
		return path
	}
	return filepath.Join(defaultPath("XDG_DATA_HOME", ".local/share"), path)
}

// Always open the provider's database read-only. In particular, never create or
// migrate it, and do not use immutable=1: active sessions write through WAL.
func (p *Provider) database() (*sql.DB, error) {
	if _, err := os.Stat(p.dbPath); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, providers.ErrNotFound
		}
		return nil, err
	}
	path, err := filepath.Abs(p.dbPath)
	if err != nil {
		return nil, err
	}
	u := url.URL{Scheme: "file", Path: path, RawQuery: "mode=ro&_pragma=busy_timeout(2000)"}
	db, err := sql.Open("sqlite", u.String())
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	return db, nil
}

type record struct {
	ID, Kind string
	Created  int64
	Raw      json.RawMessage
	Parts    []json.RawMessage
}

func records(ctx context.Context, db *sql.DB, id, table string) ([]record, error) {
	var exists int
	if err := db.QueryRowContext(ctx, "SELECT count(*) FROM sqlite_master WHERE type='table' AND name='session_message'").Scan(&exists); err != nil {
		return nil, err
	}
	if exists > 0 {
		rows, err := db.QueryContext(ctx, "SELECT id,type,time_created,data FROM session_message WHERE session_id=? ORDER BY seq", id)
		if err != nil {
			return nil, err
		}
		var out []record
		for rows.Next() {
			var r record
			if err := rows.Scan(&r.ID, &r.Kind, &r.Created, (*[]byte)(&r.Raw)); err != nil {
				rows.Close()
				return nil, err
			}
			out = append(out, r)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, err
		}
		if len(out) > 0 || table == "session_v2" {
			return out, nil
		}
	}
	rows, err := db.QueryContext(ctx, "SELECT id,time_created,data FROM message WHERE session_id=? ORDER BY time_created,id", id)
	if err != nil {
		return nil, err
	}
	var out []record
	for rows.Next() {
		var r record
		if err := rows.Scan(&r.ID, &r.Created, (*[]byte)(&r.Raw)); err != nil {
			rows.Close()
			return nil, err
		}
		out = append(out, r)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	// One query for all parts, avoiding one round trip per message.
	rows, err = db.QueryContext(ctx, "SELECT message_id,data FROM part WHERE session_id=? ORDER BY id", id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	idx := map[string]int{}
	for i, r := range out {
		idx[r.ID] = i
	}
	for rows.Next() {
		var mid string
		var raw []byte
		if err := rows.Scan(&mid, &raw); err != nil {
			return nil, err
		}
		if i, ok := idx[mid]; ok {
			out[i].Parts = append(out[i].Parts, raw)
		}
	}
	return out, rows.Err()
}

func sessionSummary(ctx context.Context, db *sql.DB, id, table string) (*providers.Summary, error) {
	s := &providers.Summary{NativeID: id}
	var updated int64
	err := db.QueryRowContext(ctx, "SELECT directory,coalesce(title,''),time_updated FROM "+table+" WHERE id=?", id).Scan(&s.Cwd, &s.Title, &updated)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, providers.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	s.UpdatedAt = time.UnixMilli(updated).UTC()
	return s, nil
}

func (p *Provider) Recent(ctx context.Context, since time.Time) ([]providers.Summary, error) {
	return p.RecentExcluding(ctx, since, nil)
}
func (p *Provider) RecentExcluding(ctx context.Context, since time.Time, exclude map[string]bool) ([]providers.Summary, error) {
	db, err := p.database()
	if errors.Is(err, providers.ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer db.Close()
	tables, err := sessionTables(ctx, db)
	if err != nil {
		return nil, err
	}
	var out []providers.Summary
	seen := map[string]bool{}
	for _, table := range tables {
		rows, err := db.QueryContext(ctx, "SELECT id,directory,coalesce(title,''),time_updated FROM "+table+" WHERE time_updated>=? AND parent_id IS NULL ORDER BY time_updated DESC LIMIT 100", since.UnixMilli())
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var s providers.Summary
			var updated int64
			if err := rows.Scan(&s.NativeID, &s.Cwd, &s.Title, &updated); err != nil {
				rows.Close()
				return nil, err
			}
			s.UpdatedAt = time.UnixMilli(updated).UTC()
			if !exclude[s.NativeID] && !seen[s.NativeID] {
				out = append(out, s)
				seen[s.NativeID] = true
			}
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, err
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].UpdatedAt.After(out[j].UpdatedAt) })
	if len(out) > 100 {
		out = out[:100]
	}
	return out, nil
}

// v2.0.x and v1 can coexist in one database. Prefer the migrated v2 session
// when both retain the same id. Table names only come from this allowlist.
func sessionTables(ctx context.Context, db *sql.DB) ([]string, error) {
	var out []string
	for _, table := range []string{"session_v2", "session"} {
		var n int
		if err := db.QueryRowContext(ctx, "SELECT count(*) FROM sqlite_master WHERE type='table' AND name=?", table).Scan(&n); err != nil {
			return nil, err
		}
		if n > 0 {
			out = append(out, table)
		}
	}
	return out, nil
}
func findTable(ctx context.Context, db *sql.DB, id string) (string, error) {
	tables, err := sessionTables(ctx, db)
	if err != nil {
		return "", err
	}
	for _, table := range tables {
		var n int
		if err := db.QueryRowContext(ctx, "SELECT count(*) FROM "+table+" WHERE id=?", id).Scan(&n); err != nil {
			return "", err
		}
		if n > 0 {
			return table, nil
		}
	}
	return "", providers.ErrNotFound
}
