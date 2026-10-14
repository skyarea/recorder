package httpfiles

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func write(t *testing.T, root, rel, content string) string {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func server(getRoot func() string) *httptest.Server {
	srv := httptest.NewServer(NewHandler(getRoot))
	return srv
}

func fetch(t *testing.T, srv *httptest.Server, path string, hdr map[string]string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, srv.URL+path, nil)
	if err != nil {
		t.Fatal(err)
	}
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	// Redirects must never be followed: a traversal attempt has to be
	// refused outright, not bounced to another URL.
	noRedirect := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	resp, err := noRedirect.Do(req)
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	return resp
}

func TestServesFileUnderRoot(t *testing.T) {
	root := t.TempDir()
	write(t, root, "sub/a.pdf", "%PDF-1.4 hello world\n")
	srv := server(func() string { return root })
	defer srv.Close()

	resp := fetch(t, srv, "/files/sub/a.pdf", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(body), "%PDF-1.4 hello world\n"; got != want {
		t.Fatalf("body = %q, want %q", got, want)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "application/pdf" {
		t.Fatalf("Content-Type = %q, want application/pdf", ct)
	}
}

func TestFilesRejectsTraversal(t *testing.T) {
	root := t.TempDir()
	write(t, root, "a.pdf", "%PDF inside")
	// A file that sits next to the root: reading it back would prove an escape.
	elsewhere := filepath.Dir(root)
	write(t, elsewhere, "secret.pdf", "TOP SECRET")

	srv := server(func() string { return root })
	defer srv.Close()

	// Every spelling of "get out of the root" must 404, including the
	// percent-encoded variants that reach us already decoded.
	for _, path := range []string{
		"/files/../secret.pdf",
		"/files/../../secret.pdf",
		"/files/sub/../../secret.pdf",
		"/files/%2e%2e/%2e%2e/secret.pdf",
		"/files/%2E%2E/%2E%2E/secret.pdf",
		"/files/..%2f..%2fsecret.pdf",
		"/files/%2e%2e%2f%2e%2e%2fsecret.pdf",
		"/files/..%2f..%5csecret.pdf",
		`/files/..\..\secret.pdf`,
		`/files\..\..\secret.pdf`,
		"/files/%252e%252e/secret.pdf",
		"/files/secret.pdf",
		"/files/",
		"/files",
	} {
		resp := fetch(t, srv, path, nil)
		if resp.StatusCode != http.StatusNotFound {
			t.Fatalf("GET %s = %d, want 404", path, resp.StatusCode)
		}
		body, _ := io.ReadAll(resp.Body)
		if strings.Contains(string(body), "TOP SECRET") {
			t.Fatalf("GET %s leaked root-external content: %q", path, body)
		}
	}
}

func TestFilesMissingRootOrFile404(t *testing.T) {
	root := t.TempDir()
	write(t, root, "a.pdf", "%PDF inside")

	// No root configured at all.
	noRoot := server(func() string { return "" })
	defer noRoot.Close()
	if resp := fetch(t, noRoot, "/files/a.pdf", nil); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("empty root = %d, want 404", resp.StatusCode)
	}

	// Root set, file absent, root itself a directory.
	srv := server(func() string { return root })
	defer srv.Close()
	if resp := fetch(t, srv, "/files/nope.pdf", nil); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("missing file = %d, want 404", resp.StatusCode)
	}
	// A directory is not a document.
	if resp := fetch(t, srv, "/files/", nil); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("directory = %d, want 404", resp.StatusCode)
	}

	// Root vanished from disk since it was configured.
	gone := server(func() string { return filepath.Join(root, "not-a-directory") })
	defer gone.Close()
	if resp := fetch(t, gone, "/files/a.pdf", nil); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("stale root = %d, want 404", resp.StatusCode)
	}
}

func TestFilesRangeRequest(t *testing.T) {
	root := t.TempDir()
	write(t, root, "big.pdf", "0123456789ABCDEF")
	srv := server(func() string { return root })
	defer srv.Close()

	resp := fetch(t, srv, "/files/big.pdf", map[string]string{"Range": "bytes=2-5"})
	if resp.StatusCode != http.StatusPartialContent {
		t.Fatalf("status = %d, want 206", resp.StatusCode)
	}
	if got, want := resp.Header.Get("Content-Range"), "bytes 2-5/16"; got != want {
		t.Fatalf("Content-Range = %q, want %q", got, want)
	}
	if cl := resp.Header.Get("Content-Length"); cl != "4" {
		t.Fatalf("Content-Length = %q, want 4", cl)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(body), "2345"; got != want {
		t.Fatalf("range body = %q, want %q", got, want)
	}

	// An unsatisfiable range is a 416, not a silent full body.
	if resp := fetch(t, srv, "/files/big.pdf", map[string]string{"Range": "bytes=99-200"}); resp.StatusCode != http.StatusRequestedRangeNotSatisfiable {
		t.Fatalf("out-of-range status = %d, want 416", resp.StatusCode)
	}
}

func TestFilesAdvertisesCachingValidators(t *testing.T) {
	root := t.TempDir()
	write(t, root, "a.pdf", "%PDF-1.4")
	srv := server(func() string { return root })
	defer srv.Close()

	resp := fetch(t, srv, "/files/a.pdf", nil)
	if resp.Header.Get("Last-Modified") == "" {
		t.Fatal("missing Last-Modified header")
	}
	etag := resp.Header.Get("Etag")
	if etag == "" {
		t.Fatal("missing Etag header")
	}

	// The ETag must round-trip into a 304, which is what lets pdf.js
	// re-open the same document without re-fetching it.
	if resp2 := fetch(t, srv, "/files/a.pdf", map[string]string{"If-None-Match": etag}); resp2.StatusCode != http.StatusNotModified {
		t.Fatalf("If-None-Match status = %d, want 304", resp2.StatusCode)
	}
}

// The root is a folder the user picked, so it can hold anything. Only a PDF is
// called a PDF; the rest is served with the type its own name advertises.
func TestFilesContentTypeFollowsTheName(t *testing.T) {
	root := t.TempDir()
	write(t, root, "notes.txt", "hello")
	write(t, root, "doc.PDF", "%PDF-1.4")
	srv := server(func() string { return root })
	defer srv.Close()

	for path, want := range map[string]string{
		"/files/notes.txt": "text/plain",
		"/files/doc.PDF":   "application/pdf",
	} {
		resp := fetch(t, srv, path, nil)
		if got := resp.Header.Get("Content-Type"); !strings.HasPrefix(got, want) {
			t.Fatalf("GET %s Content-Type = %q, want %s", path, got, want)
		}
	}
}

func TestResolveRejectsEscapeButKeepsNested(t *testing.T) {
	root := t.TempDir()
	sub := filepath.Join(root, "sub")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}

	for _, rel := range []string{"a.pdf", "sub/a.pdf", "./a.pdf", `sub\a.pdf`, "/a.pdf", "sub/../a.pdf"} {
		if _, err := Resolve(root, rel); err != nil {
			t.Fatalf("Resolve(%q) = %v, want success", rel, err)
		}
	}
	for _, rel := range []string{"../a.pdf", "sub/../../a.pdf", `..\a.pdf`} {
		if _, err := Resolve(root, rel); err == nil {
			t.Fatalf("Resolve(%q) succeeded, want escape error", rel)
		}
	}
	if _, err := Resolve("", "a.pdf"); err == nil {
		t.Fatal("Resolve with empty root succeeded, want error")
	}
}
