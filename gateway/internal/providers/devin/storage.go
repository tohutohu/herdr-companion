package devin

import (
	"context"
	"database/sql"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/tohutohu/herdr-android-client/gateway/internal/providers"
	_ "modernc.org/sqlite"
)

// defaultDatabase follows Devin CLI's XDG data location. The Application
// Support path is kept as a compatibility fallback for older macOS builds.
func defaultDatabase() string {
	if path := os.Getenv("DEVIN_DB"); path != "" {
		return path
	}
	home, _ := os.UserHomeDir()
	dataHome := os.Getenv("XDG_DATA_HOME")
	if dataHome == "" {
		dataHome = filepath.Join(home, ".local", "share")
	}
	primary := filepath.Join(dataHome, "devin", "cli", "sessions.db")
	fallback := filepath.Join(home, "Library", "Application Support", "devin", "cli", "sessions.db")
	if _, err := os.Stat(primary); errors.Is(err, os.ErrNotExist) {
		if _, err := os.Stat(fallback); err == nil {
			return fallback
		}
	}
	return primary
}

// database opens Devin's database read-only. Devin writes through WAL while a
// TUI is running, so immutable=1 would make live history stale.
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
	dsn := url.URL{Scheme: "file", Path: path, RawQuery: "mode=ro&_pragma=busy_timeout(2000)"}
	db, err := sql.Open("sqlite", dsn.String())
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	return db, nil
}

type sessionRow struct {
	ID        string
	Cwd       string
	Model     string
	AgentMode string
	Created   int64
	Updated   int64
	Title     sql.NullString
	MainNode  sql.NullInt64
	Hidden    int64
}

type node struct {
	ID       int64
	Parent   sql.NullInt64
	Raw      string
	Created  int64
	Metadata sql.NullString
}

func readSession(ctx context.Context, db *sql.DB, id string) (sessionRow, error) {
	var row sessionRow
	err := db.QueryRowContext(ctx, `
		SELECT id, working_directory, model, agent_mode, created_at,
		       last_activity_at, title, main_chain_id, hidden
		FROM sessions WHERE id = ?`, id).Scan(
		&row.ID, &row.Cwd, &row.Model, &row.AgentMode, &row.Created,
		&row.Updated, &row.Title, &row.MainNode, &row.Hidden,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return sessionRow{}, providers.ErrNotFound
	}
	return row, err
}

func readNodes(ctx context.Context, db *sql.DB, id string) ([]node, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT node_id, parent_node_id, chat_message, created_at, metadata
		FROM message_nodes WHERE session_id = ?`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []node
	for rows.Next() {
		var n node
		if err := rows.Scan(&n.ID, &n.Parent, &n.Raw, &n.Created, &n.Metadata); err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

// Devin currently stores Unix seconds. Accept milliseconds too so a future
// client migration does not turn every old message into a date in 1970.
func timestamp(value int64) time.Time {
	switch {
	case value > 1e17:
		return time.Unix(0, value).UTC()
	case value > 1e14:
		return time.UnixMicro(value).UTC()
	case value > 1e11:
		return time.UnixMilli(value).UTC()
	default:
		return time.Unix(value, 0).UTC()
	}
}

func timestampValue(ctx context.Context, db *sql.DB, t time.Time) int64 {
	var max sql.NullInt64
	if err := db.QueryRowContext(ctx, "SELECT MAX(last_activity_at) FROM sessions").Scan(&max); err == nil && max.Valid && max.Int64 > 1e11 {
		return t.UnixMilli()
	}
	return t.Unix()
}

func summaryFromRow(row sessionRow) providers.Summary {
	s := providers.Summary{
		NativeID:  row.ID,
		Cwd:       row.Cwd,
		Model:     row.Model,
		Mode:      modeLabel(row.AgentMode),
		UpdatedAt: timestamp(row.Updated),
	}
	if row.Title.Valid {
		s.Title = row.Title.String
	}
	return s
}

func modeLabel(mode string) string {
	switch mode {
	case "normal":
		return "Normal"
	case "accept-edits":
		return "Accept edits"
	case "smart":
		return "Smart"
	case "plan":
		return "Plan"
	case "bypass":
		return "Bypass permissions"
	case "autonomous":
		return "Autonomous"
	default:
		return mode
	}
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
	rows, err := db.QueryContext(ctx, `
		SELECT id, working_directory, model, agent_mode, created_at,
		       last_activity_at, title, main_chain_id, hidden
		FROM sessions
		WHERE last_activity_at >= ? AND hidden = 0
		ORDER BY last_activity_at DESC LIMIT 100`, timestampValue(ctx, db, since))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []providers.Summary
	for rows.Next() {
		var row sessionRow
		if err := rows.Scan(&row.ID, &row.Cwd, &row.Model, &row.AgentMode, &row.Created, &row.Updated, &row.Title, &row.MainNode, &row.Hidden); err != nil {
			return nil, err
		}
		if exclude[row.ID] {
			continue
		}
		out = append(out, summaryFromRow(row))
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].UpdatedAt.After(out[j].UpdatedAt) })
	return out, nil
}

func (p *Provider) LocateLaunched(ctx context.Context, cwd string, since time.Time) string {
	db, err := p.database()
	if err != nil {
		return ""
	}
	defer db.Close()
	rows, err := db.QueryContext(ctx, `
		SELECT id FROM sessions
		WHERE working_directory = ? AND created_at >= ? AND hidden = 0
		ORDER BY created_at DESC LIMIT 1`, cwd, timestampValue(ctx, db, since))
	if err != nil {
		return ""
	}
	defer rows.Close()
	var id string
	if rows.Next() && rows.Scan(&id) == nil {
		return id
	}
	return ""
}
