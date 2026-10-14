// Package httpfiles serves library documents over the loopback HTTP server
// as GET /files/<relpath>, where relpath is relative to the configured root.
//
// It is the only place that decides whether a request path is inside the
// root, so the file server and the native "reveal in Explorer" endpoint
// cannot drift apart on the one check that must not be wrong.
package httpfiles

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Prefix is the URL path prefix this handler owns.
const Prefix = "/files/"

// Resolve returns the OS path for rel inside root, or an error if rel is
// empty-rooted or escapes root through "..", an absolute path, or any
// separator spelling.
func Resolve(root, rel string) (string, error) {
	if root == "" {
		return "", errors.New("httpfiles: no root configured")
	}
	rootFull := filepath.Clean(filepath.FromSlash(root))
	joined := filepath.Join(rootFull, fromURLPath(rel))
	rp, err := filepath.Rel(rootFull, joined)
	if err != nil {
		return "", fmt.Errorf("httpfiles: cannot place %q inside root: %w", rel, err)
	}
	if rp == ".." || strings.HasPrefix(rp, ".."+string(filepath.Separator)) || filepath.IsAbs(rp) {
		return "", fmt.Errorf("httpfiles: path escapes root: %s", rel)
	}
	return joined, nil
}

// fromURLPath turns a URL path segment into an OS path. A URL is always
// '/'-delimited, and '\' cannot appear in a Windows file name, so both are
// treated as delimiters: that keeps "..\" from smuggling an escape past a
// '/'-only check on platforms where '\' is not a separator.
func fromURLPath(rel string) string {
	return filepath.FromSlash(strings.ReplaceAll(rel, `\`, `/`))
}

// NewHandler builds the /files/* handler. getRoot is called per request so a
// root change takes effect without rebuilding the handler. Anything it cannot
// serve from inside the root is a 404: a missing root, an escaping path, a
// absent file, or a directory.
func NewHandler(getRoot func() string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			writeErrJSON(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		rel, ok := relOf(r.URL)
		if !ok {
			writeErrJSON(w, http.StatusNotFound, "not a file path")
			return
		}
		full, err := Resolve(getRoot(), rel)
		if err != nil {
			writeErrJSON(w, http.StatusNotFound, "not found")
			return
		}
		f, err := os.Open(full)
		if err != nil {
			writeErrJSON(w, http.StatusNotFound, "not found")
			return
		}
		defer f.Close()
		fi, err := f.Stat()
		if err != nil || fi.IsDir() {
			writeErrJSON(w, http.StatusNotFound, "not found")
			return
		}
		// Naming the PDF type explicitly keeps a Windows registry quirk from
		// talking pdf.js out of it; anything else in the root is served with
		// the type its own name advertises.
		if strings.EqualFold(filepath.Ext(fi.Name()), ".pdf") {
			w.Header().Set("Content-Type", "application/pdf")
		}
		// ServeContent mints an ETag only for If-Range requests, so set the
		// validator here: re-opening the same document then costs a 304.
		w.Header().Set("Etag", etagOf(fi))
		// ServeContent gives us Range (pdf.js fetches documents in pieces)
		// and Last-Modified, and honours the validators set above.
		http.ServeContent(w, r, fi.Name(), fi.ModTime(), f)
	})
}

// relOf strips the prefix from the decoded path, so percent-encoded traversal
// ("%2e%2e") arrives as the ".." it really means and gets refused.
func relOf(u *url.URL) (string, bool) {
	p := u.Path
	if !strings.HasPrefix(p, Prefix) {
		return "", false
	}
	return strings.TrimPrefix(p, Prefix), true
}

// etagOf is a strong validator over what actually changes a document: its
// size and modification time.
func etagOf(fi os.FileInfo) string {
	return strconv.Quote(fmt.Sprintf("%d-%d", fi.Size(), fi.ModTime().UnixNano()))
}

func writeErrJSON(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}
