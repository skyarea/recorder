// Package export implements JSON backup and restore for the reading
// store in the format of the legacy single-file reference app
// (deepseek_html_20261013_7c2d5a.html, exportJSON/importJSON), extended
// with top-level annotations and links arrays. The legacy app ignores
// unknown top-level keys, so its own exports still import here while
// this side never drops annotation data.
package export

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"

	"github.com/skyarea/recorder/internal/storage"
)

// RootNameSetting is the settings key both sides agree on for the
// exported rootName (Task 7 reads it from here).
const RootNameSetting = "root_name"

// legacyVersion is the format version stamped on exports. As in the
// reference app, imports do not check it: presence of "files" is the
// only structural requirement.
const legacyVersion = 1

// document mirrors the reference app's store object field for field,
// plus the two extension arrays. File records are keyed by path.
type document struct {
	Version     int                    `json:"version"`
	RootName    string                 `json:"rootName"`
	Files       map[string]*fileRecord `json:"files"`
	Annotations []annotationRecord     `json:"annotations"`
	Links       []linkRecord           `json:"links"`
}

type fileRecord struct {
	Status     string       `json:"status"`
	Page       int          `json:"page"`
	TotalPages int          `json:"totalPages"`
	Starred    bool         `json:"starred"`
	Notes      []noteRecord `json:"notes"`
	UpdatedAt  string       `json:"updatedAt"`
}

type noteRecord struct {
	ID   string `json:"id"`
	Text string `json:"text"`
	Page *int   `json:"page"` // null = document-level note, as in the reference
	TS   string `json:"ts"`
}

type annotationRecord struct {
	ID        string `json:"id"`
	Path      string `json:"path"`
	Page      int    `json:"page"`
	Kind      string `json:"kind"`
	Data      string `json:"data"`
	CreatedAt string `json:"createdAt"`
}

type linkRecord struct {
	NoteID       string `json:"noteId"`
	AnnotationID string `json:"annotationId"`
}

// ExportJSON renders the whole store as legacy-format JSON with two
// space indentation, like the reference exportJSON (JSON.stringify(store,
// null, 2)). HTML metacharacters stay unescaped, as JSON.stringify
// emits them. rootName is stamped verbatim; every id, ts, created_at
// and updated_at round-trips unchanged.
func ExportJSON(s *storage.Store, rootName string) ([]byte, error) {
	d, err := s.Dump()
	if err != nil {
		return nil, fmt.Errorf("export: dump store: %w", err)
	}

	doc := document{
		Version:     legacyVersion,
		RootName:    rootName,
		Files:       make(map[string]*fileRecord, len(d.Files)),
		Annotations: make([]annotationRecord, 0, len(d.Annotations)),
		Links:       make([]linkRecord, 0, len(d.Links)),
	}

	// Group notes by file; Dump orders them by (file_path, ts, rowid),
	// which is the order ListNotes would return for each file.
	notesByPath := make(map[string][]noteRecord)
	for _, n := range d.Notes {
		notesByPath[n.FilePath] = append(notesByPath[n.FilePath], noteRecord{
			ID: n.ID, Text: n.Text, Page: n.Page, TS: n.TS,
		})
	}

	for _, f := range d.Files {
		rec := &fileRecord{
			Status:     f.Status,
			Page:       f.Page,
			TotalPages: f.TotalPages,
			Starred:    f.Starred,
			Notes:      notesByPath[f.Path],
			UpdatedAt:  f.UpdatedAt,
		}
		if rec.Notes == nil {
			rec.Notes = []noteRecord{} // always "notes": [], never null
		}
		doc.Files[f.Path] = rec
	}
	// Notes of paths without a file record (possible: adding a note
	// never required one) still need a record to live in the legacy
	// shape, so synthesise the reference's ensureRec default.
	for _, n := range d.Notes {
		if _, ok := doc.Files[n.FilePath]; !ok {
			doc.Files[n.FilePath] = &fileRecord{Status: "todo", Notes: notesByPath[n.FilePath]}
		}
	}

	for _, a := range d.Annotations {
		doc.Annotations = append(doc.Annotations, annotationRecord{
			ID: a.ID, Path: a.FilePath, Page: a.Page,
			Kind: a.Kind, Data: a.Data, CreatedAt: a.CreatedAt,
		})
	}
	for _, l := range d.Links {
		doc.Links = append(doc.Links, linkRecord{NoteID: l[0], AnnotationID: l[1]})
	}

	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false) // match JSON.stringify byte-for-byte
	enc.SetIndent("", "  ")
	if err := enc.Encode(doc); err != nil {
		return nil, fmt.Errorf("export: encode JSON: %w", err)
	}
	return buf.Bytes(), nil
}

// ImportJSON restores data into s. The input is parsed and structurally
// validated fully in memory first; only then is the store written, in a
// single transaction, so any error leaves the store untouched.
//
// replace=true replaces every file record wholesale (as the reference:
// store.files = data.files) and sets rootName when present; notes and
// annotations of every path come from the input alone. replace=false
// merges per path: the incoming record wins only when its updatedAt
// string is strictly greater than the stored one (the reference's
// string comparison), and a winning path replaces that path's notes and
// annotations wholesale — the reference replaces the whole record
// object, notes included. Paths whose record loses keep everything.
//
// The returned count is the number of file records taken from the
// input: all of them when replace, only the winning paths when merging.
func ImportJSON(s *storage.Store, data []byte, replace bool) (int, error) {
	var doc document
	if err := json.Unmarshal(data, &doc); err != nil {
		return 0, fmt.Errorf("export: parse import JSON: %w", err)
	}
	// The reference rejects a missing/falsy "files"; a Go nil map
	// covers both a missing key and an explicit null. An empty object
	// ("files": {}) stays valid and means "no records".
	if doc.Files == nil {
		return 0, fmt.Errorf(`export: import JSON has no "files" object`)
	}

	// Pre-validated in memory: build the full desired snapshot...
	next, imported, err := mergedDump(s, &doc, replace)
	if err != nil {
		return 0, fmt.Errorf("export: build import: %w", err)
	}
	// ...and write it in one transaction. RestoreBatch is all-or-
	// nothing, so a mid-write failure also leaves the store untouched.
	if err := s.RestoreBatch(next); err != nil {
		return 0, fmt.Errorf("export: restore store: %w", err)
	}
	return imported, nil
}

// mergedDump computes the snapshot ImportJSON will write, without
// touching the store. replace takes the input wholesale; otherwise it
// applies the per-path newer-wins merge against the current dump.
func mergedDump(s *storage.Store, doc *document, replace bool) (storage.Dump, int, error) {
	cur, err := s.Dump()
	if err != nil {
		return storage.Dump{}, 0, err
	}

	next := storage.Dump{
		Files:       []storage.FileRec{},
		Notes:       []storage.Note{},
		Annotations: []storage.Annotation{},
		Links:       [][2]string{},
		Settings:    cur.Settings, // settings survive; root_name may be overwritten below
	}

	// Index the incoming payload by path.
	notesByPath := make(map[string][]storage.Note)
	for path, f := range doc.Files {
		for _, n := range f.Notes {
			notesByPath[path] = append(notesByPath[path], storage.Note{
				ID: n.ID, FilePath: path, Text: n.Text, Page: n.Page, TS: n.TS, AnnIDs: []string{},
			})
		}
	}
	annsByPath := make(map[string][]storage.Annotation)
	for _, a := range doc.Annotations {
		annsByPath[a.Path] = append(annsByPath[a.Path], storage.Annotation{
			ID: a.ID, FilePath: a.Path, Page: a.Page, Kind: a.Kind, Data: a.Data, CreatedAt: a.CreatedAt,
		})
	}

	paths := make([]string, 0, len(doc.Files))
	for path := range doc.Files {
		paths = append(paths, path)
	}
	sort.Strings(paths) // deterministic output regardless of map order

	if replace {
		// Wholesale: every file record and its embedded notes come from
		// the input; a legacy file without annotations/links restores
		// as empty, per the reference's store.files = data.files.
		for _, path := range paths {
			f := doc.Files[path]
			next.Files = append(next.Files, storage.FileRec{
				Path: path, Status: f.Status, Page: f.Page,
				TotalPages: f.TotalPages, Starred: f.Starred, UpdatedAt: f.UpdatedAt,
			})
			next.Notes = append(next.Notes, notesByPath[path]...)
		}
		// Annotations are keyed by their own path, which may have no
		// file record at all; take them from the flat list.
		for _, a := range doc.Annotations {
			next.Annotations = append(next.Annotations, storage.Annotation{
				ID: a.ID, FilePath: a.Path, Page: a.Page, Kind: a.Kind, Data: a.Data, CreatedAt: a.CreatedAt,
			})
		}
		for _, l := range doc.Links {
			next.Links = append(next.Links, [2]string{l.NoteID, l.AnnotationID})
		}
		// The reference only assigns rootName on replace, and only when
		// present; the store keeps its old value otherwise.
		if doc.RootName != "" {
			next.Settings[RootNameSetting] = doc.RootName
		}
		return next, len(doc.Files), nil
	}

	// Merge: a path's data is taken from the input only when the
	// incoming record's updatedAt string is strictly greater than the
	// stored one (the reference's comparison); everything else is
	// carried over untouched.
	curFiles := make(map[string]storage.FileRec, len(cur.Files))
	for _, f := range cur.Files {
		curFiles[f.Path] = f
	}
	curNotesByPath := make(map[string][]storage.Note)
	for _, n := range cur.Notes {
		curNotesByPath[n.FilePath] = append(curNotesByPath[n.FilePath], n)
	}
	curAnnsByPath := make(map[string][]storage.Annotation)
	for _, a := range cur.Annotations {
		curAnnsByPath[a.FilePath] = append(curAnnsByPath[a.FilePath], a)
	}

	imported := 0
	winning := make(map[string]bool)
	droppedNoteIDs := map[string]bool{}
	droppedAnnIDs := map[string]bool{}
	for _, path := range paths {
		f := doc.Files[path]
		curRec, exists := curFiles[path]
		if exists && f.UpdatedAt <= curRec.UpdatedAt {
			continue // incoming loses: stored record and its data stay
		}
		imported++
		winning[path] = true
		next.Files = append(next.Files, storage.FileRec{
			Path: path, Status: f.Status, Page: f.Page,
			TotalPages: f.TotalPages, Starred: f.Starred, UpdatedAt: f.UpdatedAt,
		})
		// The reference replaces the whole record object, notes
		// included: the path's stored notes and annotations give way to
		// the incoming ones.
		for _, n := range curNotesByPath[path] {
			droppedNoteIDs[n.ID] = true
		}
		for _, a := range curAnnsByPath[path] {
			droppedAnnIDs[a.ID] = true
		}
		next.Notes = append(next.Notes, notesByPath[path]...)
		next.Annotations = append(next.Annotations, annsByPath[path]...)
	}

	// Carry over what the winners did not replace.
	for _, f := range cur.Files {
		if !winning[f.Path] {
			next.Files = append(next.Files, f)
		}
	}
	for _, n := range cur.Notes {
		if !winning[n.FilePath] {
			next.Notes = append(next.Notes, n)
		}
	}
	for _, a := range cur.Annotations {
		if !winning[a.FilePath] {
			next.Annotations = append(next.Annotations, a)
		}
	}
	// Stored links survive unless they pointed at a replaced note or
	// annotation; winning paths contribute the incoming links of their
	// notes.
	for _, l := range cur.Links {
		if !droppedNoteIDs[l[0]] && !droppedAnnIDs[l[1]] {
			next.Links = append(next.Links, l)
		}
	}
	for _, path := range paths {
		if !winning[path] {
			continue
		}
		noteIDs := make(map[string]bool, len(notesByPath[path]))
		for _, n := range notesByPath[path] {
			noteIDs[n.ID] = true
		}
		for _, l := range doc.Links {
			if noteIDs[l.NoteID] {
				next.Links = append(next.Links, [2]string{l.NoteID, l.AnnotationID})
			}
		}
	}

	// Merge never touches rootName (the reference only assigns it on
	// replace).
	return next, imported, nil
}
