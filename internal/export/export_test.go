package export

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"reflect"
	"slices"
	"testing"

	"github.com/skyarea/recorder/internal/storage"
)

// newTestStore opens a Store on a fresh database file inside the test's
// temp directory.
func newTestStore(t *testing.T) *storage.Store {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "recorder.db")
	s, err := storage.Open(dbPath)
	if err != nil {
		t.Fatalf("Open(%q): %v", dbPath, err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

// rawDoc unmarshals exported bytes into generic JSON for shape checks.
func rawDoc(t *testing.T, data []byte) map[string]any {
	t.Helper()
	var doc map[string]any
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("exported bytes are not valid JSON: %v\n%s", err, data)
	}
	return doc
}

// wantKeys asserts that obj has exactly the given keys (order-free).
func wantKeys(t *testing.T, what string, obj map[string]any, want []string) {
	t.Helper()
	got := make([]string, 0, len(obj))
	for k := range obj {
		got = append(got, k)
	}
	slices.Sort(got)
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Errorf("%s keys = %v, want exactly %v", what, got, want)
	}
}

// noteOf finds the note object with id inside arr.
func noteOf(t *testing.T, arr []any, id string) map[string]any {
	t.Helper()
	for _, e := range arr {
		m, ok := e.(map[string]any)
		if !ok {
			t.Fatalf("notes entry is %T, want object", e)
		}
		if m["id"] == id {
			return m
		}
	}
	t.Fatalf("note %q missing from %v", id, arr)
	return nil
}

// mustAddNote adds a note or fails the test.
func mustAddNote(t *testing.T, s *storage.Store, path, text string, page *int) storage.Note {
	t.Helper()
	n, err := s.AddNote(path, text, page)
	if err != nil {
		t.Fatalf("AddNote(%q): %v", text, err)
	}
	return n
}

// mustAddAnn adds an annotation or fails the test.
func mustAddAnn(t *testing.T, s *storage.Store, path string, page int, kind, data string) storage.Annotation {
	t.Helper()
	a, err := s.AddAnnotation(path, page, kind, data)
	if err != nil {
		t.Fatalf("AddAnnotation(%q): %v", kind, err)
	}
	return a
}

// TestExportMatchesLegacyShape pins the exported JSON to the reference
// app's exportJSON output: top level {version, rootName, files}, file
// records {status, page, totalPages, starred, notes, updatedAt}, notes
// {id, text, page, ts} — plus the extended top-level annotations and
// links arrays whose element shapes are fixed by the controller ruling.
func TestExportMatchesLegacyShape(t *testing.T) {
	s := newTestStore(t)
	const p1 = "C:/books/a.pdf"
	const p2 = "C:/books/b.pdf"

	if err := s.SetStatus(p1, "reading"); err != nil {
		t.Fatalf("SetStatus: %v", err)
	}
	if err := s.SetStar(p1, true); err != nil {
		t.Fatalf("SetStar: %v", err)
	}
	if err := s.SetTotalPages(p1, 10); err != nil {
		t.Fatalf("SetTotalPages(10): %v", err)
	}
	if err := s.SetProgress(p1, 4); err != nil {
		t.Fatalf("SetProgress(4): %v", err)
	}
	page := 3
	// Text with HTML metacharacters: the legacy app emits them raw
	// (JSON.stringify does not escape HTML), so must we.
	n1 := mustAddNote(t, s, p1, "a < b & c", &page)
	n2 := mustAddNote(t, s, p1, "doc level", nil)
	ann := mustAddAnn(t, s, p1, 2, "highlight", `{"rect":[0.1,0.2,0.3,0.4]}`)
	if err := s.LinkNote(n1.ID, ann.ID); err != nil {
		t.Fatalf("LinkNote: %v", err)
	}
	if err := s.SetStatus(p2, "done"); err != nil {
		t.Fatalf("SetStatus(p2): %v", err)
	}

	rec1, err := s.File(p1)
	if err != nil {
		t.Fatalf("File(p1): %v", err)
	}
	data, err := ExportJSON(s, "books")
	if err != nil {
		t.Fatalf("ExportJSON: %v", err)
	}

	// Pristine output: raw < and & exactly as JSON.stringify would emit
	// them, never the Go default < / & escapes.
	if !bytes.Contains(data, []byte("a < b & c")) {
		t.Errorf("note text not emitted verbatim (HTML-escaped?):\n%s", data)
	}
	if bytes.Contains(data, []byte("\\u003c")) || bytes.Contains(data, []byte("\\u0026")) {
		t.Errorf("output contains HTML escapes, want pristine characters:\n%s", data)
	}

	doc := rawDoc(t, data)
	wantKeys(t, "top level", doc, []string{"version", "rootName", "files", "annotations", "links"})
	if doc["version"] != float64(1) {
		t.Errorf("version = %v, want 1", doc["version"])
	}
	if doc["rootName"] != "books" {
		t.Errorf("rootName = %v, want \"books\"", doc["rootName"])
	}

	files, ok := doc["files"].(map[string]any)
	if !ok {
		t.Fatalf("files is %T, want object", doc["files"])
	}
	if len(files) != 2 {
		t.Fatalf("files has %d entries, want 2", len(files))
	}

	f1, ok := files[p1].(map[string]any)
	if !ok {
		t.Fatalf("files[%q] is %T, want object", p1, files[p1])
	}
	wantKeys(t, "file record", f1, []string{"status", "page", "totalPages", "starred", "notes", "updatedAt"})
	if f1["status"] != "reading" || f1["page"] != float64(4) || f1["totalPages"] != float64(10) || f1["starred"] != true {
		t.Errorf("file record = %v, want status=reading page=4 totalPages=10 starred=true", f1)
	}
	if f1["updatedAt"] != rec1.UpdatedAt {
		t.Errorf("updatedAt = %v, want %q (verbatim)", f1["updatedAt"], rec1.UpdatedAt)
	}

	notes, ok := f1["notes"].([]any)
	if !ok {
		t.Fatalf("notes is %T, want array", f1["notes"])
	}
	if len(notes) != 2 {
		t.Fatalf("notes has %d entries, want 2", len(notes))
	}
	gotN1 := noteOf(t, notes, n1.ID)
	wantKeys(t, "note", gotN1, []string{"id", "text", "page", "ts"})
	if gotN1["text"] != "a < b & c" || gotN1["page"] != float64(3) || gotN1["ts"] != n1.TS {
		t.Errorf("note = %v, want text=%q page=3 ts=%q", gotN1, "a < b & c", n1.TS)
	}
	gotN2 := noteOf(t, notes, n2.ID)
	if gotN2["page"] != nil {
		t.Errorf("page-less note page = %v, want null", gotN2["page"])
	}
	if gotN2["text"] != "doc level" || gotN2["ts"] != n2.TS {
		t.Errorf("note = %v, want text=%q ts=%q", gotN2, "doc level", n2.TS)
	}

	// p2 is a bare record: notes must be [] (the reference always has
	// the key), not null and not missing.
	f2, ok := files[p2].(map[string]any)
	if !ok {
		t.Fatalf("files[%q] is %T, want object", p2, files[p2])
	}
	wantKeys(t, "file record p2", f2, []string{"status", "page", "totalPages", "starred", "notes", "updatedAt"})
	if arr, ok := f2["notes"].([]any); !ok || len(arr) != 0 {
		t.Errorf("bare record notes = %v, want empty array", f2["notes"])
	}

	// The extensions: annotations and links with the exact element keys.
	anns, ok := doc["annotations"].([]any)
	if !ok || len(anns) != 1 {
		t.Fatalf("annotations = %v, want one-element array", doc["annotations"])
	}
	gotAnn, ok := anns[0].(map[string]any)
	if !ok {
		t.Fatalf("annotation entry is %T, want object", anns[0])
	}
	wantKeys(t, "annotation", gotAnn, []string{"id", "path", "page", "kind", "data", "createdAt"})
	if gotAnn["id"] != ann.ID || gotAnn["path"] != p1 || gotAnn["page"] != float64(2) ||
		gotAnn["kind"] != "highlight" || gotAnn["data"] != ann.Data || gotAnn["createdAt"] != ann.CreatedAt {
		t.Errorf("annotation = %v, want verbatim %+v", gotAnn, ann)
	}

	links, ok := doc["links"].([]any)
	if !ok || len(links) != 1 {
		t.Fatalf("links = %v, want one-element array", doc["links"])
	}
	gotLink, ok := links[0].(map[string]any)
	if !ok {
		t.Fatalf("link entry is %T, want object", links[0])
	}
	wantKeys(t, "link", gotLink, []string{"noteId", "annotationId"})
	if gotLink["noteId"] != n1.ID || gotLink["annotationId"] != ann.ID {
		t.Errorf("link = %v, want {%s %s}", gotLink, n1.ID, ann.ID)
	}
}

// TestImportRoundTrip exports a fully-populated store, imports it into a
// fresh one with replace=true and asserts byte-identical re-export:
// every id, timestamp and record survives verbatim.
func TestImportRoundTrip(t *testing.T) {
	src := newTestStore(t)
	const p1 = "C:/books/a.pdf"
	const p2 = "C:/books/b.pdf"
	const p3 = "C:/books/ann-only.pdf" // annotation with no file record

	if err := src.SetStatus(p1, "reading"); err != nil {
		t.Fatalf("SetStatus(p1): %v", err)
	}
	if err := src.SetStar(p1, true); err != nil {
		t.Fatalf("SetStar(p1): %v", err)
	}
	if err := src.SetTotalPages(p1, 12); err != nil {
		t.Fatalf("SetTotalPages: %v", err)
	}
	if err := src.SetProgress(p1, 5); err != nil {
		t.Fatalf("SetProgress: %v", err)
	}
	page := 5
	n1 := mustAddNote(t, src, p1, "pinned note", &page)
	n2 := mustAddNote(t, src, p1, "floating note", nil)
	a1 := mustAddAnn(t, src, p1, 1, "highlight", `{"rect":[0,0,1,1]}`)
	a2 := mustAddAnn(t, src, p1, 2, "line", `{"x":[1,2],"y":[3,4]}`)
	if err := src.LinkNote(n1.ID, a1.ID); err != nil {
		t.Fatalf("LinkNote(n1,a1): %v", err)
	}
	if err := src.LinkNote(n1.ID, a2.ID); err != nil {
		t.Fatalf("LinkNote(n1,a2): %v", err)
	}
	if err := src.LinkNote(n2.ID, a1.ID); err != nil {
		t.Fatalf("LinkNote(n2,a1): %v", err)
	}
	if err := src.SetStatus(p2, "done"); err != nil {
		t.Fatalf("SetStatus(p2): %v", err)
	}
	mustAddNote(t, src, p2, "elsewhere", nil)
	mustAddAnn(t, src, p3, 7, "box", `{"rect":[9,9,1,1]}`)

	data, err := ExportJSON(src, "books")
	if err != nil {
		t.Fatalf("ExportJSON: %v", err)
	}

	dst := newTestStore(t)
	// An unrelated setting must survive a replace import: the legacy
	// format carries no settings, so a backup restore must not wipe
	// ones the user chose locally.
	if err := dst.SetSetting("theme", "dark"); err != nil {
		t.Fatalf("SetSetting(theme): %v", err)
	}
	imported, err := ImportJSON(dst, data, true)
	if err != nil {
		t.Fatalf("ImportJSON(replace): %v", err)
	}
	if imported != 2 {
		t.Errorf("imported = %d, want 2 (one per file record; the annotation-only path has none)", imported)
	}

	srcFiles, err := src.ListFiles()
	if err != nil {
		t.Fatalf("ListFiles(src): %v", err)
	}
	dstFiles, err := dst.ListFiles()
	if err != nil {
		t.Fatalf("ListFiles(dst): %v", err)
	}
	if !reflect.DeepEqual(srcFiles, dstFiles) {
		t.Errorf("ListFiles mismatch:\n src=%+v\n dst=%+v", srcFiles, dstFiles)
	}
	for _, p := range []string{p1, p2, p3} {
		wantNotes, err := src.ListNotes(p)
		if err != nil {
			t.Fatalf("ListNotes(src, %q): %v", p, err)
		}
		gotNotes, err := dst.ListNotes(p)
		if err != nil {
			t.Fatalf("ListNotes(dst, %q): %v", p, err)
		}
		if !reflect.DeepEqual(wantNotes, gotNotes) {
			t.Errorf("ListNotes(%q) mismatch:\n src=%+v\n dst=%+v", p, wantNotes, gotNotes)
		}
		wantAnns, err := src.ListAnnotations(p)
		if err != nil {
			t.Fatalf("ListAnnotations(src, %q): %v", p, err)
		}
		gotAnns, err := dst.ListAnnotations(p)
		if err != nil {
			t.Fatalf("ListAnnotations(dst, %q): %v", p, err)
		}
		if !reflect.DeepEqual(wantAnns, gotAnns) {
			t.Errorf("ListAnnotations(%q) mismatch:\n src=%+v\n dst=%+v", p, wantAnns, gotAnns)
		}
	}

	// rootName lands in the agreed settings key; the other setting stays.
	if v, ok, err := dst.Setting("root_name"); err != nil || !ok || v != "books" {
		t.Errorf("Setting(root_name) = (%q, %v, %v), want (\"books\", true, nil)", v, ok, err)
	}
	if v, ok, err := dst.Setting("theme"); err != nil || !ok || v != "dark" {
		t.Errorf("Setting(theme) = (%q, %v, %v), want (\"dark\", true, nil)", v, ok, err)
	}

	// Pinned ids and timestamps: re-export must be byte-identical, so
	// everything that shapes the JSON survived verbatim.
	reData, err := ExportJSON(dst, "books")
	if err != nil {
		t.Fatalf("ExportJSON(dst): %v", err)
	}
	if !bytes.Equal(data, reData) {
		t.Errorf("re-export differs from original:\n original:\n%s\n re-export:\n%s", data, reData)
	}
}

// TestImportMergeNewerWins pins the replace=false semantics of the
// reference importJSON: per-path merge where the incoming record wins
// only when its updatedAt string is strictly greater; the winning
// path's notes/annotations/links are replaced wholesale (the reference
// replaces the whole record object, notes included). Legacy files
// without annotations/links keys must import fine.
func TestImportMergeNewerWins(t *testing.T) {
	s := newTestStore(t)
	const old = "C:/books/old.pdf"   // incoming older  → kept
	const fresh = "C:/books/new.pdf" // incoming newer  → wins
	const mine = "C:/books/mine.pdf" // only in store   → kept
	const added = "C:/books/added.pdf"

	if err := s.SetStatus(old, "reading"); err != nil {
		t.Fatalf("SetStatus(old): %v", err)
	}
	keepNote := mustAddNote(t, s, old, "keep me", nil)
	keepAnn := mustAddAnn(t, s, old, 3, "box", `{"r":[1,1,1,1]}`)

	if err := s.SetStatus(fresh, "todo"); err != nil {
		t.Fatalf("SetStatus(fresh): %v", err)
	}
	staleNote := mustAddNote(t, s, fresh, "stale", nil)
	staleAnn := mustAddAnn(t, s, fresh, 1, "highlight", `{"r":[2,2,2,2]}`)
	if err := s.LinkNote(staleNote.ID, staleAnn.ID); err != nil {
		t.Fatalf("LinkNote(stale): %v", err)
	}

	if err := s.SetStatus(mine, "done"); err != nil {
		t.Fatalf("SetStatus(mine): %v", err)
	}

	// Legacy-shaped input: no annotations/links keys at all except the
	// winner's, plus a record older than the stored one.
	const incoming = `{
  "version": 1,
  "rootName": "elsewhere",
  "files": {
    "C:/books/old.pdf": {"status":"skip","page":9,"totalPages":9,"starred":true,"notes":[],"updatedAt":"2000-01-01T00:00:00Z"},
    "C:/books/new.pdf": {"status":"done","page":2,"totalPages":5,"starred":false,
      "notes":[{"id":"note-new","text":"incoming note","page":2,"ts":"2030-01-02T03:04:05Z"}],
      "updatedAt":"2030-01-01T00:00:00Z"},
    "C:/books/added.pdf": {"status":"todo","page":0,"totalPages":0,"starred":false,"notes":[],"updatedAt":"2030-01-01T00:00:00Z"}
  },
  "annotations": [{"id":"ann-new","path":"C:/books/new.pdf","page":2,"kind":"box","data":"{}","createdAt":"2030-01-02T03:04:05Z"}],
  "links": [{"noteId":"note-new","annotationId":"ann-new"}]
}`
	imported, err := ImportJSON(s, []byte(incoming), false)
	if err != nil {
		t.Fatalf("ImportJSON(merge): %v", err)
	}
	if imported != 2 {
		t.Errorf("imported = %d, want 2 (new.pdf and added.pdf win; old.pdf loses)", imported)
	}

	// old.pdf: incoming updatedAt is older → stored record and its
	// notes/annotations are untouched.
	rec, err := s.File(old)
	if err != nil {
		t.Fatalf("File(old): %v", err)
	}
	if rec.Status != "reading" || rec.Page != 0 {
		t.Errorf("old.pdf record = %+v, want untouched status=reading", rec)
	}
	notes, err := s.ListNotes(old)
	if err != nil {
		t.Fatalf("ListNotes(old): %v", err)
	}
	if len(notes) != 1 || notes[0].ID != keepNote.ID {
		t.Errorf("old.pdf notes = %+v, want only %s", notes, keepNote.ID)
	}
	anns, err := s.ListAnnotations(old)
	if err != nil {
		t.Fatalf("ListAnnotations(old): %v", err)
	}
	if len(anns) != 1 || anns[0].ID != keepAnn.ID {
		t.Errorf("old.pdf annotations = %+v, want only %s", anns, keepAnn.ID)
	}

	// fresh.pdf: incoming wins → record replaced, old notes and their
	// links replaced by the incoming ones.
	rec, err = s.File(fresh)
	if err != nil {
		t.Fatalf("File(fresh): %v", err)
	}
	if rec.Status != "done" || rec.Page != 2 || rec.TotalPages != 5 || rec.UpdatedAt != "2030-01-01T00:00:00Z" {
		t.Errorf("fresh.pdf record = %+v, want incoming values verbatim", rec)
	}
	notes, err = s.ListNotes(fresh)
	if err != nil {
		t.Fatalf("ListNotes(fresh): %v", err)
	}
	if len(notes) != 1 || notes[0].ID != "note-new" || notes[0].TS != "2030-01-02T03:04:05Z" {
		t.Errorf("fresh.pdf notes = %+v, want only incoming note-new with verbatim ts", notes)
	}
	if len(notes[0].AnnIDs) != 1 || notes[0].AnnIDs[0] != "ann-new" {
		t.Errorf("fresh.pdf note AnnIDs = %v, want [ann-new] (stale link gone)", notes[0].AnnIDs)
	}
	anns, err = s.ListAnnotations(fresh)
	if err != nil {
		t.Fatalf("ListAnnotations(fresh): %v", err)
	}
	if len(anns) != 1 || anns[0].ID != "ann-new" {
		t.Errorf("fresh.pdf annotations = %+v, want only ann-new (stale gone)", anns)
	}

	// mine.pdf only exists locally → kept as-is.
	rec, err = s.File(mine)
	if err != nil {
		t.Fatalf("File(mine): %v", err)
	}
	if rec.Status != "done" {
		t.Errorf("mine.pdf status = %q, want kept \"done\"", rec.Status)
	}

	// added.pdf arrives from the input.
	rec, err = s.File(added)
	if err != nil {
		t.Fatalf("File(added): %v", err)
	}
	if rec.Status != "todo" || rec.UpdatedAt != "2030-01-01T00:00:00Z" {
		t.Errorf("added.pdf record = %+v, want incoming values", rec)
	}

	// Merge mode never touches rootName (the reference only assigns it
	// on replace).
	if _, ok, err := s.Setting("root_name"); err != nil || ok {
		t.Errorf("Setting(root_name) after merge = ok=%v err=%v, want untouched", ok, err)
	}
}

// TestImportRejectsInvalidWithoutTouchingStore pins Review Focus #5:
// invalid JSON or a missing/non-object files key must return an error
// and leave every table of the store byte-identical.
func TestImportRejectsInvalidWithoutTouchingStore(t *testing.T) {
	s := newTestStore(t)
	const p1 = "C:/books/a.pdf"
	const p2 = "C:/books/b.pdf"

	if err := s.SetStatus(p1, "reading"); err != nil {
		t.Fatalf("SetStatus: %v", err)
	}
	if err := s.SetStar(p1, true); err != nil {
		t.Fatalf("SetStar: %v", err)
	}
	if err := s.SetTotalPages(p1, 10); err != nil {
		t.Fatalf("SetTotalPages: %v", err)
	}
	if err := s.SetProgress(p1, 4); err != nil {
		t.Fatalf("SetProgress: %v", err)
	}
	page := 3
	n1 := mustAddNote(t, s, p1, "note one", &page)
	mustAddNote(t, s, p1, "note two", nil)
	a1 := mustAddAnn(t, s, p1, 1, "highlight", `{"rect":[0,0,1,1]}`)
	if err := s.LinkNote(n1.ID, a1.ID); err != nil {
		t.Fatalf("LinkNote: %v", err)
	}
	if err := s.SetStatus(p2, "done"); err != nil {
		t.Fatalf("SetStatus(p2): %v", err)
	}
	if err := s.SetSetting("root_name", "books"); err != nil {
		t.Fatalf("SetSetting(root_name): %v", err)
	}
	if err := s.SetSetting("theme", "dark"); err != nil {
		t.Fatalf("SetSetting(theme): %v", err)
	}

	before, err := s.Dump()
	if err != nil {
		t.Fatalf("Dump(before): %v", err)
	}

	cases := []struct {
		name string
		in   string
	}{
		{"truncated", `{"files": {"a":`},
		{"not JSON at all", `hello world`},
		{"empty object (no files)", `{}`},
		{"null document", `null`},
		{"array document", `[]`},
		{"files not an object", `{"files": 5}`},
		{"files null", `{"files": null}`},
	}
	for _, tc := range cases {
		for _, replace := range []bool{true, false} {
			n, err := ImportJSON(s, []byte(tc.in), replace)
			if err == nil {
				t.Errorf("ImportJSON(%s, replace=%v) = %v, want error", tc.name, replace, err)
			}
			if n != 0 {
				t.Errorf("ImportJSON(%s, replace=%v) imported = %d, want 0", tc.name, replace, n)
			}
			after, derr := s.Dump()
			if derr != nil {
				t.Fatalf("Dump(after %s): %v", tc.name, derr)
			}
			if !reflect.DeepEqual(before, after) {
				t.Errorf("store changed by rejected ImportJSON(%s, replace=%v):\n before=%+v\n after=%+v",
					tc.name, replace, before, after)
			}
		}
	}
}
