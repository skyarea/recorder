package storage

import (
	"path/filepath"
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
