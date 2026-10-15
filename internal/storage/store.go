package storage

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
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

const schemaNotes = `
CREATE TABLE IF NOT EXISTS notes (
	id TEXT PRIMARY KEY,
	file_path TEXT NOT NULL,
	text TEXT NOT NULL,
	page INTEGER,
	ts TEXT NOT NULL
);`

const schemaAnnotations = `
CREATE TABLE IF NOT EXISTS annotations (
	id TEXT PRIMARY KEY,
	file_path TEXT NOT NULL,
	page INTEGER NOT NULL,
	kind TEXT NOT NULL,
	data TEXT NOT NULL,
	created_at TEXT NOT NULL
);`

// schemaNoteAnnotations is the M:N join: composite primary key so the
// same (note, annotation) pair can be linked only once. Deletion of a
// parent cascades its rows away in DeleteNote/DeleteAnnotation (one
// transaction, in code — SQLite enforces no foreign keys here because
// the driver runs without PRAGMA foreign_keys).
const schemaNoteAnnotations = `
CREATE TABLE IF NOT EXISTS note_annotations (
	note_id TEXT NOT NULL,
	annotation_id TEXT NOT NULL,
	PRIMARY KEY (note_id, annotation_id)
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
		return nil, fmt.Errorf("storage: open database: %w", err)
	}

	// SQLite 是进程内的单文件数据库，不存在多连接带来的并发收益，
	// 反而引入真实的失败模式：读写各占一条连接时，非 WAL 的 journal
	// 模式下写方的 BEGIN IMMEDIATE 要等读方释放共享锁，一旦在慢盘 /
	// 抢资源的 runner 上超过 busy_timeout(5000) 就报 SQLITE_BUSY。
	// CI 已经两次因此挂掉（4f2f391、d5b49a1）。
	// 把连接池压成一条：读写全部串行经过同一连接，跨连接的锁竞争
	// 从机制上消失。每条 setter 的 BEGIN IMMEDIATE 仍能保住事务级原子性，
	// 因为并发发生在 Go 的 goroutine 层，而不是数据库层。
	db.SetMaxOpenConns(1)

	for _, stmt := range []string{schemaFiles, schemaSettings, schemaNotes, schemaAnnotations, schemaNoteAnnotations} {
		if _, err := db.Exec(stmt); err != nil {
			_ = db.Close()
			return nil, fmt.Errorf("storage: create schema: %w", err)
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

// newID returns a short random hex id (8 bytes → 16 hex chars), generated
// server-side by every Add* method. The Go code base has no prior id
// scheme to reuse.
func newID() string {
	var b [8]byte
	_, _ = rand.Read(b[:]) // crypto/rand.Read never fails on Go ≥ 1.24
	return hex.EncodeToString(b[:])
}

// pageArg renders a *int column value for the driver: nil becomes SQL NULL.
func pageArg(p *int) any {
	if p == nil {
		return nil
	}
	return *p
}

// AddNote inserts a note for filePath and returns the stored record with
// its backend-generated id and RFC3339 ts. A nil page pins the note to no
// page; the returned note's AnnIDs is empty (links come from ListNotes).
func (s *Store) AddNote(filePath, text string, page *int) (Note, error) {
	n := Note{ID: newID(), FilePath: filePath, Text: text, TS: now(), AnnIDs: []string{}}
	if page != nil {
		p := *page // copy: the returned Note must not alias the caller's variable
		n.Page = &p
	}
	_, err := s.db.Exec(`
		INSERT INTO notes (id, file_path, text, page, ts)
		VALUES (?, ?, ?, ?, ?)`,
		n.ID, n.FilePath, n.Text, pageArg(page), n.TS)
	if err != nil {
		return Note{}, err
	}
	return n, nil
}

// UpdateNote replaces the text of the note with id. An unknown id is a
// no-op (legacy reference behaviour: find-then-write never fails on a
// missing note), matching this package's lenient missing-row idiom.
func (s *Store) UpdateNote(id, text string) error {
	_, err := s.db.Exec(`UPDATE notes SET text = ? WHERE id = ?`, text, id)
	return err
}

// DeleteNote removes the note with id and, in the same transaction, every
// note_annotations row pointing at it. An unknown id is a no-op.
func (s *Store) DeleteNote(id string) error {
	return s.cascadeDelete(
		`DELETE FROM notes WHERE id = ?`,
		`DELETE FROM note_annotations WHERE note_id = ?`, id)
}

// cascadeDelete removes the link rows named by linkQuery first and then
// the parent row named by parentQuery, both in one transaction, so the
// two statements commit atomically.
func (s *Store) cascadeDelete(parentQuery, linkQuery, id string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }() // no-op after Commit

	if _, err := tx.Exec(linkQuery, id); err != nil {
		return err
	}
	if _, err := tx.Exec(parentQuery, id); err != nil {
		return err
	}
	return tx.Commit()
}

// scanNotes drains and closes rows into notes, giving each an empty
// (non-nil) AnnIDs slice ready to be filled by the caller.
func scanNotes(rows *sql.Rows) ([]Note, error) {
	defer func() { _ = rows.Close() }()

	var out []Note
	for rows.Next() {
		var n Note
		var page sql.NullInt64
		if err := rows.Scan(&n.ID, &n.FilePath, &n.Text, &page, &n.TS); err != nil {
			return nil, err
		}
		if page.Valid {
			p := int(page.Int64)
			n.Page = &p
		}
		n.AnnIDs = []string{}
		out = append(out, n)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// loadAnnIDs returns noteID → linked annotation ids (in link creation
// order) for every note of filePath, in one query.
func (s *Store) loadAnnIDs(filePath string) (map[string][]string, error) {
	rows, err := s.db.Query(`
		SELECT na.note_id, na.annotation_id
		  FROM note_annotations AS na
		  JOIN notes AS n ON n.id = na.note_id
		 WHERE n.file_path = ?
		 ORDER BY na.rowid`, filePath)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	out := map[string][]string{}
	for rows.Next() {
		var noteID, annID string
		if err := rows.Scan(&noteID, &annID); err != nil {
			return nil, err
		}
		out[noteID] = append(out[noteID], annID)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// ListNotes returns the notes of filePath ordered by ts ascending (ties
// fall back to insertion order: RFC3339 has a 1s granularity, and the
// legacy export kept same-second notes in insertion order), each with
// AnnIDs populated from the link table.
func (s *Store) ListNotes(filePath string) ([]Note, error) {
	rows, err := s.db.Query(`
		SELECT id, file_path, text, page, ts
		  FROM notes WHERE file_path = ?
		 ORDER BY ts, rowid`, filePath)
	if err != nil {
		return nil, err
	}
	// The note rows are fully drained and closed before loadAnnIDs opens
	// its own query, so no connection is held across the two.
	out, err := scanNotes(rows)
	if err != nil {
		return nil, err
	}

	annIDs, err := s.loadAnnIDs(filePath)
	if err != nil {
		return nil, err
	}
	for i := range out {
		if ids := annIDs[out[i].ID]; ids != nil {
			out[i].AnnIDs = ids
		}
	}
	return out, nil
}

// AddAnnotation inserts one highlight|line|box annotation for filePath
// and returns the stored record with a backend-generated id and RFC3339
// created_at. data (the normalised geometry JSON) is stored verbatim —
// it is deliberately neither parsed nor validated here. A kind outside
// highlight|line|box returns an error before anything is written.
func (s *Store) AddAnnotation(filePath string, page int, kind, data string) (Annotation, error) {
	if !validAnnKinds[kind] {
		return Annotation{}, fmt.Errorf("storage: invalid annotation kind %q", kind)
	}
	a := Annotation{
		ID:        newID(),
		FilePath:  filePath,
		Page:      page,
		Kind:      kind,
		Data:      data,
		CreatedAt: now(),
	}
	_, err := s.db.Exec(`
		INSERT INTO annotations (id, file_path, page, kind, data, created_at)
		VALUES (?, ?, ?, ?, ?, ?)`,
		a.ID, a.FilePath, a.Page, a.Kind, a.Data, a.CreatedAt)
	if err != nil {
		return Annotation{}, err
	}
	return a, nil
}

// DeleteAnnotation removes the annotation with id and, in the same
// transaction, every note_annotations row pointing at it. An unknown id
// is a no-op. The note rows themselves are never touched.
func (s *Store) DeleteAnnotation(id string) error {
	return s.cascadeDelete(
		`DELETE FROM annotations WHERE id = ?`,
		`DELETE FROM note_annotations WHERE annotation_id = ?`, id)
}

// ListAnnotations returns every annotation of filePath, oldest first
// (ties fall back to insertion order, as in ListNotes).
func (s *Store) ListAnnotations(filePath string) ([]Annotation, error) {
	rows, err := s.db.Query(`
		SELECT id, file_path, page, kind, data, created_at
		  FROM annotations WHERE file_path = ?
		 ORDER BY created_at, rowid`, filePath)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var out []Annotation
	for rows.Next() {
		var a Annotation
		if err := rows.Scan(&a.ID, &a.FilePath, &a.Page, &a.Kind, &a.Data, &a.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// LinkNote connects a note to an annotation. It is idempotent: linking
// the same pair twice keeps the single row (composite primary key +
// ON CONFLICT DO NOTHING) and returns no error.
func (s *Store) LinkNote(noteID, annID string) error {
	_, err := s.db.Exec(`
		INSERT INTO note_annotations (note_id, annotation_id) VALUES (?, ?)
		ON CONFLICT(note_id, annotation_id) DO NOTHING`,
		noteID, annID)
	return err
}

// UnlinkNote disconnects a note from an annotation; a pair that is not
// linked is a no-op.
func (s *Store) UnlinkNote(noteID, annID string) error {
	_, err := s.db.Exec(`
		DELETE FROM note_annotations
		 WHERE note_id = ? AND annotation_id = ?`,
		noteID, annID)
	return err
}
