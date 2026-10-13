package storage

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"
)

// Store is a handle on the SQLite database. One per app run.
type Store struct {
	db *sql.DB
}

const schemaFiles = `
CREATE TABLE IF NOT EXISTS files (
	path TEXT PRIMARY KEY,
	status TEXT NOT NULL DEFAULT 'todo',
	page INTEGER NOT NULL DEFAULT 0,
	total_pages INTEGER NOT NULL DEFAULT 0,
	starred INTEGER NOT NULL DEFAULT 0,
	updated_at TEXT NOT NULL DEFAULT ''
);`

const schemaSettings = `
CREATE TABLE IF NOT EXISTS settings (
	"key" TEXT PRIMARY KEY,
	value TEXT NOT NULL
);`

// dsnParams tunes every connection opened by the pool:
//   - busy_timeout(5000): a contended write waits up to 5s instead of
//     failing immediately with "database is locked" (modernc.org/sqlite
//     applies no busy timeout unless the DSN asks for one).
//   - _txlock=immediate: db.Begin() issues BEGIN IMMEDIATE, taking the
//     write lock up front. That (a) makes the read-modify-write inside
//     each setter a single atomic step and (b) guarantees SQLite invokes
//     the busy handler on contention rather than returning SQLITE_BUSY
//     without waiting (a deferred transaction deadlocks when upgrading).
const dsnParams = "?_pragma=busy_timeout(5000)&_txlock=immediate"

// Open opens (creating if needed) the SQLite database at dbPath and
// creates the tables idempotently. Production callers pass
// %APPDATA%/recorder/recorder.db.
func Open(dbPath string) (*Store, error) {
	if dir := filepath.Dir(dbPath); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, fmt.Errorf("storage: create db dir: %w", err)
		}
	}
	db, err := sql.Open("sqlite", "file:"+dbPath+dsnParams)
	if err != nil {
		return nil, err
	}
	for _, stmt := range []string{schemaFiles, schemaSettings} {
		if _, err := db.Exec(stmt); err != nil {
			_ = db.Close()
			return nil, err
		}
	}
	return &Store{db: db}, nil
}

// Close releases the database handle.
func (s *Store) Close() error {
	return s.db.Close()
}

// now returns the current time in the RFC3339 form stored in updated_at.
func now() string {
	return time.Now().Format(time.RFC3339)
}

// rowQuerier is the subset of *sql.DB and *sql.Tx used to read one record.
type rowQuerier interface {
	QueryRow(query string, args ...any) *sql.Row
}

// loadFile reads one record; a path with no record yields the todo default.
// The caller passes either the Store's db or an open transaction.
func loadFile(q rowQuerier, path string) (FileRec, error) {
	var rec FileRec
	err := q.QueryRow(
		`SELECT path, status, page, total_pages, starred, updated_at
		   FROM files WHERE path = ?`, path,
	).Scan(&rec.Path, &rec.Status, &rec.Page, &rec.TotalPages, &rec.Starred, &rec.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return FileRec{Status: "todo"}, nil
	}
	if err != nil {
		return FileRec{}, err
	}
	return rec, nil
}

// File returns the record for path, or the todo default when none exists.
func (s *Store) File(path string) (FileRec, error) {
	return loadFile(s.db, path)
}

// SetStatus stores status for path (creating the record if needed) and
// returns an error — leaving data unchanged — for anything outside
// todo|reading|done|skip.
func (s *Store) SetStatus(path, status string) error {
	if !validStatuses[status] {
		return fmt.Errorf("storage: invalid status %q", status)
	}
	_, err := s.db.Exec(`
		INSERT INTO files (path, status, page, total_pages, starred, updated_at)
		VALUES (?, ?, 0, 0, 0, ?)
		ON CONFLICT(path) DO UPDATE SET status = excluded.status, updated_at = excluded.updated_at`,
		path, status, now())
	return err
}

// SetStar stores the starred flag for path, creating the record if needed.
func (s *Store) SetStar(path string, starred bool) error {
	_, err := s.db.Exec(`
		INSERT INTO files (path, status, page, total_pages, starred, updated_at)
		VALUES (?, 'todo', 0, 0, ?, ?)
		ON CONFLICT(path) DO UPDATE SET starred = excluded.starred, updated_at = excluded.updated_at`,
		path, starred, now())
	return err
}

// SetProgress stores the current page for path: negative pages clamp to 0
// and, when the stored record has TotalPages > 0, page is capped at it.
// The load and the write run in one transaction so a concurrent
// SetTotalPages cannot slip between them and leave page > total_pages.
func (s *Store) SetProgress(path string, page int) error {
	if page < 0 {
		page = 0
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }() // no-op after Commit

	rec, err := loadFile(tx, path)
	if err != nil {
		return err
	}
	if rec.TotalPages > 0 && page > rec.TotalPages {
		page = rec.TotalPages
	}
	if _, err := tx.Exec(`
		INSERT INTO files (path, status, page, total_pages, starred, updated_at)
		VALUES (?, 'todo', ?, 0, 0, ?)
		ON CONFLICT(path) DO UPDATE SET page = excluded.page, updated_at = excluded.updated_at`,
		path, page, now()); err != nil {
		return err
	}
	return tx.Commit()
}

// SetTotalPages stores the page count for path: negative totals clamp to 0,
// and a stored Page above the new total is linked down to it.
// The load and the write run in one transaction for the same reason as
// SetProgress.
func (s *Store) SetTotalPages(path string, total int) error {
	if total < 0 {
		total = 0
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }() // no-op after Commit

	rec, err := loadFile(tx, path)
	if err != nil {
		return err
	}
	page := rec.Page
	if page > total {
		page = total
	}
	if _, err := tx.Exec(`
		INSERT INTO files (path, status, page, total_pages, starred, updated_at)
		VALUES (?, 'todo', ?, ?, 0, ?)
		ON CONFLICT(path) DO UPDATE SET total_pages = excluded.total_pages,
			page = excluded.page, updated_at = excluded.updated_at`,
		path, page, total, now()); err != nil {
		return err
	}
	return tx.Commit()
}

// ListFiles returns every stored record, ordered by path.
func (s *Store) ListFiles() ([]FileRec, error) {
	rows, err := s.db.Query(
		`SELECT path, status, page, total_pages, starred, updated_at
		   FROM files ORDER BY path`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var out []FileRec
	for rows.Next() {
		var rec FileRec
		if err := rows.Scan(&rec.Path, &rec.Status, &rec.Page, &rec.TotalPages, &rec.Starred, &rec.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, rec)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// SetSetting stores k = v, overwriting any previous value.
func (s *Store) SetSetting(k, v string) error {
	_, err := s.db.Exec(`
		INSERT INTO settings ("key", value) VALUES (?, ?)
		ON CONFLICT("key") DO UPDATE SET value = excluded.value`,
		k, v)
	return err
}

// Setting returns the value for k; ok is false when the key is absent.
func (s *Store) Setting(k string) (string, bool, error) {
	var v string
	err := s.db.QueryRow(`SELECT value FROM settings WHERE "key" = ?`, k).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return v, true, nil
}
