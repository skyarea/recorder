package storage

import (
	"fmt"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"time"
)

// newTestStore opens a Store on a fresh database file inside the test's
// temp directory.
func newTestStore(t *testing.T) *Store {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "recorder.db")
	s, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open(%q): %v", dbPath, err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func TestFileDefaultsAndSetters(t *testing.T) {
	s := newTestStore(t)
	const path = "C:/books/a.pdf"

	// Unknown path: todo default, no error.
	got, err := s.File(path)
	if err != nil {
		t.Fatalf("File(): %v", err)
	}
	if got.Status != "todo" || got.Page != 0 || got.TotalPages != 0 || got.Starred || got.UpdatedAt != "" {
		t.Fatalf("File() default = %+v, want Status \"todo\" with zero values", got)
	}

	// SetStatus persists and stamps UpdatedAt as RFC3339.
	if err := s.SetStatus(path, "reading"); err != nil {
		t.Fatalf("SetStatus(\"reading\"): %v", err)
	}
	got, err = s.File(path)
	if err != nil {
		t.Fatalf("File(): %v", err)
	}
	if got.Status != "reading" {
		t.Errorf("Status = %q, want \"reading\"", got.Status)
	}
	if _, err := time.Parse(time.RFC3339, got.UpdatedAt); err != nil {
		t.Errorf("UpdatedAt = %q, want RFC3339: %v", got.UpdatedAt, err)
	}

	// SetStar persists and leaves the status alone.
	if err := s.SetStar(path, true); err != nil {
		t.Fatalf("SetStar(true): %v", err)
	}
	got, err = s.File(path)
	if err != nil {
		t.Fatalf("File(): %v", err)
	}
	if !got.Starred {
		t.Errorf("Starred = false, want true")
	}
	if got.Status != "reading" {
		t.Errorf("Status = %q after SetStar, want \"reading\"", got.Status)
	}

	if err := s.SetStar(path, false); err != nil {
		t.Fatalf("SetStar(false): %v", err)
	}
	got, err = s.File(path)
	if err != nil {
		t.Fatalf("File(): %v", err)
	}
	if got.Starred {
		t.Errorf("Starred = true, want false")
	}
}

func TestSetProgressClampsToTotalPages(t *testing.T) {
	s := newTestStore(t)
	const path = "C:/books/b.pdf"

	if err := s.SetTotalPages(path, 10); err != nil {
		t.Fatalf("SetTotalPages(10): %v", err)
	}
	if err := s.SetProgress(path, 15); err != nil {
		t.Fatalf("SetProgress(15): %v", err)
	}
	got, err := s.File(path)
	if err != nil {
		t.Fatalf("File(): %v", err)
	}
	if got.Page != 10 {
		t.Errorf("Page = %d, want clamped to 10", got.Page)
	}
	if got.TotalPages != 10 {
		t.Errorf("TotalPages = %d, want 10", got.TotalPages)
	}

	// In-range progress is kept as-is.
	if err := s.SetProgress(path, 7); err != nil {
		t.Fatalf("SetProgress(7): %v", err)
	}
	if got, err = s.File(path); err != nil {
		t.Fatalf("File(): %v", err)
	}
	if got.Page != 7 {
		t.Errorf("Page = %d, want 7", got.Page)
	}

	// Negative page clamps to 0.
	if err := s.SetProgress(path, -3); err != nil {
		t.Fatalf("SetProgress(-3): %v", err)
	}
	if got, err = s.File(path); err != nil {
		t.Fatalf("File(): %v", err)
	}
	if got.Page != 0 {
		t.Errorf("Page = %d, want clamped to 0", got.Page)
	}
}

func TestSetTotalPagesClampsPage(t *testing.T) {
	s := newTestStore(t)
	const path = "C:/books/c.pdf"

	if err := s.SetProgress(path, 8); err != nil {
		t.Fatalf("SetProgress(8): %v", err)
	}
	if err := s.SetTotalPages(path, 5); err != nil {
		t.Fatalf("SetTotalPages(5): %v", err)
	}
	got, err := s.File(path)
	if err != nil {
		t.Fatalf("File(): %v", err)
	}
	if got.TotalPages != 5 {
		t.Errorf("TotalPages = %d, want 5", got.TotalPages)
	}
	if got.Page != 5 {
		t.Errorf("Page = %d, want linked down to 5", got.Page)
	}

	// Negative total clamps to 0; page (5 > 0) follows it down.
	if err := s.SetTotalPages(path, -1); err != nil {
		t.Fatalf("SetTotalPages(-1): %v", err)
	}
	if got, err = s.File(path); err != nil {
		t.Fatalf("File(): %v", err)
	}
	if got.TotalPages != 0 {
		t.Errorf("TotalPages = %d, want clamped to 0", got.TotalPages)
	}
	if got.Page != 0 {
		t.Errorf("Page = %d, want 0 after total clamp", got.Page)
	}
}

func TestSetStatusRejectsInvalid(t *testing.T) {
	s := newTestStore(t)
	const path = "C:/books/d.pdf"

	// The four canonical statuses are accepted.
	for _, status := range []string{"todo", "reading", "done", "skip"} {
		if err := s.SetStatus(path, status); err != nil {
			t.Errorf("SetStatus(%q): %v, want nil", status, err)
		}
	}

	if err := s.SetStatus(path, "reading"); err != nil {
		t.Fatalf("SetStatus(\"reading\"): %v", err)
	}
	before, err := s.File(path)
	if err != nil {
		t.Fatalf("File(): %v", err)
	}

	if err := s.SetStatus(path, "bogus"); err == nil {
		t.Fatal("SetStatus(\"bogus\") = nil, want error")
	}

	after, err := s.File(path)
	if err != nil {
		t.Fatalf("File(): %v", err)
	}
	if before != after {
		t.Errorf("record changed by rejected SetStatus: %+v -> %+v", before, after)
	}

	// A rejected status never creates a record either.
	const fresh = "C:/books/fresh.pdf"
	if err := s.SetStatus(fresh, "bogus"); err == nil {
		t.Fatal("SetStatus(fresh, \"bogus\") = nil, want error")
	}
	got, err := s.File(fresh)
	if err != nil {
		t.Fatalf("File(): %v", err)
	}
	if got.Status != "todo" || got.UpdatedAt != "" {
		t.Errorf("File() on fresh path = %+v, want untouched todo default", got)
	}
}

func TestSettingsRoundTrip(t *testing.T) {
	s := newTestStore(t)

	// Missing key: no value, ok=false, no error.
	v, ok, err := s.Setting("theme")
	if err != nil {
		t.Fatalf("Setting(\"theme\"): %v", err)
	}
	if ok {
		t.Errorf("Setting(\"theme\") ok = true for missing key")
	}
	if v != "" {
		t.Errorf("Setting(\"theme\") = %q, want \"\"", v)
	}

	if err := s.SetSetting("theme", "dark"); err != nil {
		t.Fatalf("SetSetting: %v", err)
	}
	if v, ok, err = s.Setting("theme"); err != nil || !ok || v != "dark" {
		t.Errorf("Setting(\"theme\") = (%q, %v, %v), want (\"dark\", true, nil)", v, ok, err)
	}

	// Overwrite.
	if err := s.SetSetting("theme", "light"); err != nil {
		t.Fatalf("SetSetting overwrite: %v", err)
	}
	if v, ok, err = s.Setting("theme"); err != nil || !ok || v != "light" {
		t.Errorf("Setting(\"theme\") = (%q, %v, %v), want (\"light\", true, nil)", v, ok, err)
	}

	// Empty value is still a present key.
	if err := s.SetSetting("empty", ""); err != nil {
		t.Fatalf("SetSetting(\"\", \"\"): %v", err)
	}
	if v, ok, err = s.Setting("empty"); err != nil || !ok || v != "" {
		t.Errorf("Setting(\"empty\") = (%q, %v, %v), want (\"\", true, nil)", v, ok, err)
	}
}

// TestConcurrentProgressAndTotalPagesKeepInvariant hammers one Store from
// several goroutines (the Wails reality: bound calls dispatch on separate
// goroutines, e.g. a save goroutine racing a UI action) and asserts that
// every observable state keeps page <= total_pages. Before the setters were
// made transactional, a SetTotalPages landing between a SetProgress load and
// its write persisted a row with page > total_pages.
func TestConcurrentProgressAndTotalPagesKeepInvariant(t *testing.T) {
	s := newTestStore(t)
	const path = "C:/books/concurrent.pdf"

	// Seed total >= 1: clamping only kicks in when TotalPages > 0, so a
	// total of 0 would legally allow page > 0 and make the assertion
	// vacuous. The SetTotalPages goroutine below never writes 0 either.
	if err := s.SetTotalPages(path, 20); err != nil {
		t.Fatalf("SetTotalPages(20): %v", err)
	}

	const (
		writersPerKind = 2
		iterations     = 100
		maxValue       = 30 // values in [0, maxValue); totals stay >= 1
	)

	var wg sync.WaitGroup
	var mu sync.Mutex
	var failures []string
	fail := func(format string, args ...any) {
		mu.Lock()
		failures = append(failures, fmt.Sprintf(format, args...))
		mu.Unlock()
	}
	start := make(chan struct{})

	for g := 0; g < writersPerKind; g++ {
		wg.Add(1)
		go func(seed int) {
			defer wg.Done()
			<-start
			for i := 0; i < iterations; i++ {
				if err := s.SetProgress(path, (seed*iterations+i)%maxValue); err != nil {
					fail("SetProgress: %v", err)
					return
				}
			}
		}(g)

		wg.Add(1)
		go func(seed int) {
			defer wg.Done()
			<-start
			for i := 0; i < iterations; i++ {
				if err := s.SetTotalPages(path, 1+(seed*iterations+i)%maxValue); err != nil {
					fail("SetTotalPages: %v", err)
					return
				}
			}
		}(g)
	}

	// A reader checks the invariant on every committed state: each setter
	// commits atomically, so any File() snapshot must already satisfy it.
	wg.Add(1)
	go func() {
		defer wg.Done()
		<-start
		for i := 0; i < iterations; i++ {
			rec, err := s.File(path)
			if err != nil {
				fail("File(): %v", err)
				return
			}
			if rec.Page > rec.TotalPages {
				fail("File() saw page=%d > total_pages=%d", rec.Page, rec.TotalPages)
				return
			}
		}
	}()

	close(start)
	wg.Wait()

	for _, msg := range failures {
		t.Error(msg)
	}
	if t.Failed() {
		return
	}

	got, err := s.File(path)
	if err != nil {
		t.Fatalf("File(): %v", err)
	}
	if got.Page > got.TotalPages {
		t.Errorf("final row page=%d > total_pages=%d, invariant broken", got.Page, got.TotalPages)
	}
	if got.Page < 0 || got.Page >= maxValue {
		t.Errorf("final row page=%d, want in [0, %d)", got.Page, maxValue)
	}
	if got.TotalPages < 1 || got.TotalPages > maxValue {
		t.Errorf("final row total_pages=%d, want in [1, %d]", got.TotalPages, maxValue)
	}
	if got.Status != "todo" {
		t.Errorf("final row status=%q, want \"todo\"", got.Status)
	}
	if _, err := time.Parse(time.RFC3339, got.UpdatedAt); err != nil {
		t.Errorf("final row UpdatedAt=%q, want RFC3339: %v", got.UpdatedAt, err)
	}
}

// sortedCopy returns a sorted copy of ids so two AnnIDs slices can be
// compared without depending on link order (unspecified by the brief).
func sortedCopy(ids []string) []string {
	out := append([]string(nil), ids...)
	slices.Sort(out)
	return out
}

// findNote returns the note with id, failing the test when it is absent.
func findNote(t *testing.T, notes []Note, id string) Note {
	t.Helper()
	for _, n := range notes {
		if n.ID == id {
			return n
		}
	}
	t.Fatalf("note %q missing from ListNotes result (%d notes)", id, len(notes))
	return Note{}
}

// linkCount counts rows in note_annotations: the link rows of a deleted
// parent are otherwise unobservable through the public API.
func linkCount(t *testing.T, s *Store) int {
	t.Helper()
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM note_annotations`).Scan(&n); err != nil {
		t.Fatalf("COUNT(note_annotations): %v", err)
	}
	return n
}

func TestNoteCRUD(t *testing.T) {
	s := newTestStore(t)
	const path = "C:/books/notes.pdf"

	// Add with Page nil: id/ts generated by the backend, page stays nil.
	n1, err := s.AddNote(path, "first", nil)
	if err != nil {
		t.Fatalf("AddNote(nil page): %v", err)
	}
	if n1.ID == "" {
		t.Error("AddNote returned an empty ID")
	}
	if n1.FilePath != path {
		t.Errorf("FilePath = %q, want %q", n1.FilePath, path)
	}
	if n1.Text != "first" {
		t.Errorf("Text = %q, want %q", n1.Text, "first")
	}
	if n1.Page != nil {
		t.Errorf("Page = %d, want nil", *n1.Page)
	}
	if _, err := time.Parse(time.RFC3339, n1.TS); err != nil {
		t.Errorf("TS = %q, want RFC3339: %v", n1.TS, err)
	}
	if len(n1.AnnIDs) != 0 {
		t.Errorf("AnnIDs = %v, want empty", n1.AnnIDs)
	}

	// Add with a numeric page.
	page := 5
	n2, err := s.AddNote(path, "second", &page)
	if err != nil {
		t.Fatalf("AddNote(page 5): %v", err)
	}
	if n2.Page == nil || *n2.Page != 5 {
		t.Errorf("Page = %v, want 5", n2.Page)
	}
	if n2.ID == n1.ID {
		t.Errorf("AddNote reused ID %q", n2.ID)
	}

	// Another file's notes are not listed under this file.
	if _, err := s.AddNote("C:/books/other.pdf", "elsewhere", nil); err != nil {
		t.Fatalf("AddNote(other file): %v", err)
	}

	notes, err := s.ListNotes(path)
	if err != nil {
		t.Fatalf("ListNotes: %v", err)
	}
	if len(notes) != 2 {
		t.Fatalf("ListNotes returned %d notes, want 2", len(notes))
	}
	if g := findNote(t, notes, n1.ID); g.Page != nil {
		t.Errorf("stored Page = %d, want nil", *g.Page)
	}
	if g := findNote(t, notes, n2.ID); g.Page == nil || *g.Page != 5 {
		t.Errorf("stored Page = %v, want 5", g.Page)
	}

	// Update persists the new text.
	if err := s.UpdateNote(n1.ID, "first edited"); err != nil {
		t.Fatalf("UpdateNote: %v", err)
	}
	notes, err = s.ListNotes(path)
	if err != nil {
		t.Fatalf("ListNotes: %v", err)
	}
	if g := findNote(t, notes, n1.ID); g.Text != "first edited" {
		t.Errorf("Text after update = %q, want %q", g.Text, "first edited")
	}

	// Updating an unknown id is a no-op, not an error (legacy reference
	// behaviour: find-then-write never fails on a missing note).
	if err := s.UpdateNote("no-such-note", "x"); err != nil {
		t.Errorf("UpdateNote(missing) = %v, want nil", err)
	}
	notes, err = s.ListNotes(path)
	if err != nil {
		t.Fatalf("ListNotes: %v", err)
	}
	if len(notes) != 2 {
		t.Fatalf("ListNotes after missing update returned %d notes, want 2", len(notes))
	}
	if g := findNote(t, notes, n1.ID); g.Text != "first edited" {
		t.Errorf("Text after missing update = %q, want unchanged %q", g.Text, "first edited")
	}

	// Delete removes exactly that note.
	if err := s.DeleteNote(n1.ID); err != nil {
		t.Fatalf("DeleteNote: %v", err)
	}
	notes, err = s.ListNotes(path)
	if err != nil {
		t.Fatalf("ListNotes: %v", err)
	}
	if len(notes) != 1 || notes[0].ID != n2.ID {
		t.Fatalf("ListNotes after delete = %+v, want only note %s", notes, n2.ID)
	}

	// Deleting an unknown id is a no-op, not an error.
	if err := s.DeleteNote("no-such-note"); err != nil {
		t.Errorf("DeleteNote(missing) = %v, want nil", err)
	}

	// Deleting the last note leaves an empty list (nil slice, no error).
	if err := s.DeleteNote(n2.ID); err != nil {
		t.Fatalf("DeleteNote(last): %v", err)
	}
	notes, err = s.ListNotes(path)
	if err != nil {
		t.Fatalf("ListNotes: %v", err)
	}
	if len(notes) != 0 {
		t.Errorf("ListNotes after deleting all = %d notes, want 0", len(notes))
	}
}

func TestAnnotationCRUD(t *testing.T) {
	s := newTestStore(t)
	const path = "C:/books/anns.pdf"

	// All three kinds are accepted and round-trip their fields.
	data := `{"rect":[0.1,0.2,0.3,0.4]}`
	var added []Annotation
	for i, kind := range []string{"highlight", "line", "box"} {
		a, err := s.AddAnnotation(path, i+1, kind, data)
		if err != nil {
			t.Fatalf("AddAnnotation(%q): %v", kind, err)
		}
		if a.ID == "" {
			t.Errorf("AddAnnotation(%q) returned an empty ID", kind)
		}
		if a.FilePath != path || a.Page != i+1 || a.Kind != kind || a.Data != data {
			t.Errorf("AddAnnotation(%q) = %+v, want FilePath %q Page %d Kind %q Data %q",
				kind, a, path, i+1, kind, data)
		}
		if _, err := time.Parse(time.RFC3339, a.CreatedAt); err != nil {
			t.Errorf("CreatedAt = %q, want RFC3339: %v", a.CreatedAt, err)
		}
		added = append(added, a)
	}

	// A kind outside highlight|line|box is rejected before any write.
	if _, err := s.AddAnnotation(path, 4, "circle", data); err == nil {
		t.Fatal(`AddAnnotation(kind "circle") = nil, want error`)
	}

	// Data is stored verbatim: storage never parses or validates the JSON.
	const raw = `not json {{{`
	rawAnn, err := s.AddAnnotation(path, 9, "line", raw)
	if err != nil {
		t.Fatalf("AddAnnotation(raw data): %v", err)
	}
	if rawAnn.Data != raw {
		t.Errorf("Data = %q, want verbatim %q", rawAnn.Data, raw)
	}

	// Another file's annotations are not listed here.
	if _, err := s.AddAnnotation("C:/books/other.pdf", 1, "box", data); err != nil {
		t.Fatalf("AddAnnotation(other file): %v", err)
	}

	list, err := s.ListAnnotations(path)
	if err != nil {
		t.Fatalf("ListAnnotations: %v", err)
	}
	if len(list) != 4 {
		t.Fatalf("ListAnnotations returned %d, want 4 (3 kinds + raw data; the rejected kind writes nothing)", len(list))
	}
	byID := make(map[string]Annotation, len(list))
	for _, a := range list {
		byID[a.ID] = a
	}
	for _, want := range append(added, rawAnn) {
		got, ok := byID[want.ID]
		if !ok {
			t.Errorf("annotation %q missing from ListAnnotations", want.ID)
			continue
		}
		if got.Data != want.Data || got.Kind != want.Kind || got.Page != want.Page {
			t.Errorf("stored %+v, want %+v", got, want)
		}
	}

	// Delete removes the row; a missing id is a no-op.
	if err := s.DeleteAnnotation(added[0].ID); err != nil {
		t.Fatalf("DeleteAnnotation: %v", err)
	}
	list, err = s.ListAnnotations(path)
	if err != nil {
		t.Fatalf("ListAnnotations: %v", err)
	}
	if len(list) != 3 {
		t.Fatalf("ListAnnotations after delete = %d, want 3", len(list))
	}
	for _, a := range list {
		if a.ID == added[0].ID {
			t.Errorf("deleted annotation %q still listed", a.ID)
		}
	}
	if err := s.DeleteAnnotation("no-such-annotation"); err != nil {
		t.Errorf("DeleteAnnotation(missing) = %v, want nil", err)
	}
}

func TestLinksAreManyToManyAndCascade(t *testing.T) {
	s := newTestStore(t)
	const path = "C:/books/links.pdf"

	ann1, err := s.AddAnnotation(path, 1, "highlight", `{}`)
	if err != nil {
		t.Fatalf("AddAnnotation 1: %v", err)
	}
	ann2, err := s.AddAnnotation(path, 2, "line", `{}`)
	if err != nil {
		t.Fatalf("AddAnnotation 2: %v", err)
	}
	noteA, err := s.AddNote(path, "A", nil)
	if err != nil {
		t.Fatalf("AddNote A: %v", err)
	}
	noteB, err := s.AddNote(path, "B", nil)
	if err != nil {
		t.Fatalf("AddNote B: %v", err)
	}

	// One note → two annotations; one annotation → two notes.
	for _, pair := range [][2]string{
		{noteA.ID, ann1.ID},
		{noteA.ID, ann2.ID},
		{noteB.ID, ann1.ID},
	} {
		if err := s.LinkNote(pair[0], pair[1]); err != nil {
			t.Fatalf("LinkNote(%s, %s): %v", pair[0], pair[1], err)
		}
	}

	// Linking the same pair twice: no error, no duplicate row.
	if err := s.LinkNote(noteA.ID, ann1.ID); err != nil {
		t.Fatalf("LinkNote(same pair twice): %v", err)
	}
	if n := linkCount(t, s); n != 3 {
		t.Errorf("COUNT(note_annotations) = %d after duplicate link, want 3", n)
	}

	notes, err := s.ListNotes(path)
	if err != nil {
		t.Fatalf("ListNotes: %v", err)
	}
	if got := sortedCopy(findNote(t, notes, noteA.ID).AnnIDs); !slices.Equal(got, sortedCopy([]string{ann1.ID, ann2.ID})) {
		t.Errorf("note A AnnIDs = %v, want {%s %s}", got, ann1.ID, ann2.ID)
	}
	if got := findNote(t, notes, noteB.ID).AnnIDs; !slices.Equal(got, []string{ann1.ID}) {
		t.Errorf("note B AnnIDs = %v, want {%s}", got, ann1.ID)
	}

	// Unlink removes exactly that pair and is idempotent.
	if err := s.UnlinkNote(noteA.ID, ann1.ID); err != nil {
		t.Fatalf("UnlinkNote: %v", err)
	}
	notes, err = s.ListNotes(path)
	if err != nil {
		t.Fatalf("ListNotes: %v", err)
	}
	if got := findNote(t, notes, noteA.ID).AnnIDs; !slices.Equal(got, []string{ann2.ID}) {
		t.Errorf("note A AnnIDs after unlink = %v, want {%s}", got, ann2.ID)
	}
	if got := findNote(t, notes, noteB.ID).AnnIDs; !slices.Equal(got, []string{ann1.ID}) {
		t.Errorf("note B AnnIDs after unlink = %v, want {%s} (untouched)", got, ann1.ID)
	}
	if n := linkCount(t, s); n != 2 {
		t.Errorf("COUNT(note_annotations) = %d after unlink, want 2", n)
	}
	if err := s.UnlinkNote(noteA.ID, ann1.ID); err != nil {
		t.Errorf("UnlinkNote(missing pair) = %v, want nil", err)
	}
	if n := linkCount(t, s); n != 2 {
		t.Errorf("COUNT(note_annotations) = %d after repeated unlink, want 2", n)
	}
	if err := s.LinkNote(noteA.ID, ann1.ID); err != nil {
		t.Fatalf("LinkNote(re-link): %v", err)
	}
	if n := linkCount(t, s); n != 3 {
		t.Errorf("COUNT(note_annotations) = %d after re-link, want 3", n)
	}

	// DeleteNote cascades: its link rows vanish, everything else stays.
	if err := s.DeleteNote(noteA.ID); err != nil {
		t.Fatalf("DeleteNote: %v", err)
	}
	var forA int
	if err := s.db.QueryRow(
		`SELECT COUNT(*) FROM note_annotations WHERE note_id = ?`, noteA.ID,
	).Scan(&forA); err != nil {
		t.Fatalf("COUNT links for deleted note: %v", err)
	}
	if forA != 0 {
		t.Errorf("link rows for deleted note A = %d, want 0", forA)
	}
	if n := linkCount(t, s); n != 1 {
		t.Errorf("COUNT(note_annotations) = %d after DeleteNote, want 1 (note B – ann1)", n)
	}
	notes, err = s.ListNotes(path)
	if err != nil {
		t.Fatalf("ListNotes: %v", err)
	}
	if len(notes) != 1 || notes[0].ID != noteB.ID {
		t.Fatalf("ListNotes after DeleteNote = %+v, want only note B", notes)
	}
	if got := notes[0].AnnIDs; !slices.Equal(got, []string{ann1.ID}) {
		t.Errorf("note B AnnIDs = %v, want {%s} (untouched by A's delete)", got, ann1.ID)
	}
	annList, err := s.ListAnnotations(path)
	if err != nil {
		t.Fatalf("ListAnnotations: %v", err)
	}
	if len(annList) != 2 {
		t.Errorf("ListAnnotations after DeleteNote = %d, want 2 (only links cascade)", len(annList))
	}

	// DeleteAnnotation cascades its link rows too.
	if err := s.DeleteAnnotation(ann1.ID); err != nil {
		t.Fatalf("DeleteAnnotation: %v", err)
	}
	var forAnn1 int
	if err := s.db.QueryRow(
		`SELECT COUNT(*) FROM note_annotations WHERE annotation_id = ?`, ann1.ID,
	).Scan(&forAnn1); err != nil {
		t.Fatalf("COUNT links for deleted annotation: %v", err)
	}
	if forAnn1 != 0 {
		t.Errorf("link rows for deleted annotation = %d, want 0", forAnn1)
	}
	if n := linkCount(t, s); n != 0 {
		t.Errorf("COUNT(note_annotations) = %d after DeleteAnnotation, want 0", n)
	}
	notes, err = s.ListNotes(path)
	if err != nil {
		t.Fatalf("ListNotes: %v", err)
	}
	if got := notes[0].AnnIDs; len(got) != 0 {
		t.Errorf("note B AnnIDs = %v, want empty after its annotation is deleted", got)
	}
	annList, err = s.ListAnnotations(path)
	if err != nil {
		t.Fatalf("ListAnnotations: %v", err)
	}
	if len(annList) != 1 || annList[0].ID != ann2.ID {
		t.Errorf("ListAnnotations = %+v, want only annotation %s", annList, ann2.ID)
	}
}

func TestListNotesReturnsAnnIDs(t *testing.T) {
	s := newTestStore(t)
	const path = "C:/books/order.pdf"

	// Three notes one second apart each: RFC3339 timestamps carry a 1s
	// granularity, so a real gap makes ts ordering unambiguous.
	first, err := s.AddNote(path, "first", nil)
	if err != nil {
		t.Fatalf("AddNote(first): %v", err)
	}
	time.Sleep(1050 * time.Millisecond)
	second, err := s.AddNote(path, "second", nil)
	if err != nil {
		t.Fatalf("AddNote(second): %v", err)
	}
	time.Sleep(1050 * time.Millisecond)
	third, err := s.AddNote(path, "third", nil)
	if err != nil {
		t.Fatalf("AddNote(third): %v", err)
	}

	// One note carries a link; the others report empty AnnIDs.
	ann, err := s.AddAnnotation(path, 1, "highlight", `{}`)
	if err != nil {
		t.Fatalf("AddAnnotation: %v", err)
	}
	if err := s.LinkNote(second.ID, ann.ID); err != nil {
		t.Fatalf("LinkNote: %v", err)
	}

	notes, err := s.ListNotes(path)
	if err != nil {
		t.Fatalf("ListNotes: %v", err)
	}
	if len(notes) != 3 {
		t.Fatalf("ListNotes returned %d notes, want 3", len(notes))
	}

	// ts ascending: the list must come back in insertion order.
	wantOrder := []string{first.ID, second.ID, third.ID}
	for i, id := range wantOrder {
		if notes[i].ID != id {
			t.Fatalf("notes[%d].ID = %s, want %s (ListNotes must return ts ascending)", i, notes[i].ID, id)
		}
	}
	if !(notes[0].TS < notes[1].TS && notes[1].TS < notes[2].TS) {
		t.Errorf("TS not strictly increasing: %q, %q, %q", notes[0].TS, notes[1].TS, notes[2].TS)
	}

	if n := notes[0]; len(n.AnnIDs) != 0 {
		t.Errorf("unlinked note AnnIDs = %v, want empty", n.AnnIDs)
	}
	if n := notes[1]; !slices.Equal(n.AnnIDs, []string{ann.ID}) {
		t.Errorf("linked note AnnIDs = %v, want {%s}", n.AnnIDs, ann.ID)
	}
	if n := notes[2]; len(n.AnnIDs) != 0 {
		t.Errorf("unlinked note AnnIDs = %v, want empty", n.AnnIDs)
	}
}

// TestDumpReturnsFullSnapshot checks that Dump reports every table with
// its documented deterministic order and verbatim ids/timestamps.
func TestDumpReturnsFullSnapshot(t *testing.T) {
	s := newTestStore(t)
	const pA = "C:/books/a.pdf"
	const pB = "C:/books/b.pdf"

	if err := s.SetStatus(pB, "done"); err != nil {
		t.Fatalf("SetStatus(pB): %v", err)
	}
	if err := s.SetStatus(pA, "reading"); err != nil {
		t.Fatalf("SetStatus(pA): %v", err)
	}
	if err := s.SetProgress(pA, 2); err != nil {
		t.Fatalf("SetProgress: %v", err)
	}
	nB := mustSeedNote(t, s, pB, "b note", nil)
	nA := mustSeedNote(t, s, pA, "a note", nil)
	aB, err := s.AddAnnotation(pB, 1, "box", `{}`)
	if err != nil {
		t.Fatalf("AddAnnotation(pB): %v", err)
	}
	aA, err := s.AddAnnotation(pA, 2, "highlight", `{}`)
	if err != nil {
		t.Fatalf("AddAnnotation(pA): %v", err)
	}
	if err := s.LinkNote(nA.ID, aA.ID); err != nil {
		t.Fatalf("LinkNote: %v", err)
	}
	if err := s.SetSetting("theme", "dark"); err != nil {
		t.Fatalf("SetSetting: %v", err)
	}

	d, err := s.Dump()
	if err != nil {
		t.Fatalf("Dump: %v", err)
	}

	// Files ordered by path.
	if len(d.Files) != 2 || d.Files[0].Path != pA || d.Files[1].Path != pB {
		t.Fatalf("Dump.Files = %+v, want %q then %q", d.Files, pA, pB)
	}
	if d.Files[0].Status != "reading" || d.Files[0].Page != 2 || d.Files[0].UpdatedAt == "" {
		t.Errorf("Dump.Files[0] = %+v, want stored reading/2 values", d.Files[0])
	}

	// Notes ordered by file_path; ids/ts verbatim; AnnIDs empty (links
	// live in Dump.Links).
	if len(d.Notes) != 2 || d.Notes[0].ID != nA.ID || d.Notes[1].ID != nB.ID {
		t.Fatalf("Dump.Notes = %+v, want %s then %s by file path", d.Notes, nA.ID, nB.ID)
	}
	if d.Notes[0].TS != nA.TS || d.Notes[0].Text != "a note" {
		t.Errorf("Dump.Notes[0] = %+v, want verbatim %+v", d.Notes[0], nA)
	}
	if d.Notes[0].AnnIDs == nil {
		t.Error("Dump.Notes[0].AnnIDs is nil, want empty non-nil slice")
	}

	// Annotations ordered by file_path.
	if len(d.Annotations) != 2 || d.Annotations[0].ID != aA.ID || d.Annotations[1].ID != aB.ID {
		t.Fatalf("Dump.Annotations = %+v, want %s then %s", d.Annotations, aA.ID, aB.ID)
	}
	if d.Annotations[0].CreatedAt != aA.CreatedAt {
		t.Errorf("Dump.Annotations[0].CreatedAt = %q, want verbatim %q", d.Annotations[0].CreatedAt, aA.CreatedAt)
	}

	// Links by insertion order.
	if len(d.Links) != 1 || d.Links[0] != [2]string{nA.ID, aA.ID} {
		t.Errorf("Dump.Links = %v, want {%s %s}", d.Links, nA.ID, aA.ID)
	}

	// Settings.
	if len(d.Settings) != 1 || d.Settings["theme"] != "dark" {
		t.Errorf("Dump.Settings = %v, want {theme: dark}", d.Settings)
	}

	// An empty store dumps to empty (non-nil) containers.
	empty := newTestStore(t)
	d2, err := empty.Dump()
	if err != nil {
		t.Fatalf("Dump(empty): %v", err)
	}
	if d2.Files != nil || d2.Notes != nil || d2.Annotations != nil || d2.Links != nil {
		t.Errorf("Dump(empty) data = %+v, want nil slices", d2)
	}
	if d2.Settings == nil || len(d2.Settings) != 0 {
		t.Errorf("Dump(empty).Settings = %v, want empty map", d2.Settings)
	}
}

// TestRestoreBatchReplacesAllPreservingIDs pins the import contract:
// one transaction replaces every data table with the given records,
// storing ids and timestamps verbatim; settings absent from the batch
// survive; an empty batch wipes the data but not the settings.
func TestRestoreBatchReplacesAllPreservingIDs(t *testing.T) {
	s := newTestStore(t)
	const old = "C:/books/old.pdf"

	// Existing data that must disappear.
	if err := s.SetStatus(old, "reading"); err != nil {
		t.Fatalf("SetStatus: %v", err)
	}
	if _, err := s.AddNote(old, "doomed note", nil); err != nil {
		t.Fatalf("AddNote: %v", err)
	}
	if _, err := s.AddAnnotation(old, 1, "line", `{}`); err != nil {
		t.Fatalf("AddAnnotation: %v", err)
	}
	if err := s.SetSetting("theme", "dark"); err != nil {
		t.Fatalf("SetSetting(theme): %v", err)
	}

	// Handcrafted batch with fixed ids and timestamps — the everyday
	// API could never produce these, which is the point.
	page := 7
	batch := Dump{
		Files: []FileRec{
			{Path: "C:/books/a.pdf", Status: "done", Page: 3, TotalPages: 9, Starred: true, UpdatedAt: "2020-01-02T03:04:05Z"},
			{Path: "C:/books/b.pdf", Status: "todo", UpdatedAt: "2019-05-06T07:08:09Z"},
		},
		Notes: []Note{
			{ID: "note-fixed-1", FilePath: "C:/books/a.pdf", Text: "restored", Page: &page, TS: "2018-11-12T13:14:15Z", AnnIDs: []string{}},
			{ID: "note-fixed-2", FilePath: "C:/books/a.pdf", Text: "no page", TS: "2018-11-12T13:14:16Z", AnnIDs: []string{}},
		},
		Annotations: []Annotation{
			{ID: "ann-fixed-1", FilePath: "C:/books/a.pdf", Page: 4, Kind: "box", Data: `{"r":[1,1,1,1]}`, CreatedAt: "2017-01-01T00:00:00Z"},
		},
		Links:    [][2]string{{"note-fixed-1", "ann-fixed-1"}},
		Settings: map[string]string{"root_name": "books"},
	}
	if err := s.RestoreBatch(batch); err != nil {
		t.Fatalf("RestoreBatch: %v", err)
	}

	got, err := s.Dump()
	if err != nil {
		t.Fatalf("Dump(after restore): %v", err)
	}
	// The doomed record and its note/annotation are gone.
	for _, f := range got.Files {
		if f.Path == old {
			t.Errorf("old record survived restore: %+v", f)
		}
	}
	// Verbatim ids and timestamps. These two records satisfy the
	// files-table invariants, so RestoreBatch's normalisation is a
	// no-op for them (TestRestoreBatchNormalizesFileRecords pins the
	// repairing of records that do not).
	if len(got.Files) != 2 || got.Files[0] != batch.Files[0] || got.Files[1] != batch.Files[1] {
		t.Errorf("restored files = %+v, want %+v verbatim", got.Files, batch.Files)
	}
	if len(got.Notes) != 2 || got.Notes[0].ID != "note-fixed-1" || got.Notes[0].TS != "2018-11-12T13:14:15Z" {
		t.Errorf("restored notes = %+v, want fixed id/ts verbatim", got.Notes)
	}
	if got.Notes[0].Page == nil || *got.Notes[0].Page != 7 || got.Notes[1].Page != nil {
		t.Errorf("restored note pages = (%v, %v), want (7, nil)", got.Notes[0].Page, got.Notes[1].Page)
	}
	if len(got.Annotations) != 1 || got.Annotations[0].ID != "ann-fixed-1" || got.Annotations[0].CreatedAt != "2017-01-01T00:00:00Z" {
		t.Errorf("restored annotations = %+v, want fixed id/created_at verbatim", got.Annotations)
	}
	// The link resolves through the public read API with the restored
	// note's AnnIDs.
	notes, err := s.ListNotes("C:/books/a.pdf")
	if err != nil {
		t.Fatalf("ListNotes: %v", err)
	}
	if len(notes) != 2 || len(notes[0].AnnIDs) != 1 || notes[0].AnnIDs[0] != "ann-fixed-1" {
		t.Errorf("restored link not visible: %+v", notes)
	}
	// Settings: root_name upserted, theme untouched.
	if v, ok, err := s.Setting("root_name"); err != nil || !ok || v != "books" {
		t.Errorf("Setting(root_name) = (%q, %v, %v), want (\"books\", true, nil)", v, ok, err)
	}
	if v, ok, err := s.Setting("theme"); err != nil || !ok || v != "dark" {
		t.Errorf("Setting(theme) = (%q, %v, %v), want preserved (\"dark\", true, nil)", v, ok, err)
	}

	// An empty batch wipes the data tables but keeps the settings.
	if err := s.RestoreBatch(Dump{}); err != nil {
		t.Fatalf("RestoreBatch(empty): %v", err)
	}
	got, err = s.Dump()
	if err != nil {
		t.Fatalf("Dump(after wipe): %v", err)
	}
	if len(got.Files) != 0 || len(got.Notes) != 0 || len(got.Annotations) != 0 || len(got.Links) != 0 {
		t.Errorf("after empty restore, data = %+v, want all empty", got)
	}
	if got.Settings["root_name"] != "books" || got.Settings["theme"] != "dark" {
		t.Errorf("after empty restore, settings = %v, want both preserved", got.Settings)
	}
}

// TestRestoreBatchNormalizesFileRecords pins the files-table invariants
// at the RestoreBatch boundary: a batch whose file records violate what
// SetStatus/SetProgress/SetTotalPages enforce (page >= 0, total_pages
// >= 0, page <= total_pages, status in todo|reading|done|skip) is
// repaired on the way in, never stored. Ids/timestamps and the
// note/annotation rows stay verbatim.
func TestRestoreBatchNormalizesFileRecords(t *testing.T) {
	s := newTestStore(t)
	const (
		over  = "C:/books/over.pdf"  // page 15 > totalPages 10
		neg   = "C:/books/neg.pdf"   // page -3
		bogus = "C:/books/bogus.pdf" // status "bogus"
		empty = "C:/books/empty.pdf" // status "" and totalPages -1
	)
	batch := Dump{
		Files: []FileRec{
			{Path: over, Status: "reading", Page: 15, TotalPages: 10, Starred: true, UpdatedAt: "2020-01-02T03:04:05Z"},
			{Path: neg, Status: "done", Page: -3, TotalPages: 8, UpdatedAt: "2020-01-02T03:04:05Z"},
			{Path: bogus, Status: "bogus", Page: 0, TotalPages: 0, UpdatedAt: "2020-01-02T03:04:05Z"},
			{Path: empty, Status: "", Page: 7, TotalPages: -1, UpdatedAt: "2020-01-02T03:04:05Z"},
		},
		Notes: []Note{
			{ID: "note-verbatim", FilePath: over, Text: "kept", TS: "2018-11-12T13:14:15Z", AnnIDs: []string{}},
		},
	}
	if err := s.RestoreBatch(batch); err != nil {
		t.Fatalf("RestoreBatch: %v", err)
	}

	cases := []struct {
		path string
		want FileRec
	}{
		{over, FileRec{Path: over, Status: "reading", Page: 10, TotalPages: 10, Starred: true, UpdatedAt: "2020-01-02T03:04:05Z"}},
		{neg, FileRec{Path: neg, Status: "done", Page: 0, TotalPages: 8, UpdatedAt: "2020-01-02T03:04:05Z"}},
		{bogus, FileRec{Path: bogus, Status: "todo", Page: 0, TotalPages: 0, UpdatedAt: "2020-01-02T03:04:05Z"}},
		{empty, FileRec{Path: empty, Status: "todo", Page: 0, TotalPages: 0, UpdatedAt: "2020-01-02T03:04:05Z"}},
	}
	for _, tc := range cases {
		got, err := s.File(tc.path)
		if err != nil {
			t.Fatalf("File(%q): %v", tc.path, err)
		}
		if got != tc.want {
			t.Errorf("File(%q) = %+v, want normalised %+v", tc.path, got, tc.want)
		}
	}

	// ListFiles sees exactly the same four rows, all invariant-clean.
	files, err := s.ListFiles()
	if err != nil {
		t.Fatalf("ListFiles: %v", err)
	}
	if len(files) != len(cases) {
		t.Fatalf("ListFiles = %d rows, want %d", len(files), len(cases))
	}
	for _, f := range files {
		if f.Page < 0 || f.TotalPages < 0 || f.Page > f.TotalPages || !validStatuses[f.Status] {
			t.Errorf("ListFiles row violates the setter invariants: %+v", f)
		}
	}

	// Notes restore verbatim regardless of file normalisation.
	notes, err := s.ListNotes(over)
	if err != nil {
		t.Fatalf("ListNotes: %v", err)
	}
	if len(notes) != 1 || notes[0].ID != "note-verbatim" || notes[0].TS != "2018-11-12T13:14:15Z" {
		t.Errorf("restored notes = %+v, want note-verbatim with verbatim ts", notes)
	}
}

// mustSeedNote adds a note or fails the test.
func mustSeedNote(t *testing.T, s *Store, path, text string, page *int) Note {
	t.Helper()
	n, err := s.AddNote(path, text, page)
	if err != nil {
		t.Fatalf("AddNote(%q): %v", text, err)
	}
	return n
}
