package search

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"strings"
	"sync"
	"time"

	_ "modernc.org/sqlite"

	"github.com/tohutohu/herdr-android-client/gateway/internal/model"
	"github.com/tohutohu/herdr-android-client/gateway/internal/providers"
)

// Source is where the index reads sessions from.
type Source interface {
	// List is the session list: live sessions and recent offline ones.
	List(ctx context.Context) ([]model.Session, error)
	ArchivedIDs() []string
	Conversation(ctx context.Context, id string) (model.Session, []model.Message, error)
}

// Scope selects which sessions a search looks at.
type Scope int

const (
	ScopeAll Scope = iota
	ScopeActive
	ScopeArchived
)

type Query struct {
	// Terms must all be found; see ParseQuery.
	Terms []string
	// Text also looks in the conversations.
	Text  bool
	Scope Scope
	// Limit is the most hits to return; 0 returns all.
	Limit int
}

type Hit struct {
	model.Session
	Match *Match `json:"match,omitempty"`
}

type Result struct {
	// Hits are newest first.
	Hits []Hit
	// More counts the hits left out by Limit.
	More int
	// Indexing means sessions are still being read, so hits may be missing.
	Indexing bool
}

// What a row is to the session list.
const (
	rowGone     = 0 // neither listed nor archived any more
	rowActive   = 1
	rowArchived = 2
)

const (
	fieldSep = '\x1f' // between a block's role and its text
	blockSep = '\x1e' // between blocks, and between metadata fields

	schemaVersion = 1
	// freshFor is how long a finished refresh answers the next searches,
	// such as the ones typed letter by letter.
	freshFor = 2 * time.Second
	// searchWait is how long a search waits for a refresh before answering
	// from what is indexed.
	searchWait = 3 * time.Second
	// retryAfter spaces the reads of a session that could not be read.
	retryAfter = 10 * time.Minute
	readers    = 8
)

type row struct {
	state     int
	updatedAt time.Time
	// retry is when a failed read may be tried again.
	retry time.Time
}

// Index keeps the searchable text of every session in a SQLite file, so a
// search does not read the transcripts again. It is a cache: deleting the
// file only costs the next search a full read.
type Index struct {
	db  *sql.DB
	src Source
	ctx context.Context

	mu      sync.Mutex
	rows    map[string]row
	running chan struct{} // closed when the refresh in progress ends
	fresh   time.Time     // when the last refresh ended
}

// Open opens the index at path. ctx bounds the refreshes it runs.
func Open(ctx context.Context, path string, src Source) (*Index, error) {
	db, err := open(path)
	if err != nil {
		// A cache that cannot be read is rebuilt.
		os.Remove(path)
		os.Remove(path + "-wal")
		os.Remove(path + "-shm")
		if db, err = open(path); err != nil {
			return nil, err
		}
	}
	x := &Index{db: db, src: src, ctx: ctx, rows: map[string]row{}}
	rows, err := db.Query(`SELECT id, state, updated_at FROM sessions`)
	if err != nil {
		db.Close()
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		var state int
		var updated int64
		if err := rows.Scan(&id, &state, &updated); err != nil {
			db.Close()
			return nil, err
		}
		x.rows[id] = row{state: state, updatedAt: time.Unix(0, updated)}
	}
	return x, rows.Err()
}

func open(path string) (*sql.DB, error) {
	db, err := sql.Open("sqlite", "file:"+path+"?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	var v int
	if err := db.QueryRow(`PRAGMA user_version`).Scan(&v); err != nil {
		db.Close()
		return nil, err
	}
	os.Chmod(path, 0o600)
	stmts := []string{`CREATE TABLE IF NOT EXISTS sessions (
		id TEXT PRIMARY KEY,
		state INTEGER NOT NULL,
		updated_at INTEGER NOT NULL,
		session TEXT NOT NULL, -- JSON; empty when the agent no longer has the session
		meta TEXT NOT NULL,
		body TEXT NOT NULL
	)`, `PRAGMA user_version = 1`}
	if v != schemaVersion {
		stmts = append([]string{`DROP TABLE IF EXISTS sessions`}, stmts...)
	}
	for _, stmt := range stmts {
		if _, err := db.Exec(stmt); err != nil {
			db.Close()
			return nil, err
		}
	}
	return db, nil
}

func (x *Index) Close() error { return x.db.Close() }

// Refresh brings the index up to date in the background, unless it just was.
// The returned channel closes when that is done.
func (x *Index) Refresh() <-chan struct{} {
	x.mu.Lock()
	defer x.mu.Unlock()
	if x.running != nil {
		return x.running
	}
	done := make(chan struct{})
	if time.Since(x.fresh) < freshFor {
		close(done)
		return done
	}
	x.running = done
	go func() {
		x.refresh(x.ctx)
		x.mu.Lock()
		x.running, x.fresh = nil, time.Now()
		x.mu.Unlock()
		close(done)
	}()
	return done
}

func (x *Index) refresh(ctx context.Context) {
	active, err := x.src.List(ctx)
	if err != nil {
		slog.Warn("search index: listing sessions failed", "operation", "search_index", "error", err)
		return
	}
	archivedIDs := x.src.ArchivedIDs()
	archived := make(map[string]bool, len(archivedIDs))
	for _, id := range archivedIDs {
		archived[id] = true
	}
	x.mu.Lock()
	known := make(map[string]row, len(x.rows))
	for id, r := range x.rows {
		known[id] = r
	}
	x.mu.Unlock()

	listed := map[string]bool{}
	var read []string // sessions whose conversation has to be read
	for _, s := range active {
		if archived[s.ID] {
			continue
		}
		listed[s.ID] = true
		if r, ok := known[s.ID]; ok && r.state == rowActive && r.updatedAt.Equal(s.UpdatedAt) {
			// Nothing was said since; the status or title may still differ.
			x.put(s.ID, rowActive, s, nil)
			continue
		}
		read = append(read, s.ID)
	}
	now := time.Now()
	// Most recently archived first, so a search during a long first read
	// finds those.
	for _, id := range archivedIDs {
		listed[id] = true
		if r, ok := known[id]; (ok && r.state == rowArchived) || now.Before(r.retry) {
			continue // an archived session no longer changes
		}
		read = append(read, id)
	}
	for id, r := range known {
		if !listed[id] && r.state != rowGone {
			x.setState(id, r, rowGone)
		}
	}
	if len(read) == 0 {
		return
	}
	started := time.Now()
	sem := make(chan struct{}, readers)
	var wg sync.WaitGroup
	for _, id := range read {
		if ctx.Err() != nil {
			break
		}
		state := rowActive
		if archived[id] {
			state = rowArchived
		}
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			s, msgs, err := x.src.Conversation(ctx, id)
			switch {
			case err == nil:
				x.put(id, state, s, &msgs)
			case errors.Is(err, providers.ErrNotFound):
				x.put(id, state, model.Session{}, &msgs)
			case ctx.Err() == nil:
				slog.Debug("search index: session unreadable", "operation", "search_index", "session_id", id, "error", err)
				x.mu.Lock()
				r := x.rows[id]
				r.retry = time.Now().Add(retryAfter)
				x.rows[id] = r
				x.mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if len(read) > 20 {
		slog.Info("search index: sessions read", "operation", "search_index", "sessions", len(read), "duration", time.Since(started).Round(time.Millisecond).String())
	}
}

// put stores a session. With msgs nil the stored conversation is kept. A
// session without an id is one the agent no longer has.
func (x *Index) put(id string, state int, s model.Session, msgs *[]model.Message) {
	var doc, meta string
	if s.ID != "" {
		s.Archived = state == rowArchived
		b, err := json.Marshal(s)
		if err != nil {
			return
		}
		doc = string(b)
		var parts []string
		for _, f := range sessionFields(s) {
			if f.text != "" {
				parts = append(parts, f.text)
			}
		}
		meta = strings.Join(parts, string(blockSep))
	}
	var err error
	if msgs == nil {
		_, err = x.db.Exec(`UPDATE sessions SET state = ?, updated_at = ?, session = ?, meta = ? WHERE id = ?`,
			state, s.UpdatedAt.UnixNano(), doc, meta, id)
	} else {
		var body strings.Builder
		for _, f := range messageFields(*msgs) {
			if body.Len() > 0 {
				body.WriteRune(blockSep)
			}
			body.WriteString(f.name)
			body.WriteRune(fieldSep)
			body.WriteString(f.text)
		}
		_, err = x.db.Exec(`INSERT INTO sessions (id, state, updated_at, session, meta, body) VALUES (?, ?, ?, ?, ?, ?)
			ON CONFLICT(id) DO UPDATE SET state = excluded.state, updated_at = excluded.updated_at,
				session = excluded.session, meta = excluded.meta, body = excluded.body`,
			id, state, s.UpdatedAt.UnixNano(), doc, meta, body.String())
	}
	if err != nil {
		slog.Warn("search index: write failed", "operation", "search_index", "session_id", id, "error", err)
		return
	}
	x.mu.Lock()
	x.rows[id] = row{state: state, updatedAt: s.UpdatedAt}
	x.mu.Unlock()
}

func (x *Index) setState(id string, r row, state int) {
	if _, err := x.db.Exec(`UPDATE sessions SET state = ? WHERE id = ?`, state, id); err != nil {
		slog.Warn("search index: write failed", "operation", "search_index", "session_id", id, "error", err)
		return
	}
	r.state = state
	x.mu.Lock()
	x.rows[id] = r
	x.mu.Unlock()
}

// Search returns the sessions that hold every term of q, newest first.
func (x *Index) Search(ctx context.Context, q Query) (Result, error) {
	var res Result
	if len(q.Terms) == 0 {
		return res, nil
	}
	done := x.Refresh()
	wait := time.NewTimer(searchWait)
	defer wait.Stop()
	select {
	case <-done:
	case <-wait.C:
		res.Indexing = true
	case <-ctx.Done():
		return res, ctx.Err()
	}

	where := []string{`session != ''`}
	var args []any
	switch q.Scope {
	case ScopeActive:
		where = append(where, `state = 1`)
	case ScopeArchived:
		where = append(where, `state = 2`)
	default:
		where = append(where, `state != 0`)
	}
	for _, term := range q.Terms {
		pattern := "%" + likeEscaper.Replace(term) + "%"
		if q.Text {
			where = append(where, `(meta LIKE ? ESCAPE '\' OR body LIKE ? ESCAPE '\')`)
			args = append(args, pattern, pattern)
		} else {
			where = append(where, `meta LIKE ? ESCAPE '\'`)
			args = append(args, pattern)
		}
	}
	rows, err := x.db.QueryContext(ctx, `SELECT session FROM sessions WHERE `+strings.Join(where, " AND ")+` ORDER BY updated_at DESC, id`, args...)
	if err != nil {
		return res, err
	}
	for rows.Next() {
		if q.Limit > 0 && len(res.Hits) == q.Limit {
			res.More++
			continue
		}
		var doc string
		if err := rows.Scan(&doc); err != nil {
			rows.Close()
			return res, err
		}
		var h Hit
		if err := json.Unmarshal([]byte(doc), &h.Session); err != nil {
			continue
		}
		res.Hits = append(res.Hits, h)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return res, err
	}
	for i := range res.Hits {
		res.Hits[i].Match = x.match(ctx, res.Hits[i].Session, q)
	}
	return res, nil
}

var likeEscaper = strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)

// match finds the excerpt for a hit, reading its conversation only when the
// session's own fields do not hold every term.
func (x *Index) match(ctx context.Context, s model.Session, q Query) *Match {
	fields := sessionFields(s)
	if m, ok := matchFields(fields, q.Terms); ok || !q.Text {
		return m
	}
	var body string
	if err := x.db.QueryRowContext(ctx, `SELECT body FROM sessions WHERE id = ?`, s.ID).Scan(&body); err != nil {
		return nil
	}
	for _, block := range strings.Split(body, string(blockSep)) {
		if role, text, ok := strings.Cut(block, string(fieldSep)); ok {
			fields = append(fields, field{role, text, false})
		}
	}
	m, _ := matchFields(fields, q.Terms)
	return m
}
