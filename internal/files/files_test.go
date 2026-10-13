package files

import (
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
