// Package files provides filesystem helpers for the PDF reader: recursive
// directory scanning and best-effort page-count sniffing.
package files

import (
	"io/fs"
	"path/filepath"
	"sort"
	"strings"
)

// FileMeta describes one discovered PDF file.
type FileMeta struct {
	Path string // relative to the scan root, '/'-separated
	Name string // base filename
}

// Scan recursively collects every *.pdf (case-insensitive) under root.
// Paths are relative to root and always use '/' separators. Results are
// de-duplicated (first wins) and sorted in natural (digit-aware) order.
// An unreadable root is returned as a non-nil error; unreadable entries
// below the root are skipped. Scan never panics.
func Scan(root string) ([]FileMeta, error) {
	return scan(root, filepath.WalkDir)
}

type walkFn func(root string, fn fs.WalkDirFunc) error

// scan runs the walk with an injectable walker so tests can simulate
// unreadable sub-entries portably (Windows os.Chmod cannot deny directory
// listing, and WalkDir never follows symlinks, so the real FS cannot
// produce a sub-entry walk error on demand).
func scan(root string, walk walkFn) ([]FileMeta, error) {
	var out []FileMeta
	seen := map[string]bool{}

	err := walk(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			if path == root {
				return err // missing/unreadable root must surface (Review Focus #1)
			}
			return nil // skip unreadable sub-entries, matching the JS reference
		}
		if d.IsDir() {
			return nil
		}
		name := d.Name()
		if !strings.EqualFold(filepath.Ext(name), ".pdf") {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if seen[rel] {
			return nil
		}
		seen[rel] = true
		out = append(out, FileMeta{Path: rel, Name: name})
		return nil
	})
	if err != nil {
		return nil, err
	}

	sort.Slice(out, func(i, j int) bool { return naturalLess(out[i].Path, out[j].Path) })
	return out, nil
}

// naturalLess compares two strings digit-aware: runs of digits compare by
// numeric value (p1 < p2 < p10), other runes compare by code point (UTF-8
// byte order, a rough zh/locale approximation).
func naturalLess(a, b string) bool {
	ia, ib := 0, 0
	for ia < len(a) && ib < len(b) {
		ca, cb := a[ia], b[ib]
		if isDigit(ca) && isDigit(cb) {
			ea := ia
			for ea < len(a) && isDigit(a[ea]) {
				ea++
			}
			eb := ib
			for eb < len(b) && isDigit(b[eb]) {
				eb++
			}
			na := strings.TrimLeft(a[ia:ea], "0")
			nb := strings.TrimLeft(b[ib:eb], "0")
			if len(na) != len(nb) {
				return len(na) < len(nb)
			}
			if na != nb {
				return na < nb
			}
			ia, ib = ea, eb
			continue
		}
		if ca != cb {
			return ca < cb
		}
		ia++
		ib++
	}
	return len(a)-ia < len(b)-ib
}

func isDigit(c byte) bool { return c >= '0' && c <= '9' }
