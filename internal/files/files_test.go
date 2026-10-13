package files

import (
	"io/fs"
	"os"
	"path/filepath"
	"testing"
)

// --- Scan ---

func TestScanCollectsPdfsWithSlashPaths(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "sub", "a"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "sub", "a", "b.PDF"), []byte("%PDF-1.4"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "x.txt"), []byte("not a pdf"), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := Scan(root)
	if err != nil {
		t.Fatalf("Scan(%q) error = %v", root, err)
	}
	if len(got) != 1 {
		t.Fatalf("Scan(%q) = %+v, want exactly 1 file", root, got)
	}
	if got[0].Path != "sub/a/b.PDF" {
		t.Errorf("Path = %q, want %q (forward slashes, relative)", got[0].Path, "sub/a/b.PDF")
	}
	if got[0].Name != "b.PDF" {
		t.Errorf("Name = %q, want %q", got[0].Name, "b.PDF")
	}
}

func TestScanSortsNaturally(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"p10.pdf", "p2.pdf", "p1.pdf"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte("%PDF-1.4"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	got, err := Scan(root)
	if err != nil {
		t.Fatalf("Scan(%q) error = %v", root, err)
	}
	want := []string{"p1.pdf", "p2.pdf", "p10.pdf"}
	if len(got) != len(want) {
		t.Fatalf("Scan(%q) = %+v, want %v", root, got, want)
	}
	for i, name := range want {
		if got[i].Path != name {
			t.Errorf("got[%d].Path = %q, want %q (natural order)", i, got[i].Path, name)
		}
	}
}

func TestScanMissingRoot(t *testing.T) {
	root := filepath.Join(t.TempDir(), "does-not-exist")
	got, err := Scan(root)
	if err == nil {
		t.Errorf("Scan(%q) error = nil, want non-nil error (got %+v)", root, got)
	}
}

// walkEntry is a minimal fs.DirEntry for simulating walk callbacks.
type walkEntry struct {
	name string
	dir  bool
}

func (e walkEntry) Name() string { return e.name }
func (e walkEntry) IsDir() bool  { return e.dir }
func (e walkEntry) Type() fs.FileMode {
	if e.dir {
		return fs.ModeDir
	}
	return 0
}
func (e walkEntry) Info() (fs.FileInfo, error) { return nil, fs.ErrInvalid }

func TestScanSkipsUnreadableSubEntries(t *testing.T) {
	// A real unreadable subdirectory cannot be simulated portably here:
	// Windows os.Chmod only toggles the read-only attribute (it does not
	// deny directory listing), and filepath.WalkDir never follows symlinks,
	// so even a dangling symlink yields no walk error (verified empirically).
	// Instead, drive scan's callback with a fake walker that reproduces
	// WalkDir's real behavior: for a directory whose ReadDir fails, the
	// callback is invoked a second time with the error, and the walk only
	// continues if the callback returns nil.
	root := filepath.Join(t.TempDir(), "root")
	fakeWalk := func(_ string, fn fs.WalkDirFunc) error {
		steps := []struct {
			path  string
			entry walkEntry
			err   error
		}{
			{root, walkEntry{name: "root", dir: true}, nil},
			{filepath.Join(root, "locked"), walkEntry{name: "locked", dir: true}, fs.ErrPermission},
			{filepath.Join(root, "a.pdf"), walkEntry{name: "a.pdf"}, nil},
			{filepath.Join(root, "sub"), walkEntry{name: "sub", dir: true}, nil},
			{filepath.Join(root, "sub", "b.pdf"), walkEntry{name: "b.pdf"}, nil},
		}
		for _, s := range steps {
			if err := fn(s.path, s.entry, s.err); err != nil {
				return err // WalkDir aborts and propagates the callback's error
			}
		}
		return nil // walk ran to completion because no callback returned an error
	}

	got, err := scan(root, fakeWalk)
	if err != nil {
		t.Fatalf("scan(root, fakeWalk) error = %v, want nil (unreadable sub-entry must not abort)", err)
	}
	want := []FileMeta{
		{Path: "a.pdf", Name: "a.pdf"},
		{Path: "sub/b.pdf", Name: "b.pdf"},
	}
	if len(got) != len(want) {
		t.Fatalf("scan(root, fakeWalk) = %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("got[%d] = %+v, want %+v", i, got[i], want[i])
		}
	}
}

// --- SniffPageCount ---

func TestSniffPageCount(t *testing.T) {
	t.Run("fake pdf with /Count 42", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "fake.pdf")
		content := []byte("%PDF-1.4\n1 0 obj\n<< /Type /Pages /Count 42 /Kids [] >>\nendobj\ntrailer\n%%EOF\n")
		if err := os.WriteFile(path, content, 0o644); err != nil {
			t.Fatal(err)
		}
		got, err := SniffPageCount(path)
		if err != nil {
			t.Fatalf("SniffPageCount(%q) error = %v", path, err)
		}
		if got != 42 {
			t.Errorf("SniffPageCount(%q) = %d, want 42", path, got)
		}
	})

	t.Run("empty file", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "empty.pdf")
		if err := os.WriteFile(path, nil, 0o644); err != nil {
			t.Fatal(err)
		}
		got, err := SniffPageCount(path)
		if err != nil {
			t.Fatalf("SniffPageCount(%q) error = %v", path, err)
		}
		if got != 0 {
			t.Errorf("SniffPageCount(%q) = %d, want 0", path, got)
		}
	})
}

// Supplementary hardening beyond the brief's four core tests.

func TestSniffPageCountNoCount(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nocount.pdf")
	if err := os.WriteFile(path, []byte("%PDF-1.4\njust some text, no count here\n%%EOF\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := SniffPageCount(path)
	if err != nil {
		t.Fatalf("SniffPageCount(%q) error = %v", path, err)
	}
	if got != 0 {
		t.Errorf("SniffPageCount(%q) = %d, want 0", path, got)
	}
}

func TestSniffPageCountLargestMatchWins(t *testing.T) {
	path := filepath.Join(t.TempDir(), "multi.pdf")
	content := []byte("%PDF-1.4\n/Count 5\n/Count 123\n/Count 17\n%%EOF\n")
	if err := os.WriteFile(path, content, 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := SniffPageCount(path)
	if err != nil {
		t.Fatalf("SniffPageCount(%q) error = %v", path, err)
	}
	if got != 123 {
		t.Errorf("SniffPageCount(%q) = %d, want 123 (largest match wins)", path, got)
	}
}

func TestSniffPageCountRejectsHugeCount(t *testing.T) {
	path := filepath.Join(t.TempDir(), "huge.pdf")
	content := []byte("%PDF-1.4\n/Count 200000\n/Count 10\n%%EOF\n")
	if err := os.WriteFile(path, content, 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := SniffPageCount(path)
	if err != nil {
		t.Fatalf("SniffPageCount(%q) error = %v", path, err)
	}
	if got != 10 {
		t.Errorf("SniffPageCount(%q) = %d, want 10 (counts >= 200000 rejected)", path, got)
	}
}

func TestSniffPageCountMissingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nope.pdf")
	got, err := SniffPageCount(path)
	if got != 0 {
		t.Errorf("SniffPageCount(%q) = %d, want 0", path, got)
	}
	if err != nil {
		t.Errorf("SniffPageCount(%q) error = %v, want nil (best-effort graceful)", path, err)
	}
}
