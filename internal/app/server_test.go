package app

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
)

// newTestApp returns an app wired to a temp DB plus an httptest server
// exposing Routes(). Dialog hooks are fakes; the frontend is a stub FS.
func newTestApp(t *testing.T) (*App, *httptest.Server, string) {
	t.Helper()
	root := t.TempDir()
	dbPath := filepath.Join(t.TempDir(), "recorder.db")
	a, err := New(dbPath)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = a.Close() })

	frontend := fstest.MapFS{
		"frontend/index.html":                  {Data: []byte("<html>real</html>")},
		"frontend/vendor/pdfjs/web/viewer.mjs": {Data: []byte("export const viewer = 1;\n")},
	}
	srv := httptest.NewServer(a.Routes(frontend))
	t.Cleanup(srv.Close)
	return a, srv, root
}

func get(t *testing.T, srv *httptest.Server, path string) (*http.Response, map[string]any) {
	t.Helper()
	resp, err := http.Get(srv.URL + path)
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	var body map[string]any
	if resp.StatusCode < 400 {
		if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
			t.Fatalf("decode body of GET %s: %v", path, err)
		}
	}
	resp.Body.Close()
	return resp, body
}

func post(t *testing.T, srv *httptest.Server, path string, payload any) (*http.Response, map[string]any) {
	t.Helper()
	var data []byte
	if s, ok := payload.(string); ok {
		data = []byte(s)
	} else {
		data, _ = json.Marshal(payload)
	}
	resp, err := http.Post(srv.URL+path, "application/json", strings.NewReader(string(data)))
	if err != nil {
		t.Fatalf("POST %s: %v", path, err)
	}
	var body map[string]any
	if resp.StatusCode < 400 {
		if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
			t.Fatalf("decode body of POST %s: %v", path, err)
		}
	} else {
		if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
			t.Fatalf("error body of POST %s is not JSON: %v", path, err)
		}
	}
	resp.Body.Close()
	return resp, body
}

func makePdfs(t *testing.T, root string, rels ...string) {
	t.Helper()
	for _, rel := range rels {
		p := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("%PDF-1.4\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func stateOf(t *testing.T, srv *httptest.Server) map[string]any {
	t.Helper()
	resp, st := get(t, srv, "/api/state")
	if resp.StatusCode != 200 {
		t.Fatalf("GET /api/state: %d", resp.StatusCode)
	}
	return st
}

func recordsOf(t *testing.T, st map[string]any) map[string]any {
	t.Helper()
	recs, ok := st["records"].(map[string]any)
	if !ok {
		t.Fatalf("records missing from state %v", st)
	}
	return recs
}

func TestStateShapeAfterScan(t *testing.T) {
	a, srv, root := newTestApp(t)
	makePdfs(t, root, "a.pdf", "b/c.pdf")
	if err := a.SetRoot(root); err != nil {
		t.Fatalf("SetRoot: %v", err)
	}

	st := stateOf(t, srv)
	if st["hasRoot"] != true {
		t.Errorf("hasRoot = %v, want true", st["hasRoot"])
	}
	if st["rootPath"] != filepath.ToSlash(root) && st["rootPath"] != root {
		t.Errorf("rootPath = %v", st["rootPath"])
	}
	if st["rootName"] != filepath.Base(root) {
		t.Errorf("rootName = %v, want %v", st["rootName"], filepath.Base(root))
	}
	scanned, ok := st["scanned"].([]any)
	if !ok || len(scanned) != 2 {
		t.Fatalf("scanned = %v, want 2 entries", st["scanned"])
	}
	first := scanned[0].(map[string]any)
	if first["path"] != "a.pdf" || first["name"] != "a.pdf" {
		t.Errorf("scanned[0] = %v", first)
	}
	recs := recordsOf(t, st)
	for _, p := range []string{"a.pdf", "b/c.pdf"} {
		rec, ok := recs[p].(map[string]any)
		if !ok {
			t.Fatalf("records[%q] missing", p)
		}
		if rec["status"] != "todo" {
			t.Errorf("records[%q].status = %v, want todo", p, rec["status"])
		}
		if notes, ok := rec["notes"].([]any); !ok || len(notes) != 0 {
			t.Errorf("records[%q].notes = %v, want []", p, rec["notes"])
		}
	}
	ui, ok := st["ui"].(map[string]any)
	if !ok {
		t.Fatalf("ui missing")
	}
	if ui["filter"] != "" || ui["notesOpen"] != false {
		t.Errorf("ui = %v, want defaults", ui)
	}
	if collapsed, ok := ui["collapsed"].([]any); !ok || len(collapsed) != 0 {
		t.Errorf("ui.collapsed = %v, want []", ui["collapsed"])
	}
	if st["orphanCount"] != float64(0) {
		t.Errorf("orphanCount = %v, want 0", st["orphanCount"])
	}
}

func TestStateWithoutRoot(t *testing.T) {
	_, srv, _ := newTestApp(t)
	st := stateOf(t, srv)
	if st["hasRoot"] != false {
		t.Errorf("hasRoot = %v, want false", st["hasRoot"])
	}
	if scanned, ok := st["scanned"].([]any); !ok || len(scanned) != 0 {
		t.Errorf("scanned = %v, want []", st["scanned"])
	}
}

// Review Focus #1: a saved root that has vanished must not crash startup;
// records survive and the UI is told (hasRoot=false + rootPath kept).
func TestRootMissingSurvivesStartup(t *testing.T) {
	root := t.TempDir()
	dbPath := filepath.Join(t.TempDir(), "recorder.db")
	a, err := New(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = a.Close() }()

	makePdfs(t, root, "keep.pdf")
	if err := a.SetRoot(root); err != nil {
		t.Fatal(err)
	}
	if err := a.store.SetStatus("keep.pdf", "reading"); err != nil {
		t.Fatal(err)
	}

	frontend := fstest.MapFS{"frontend/index.html": {Data: []byte("x")}}
	srv := httptest.NewServer(a.Routes(frontend))
	defer srv.Close()

	gone := filepath.Join(root, "deleted-dir")
	if err := a.store.SetSetting("root_path", filepath.ToSlash(gone)); err != nil {
		t.Fatal(err)
	}
	if err := a.LoadRoot(); err != nil {
		t.Fatalf("LoadRoot on missing dir must not error: %v", err)
	}

	st := stateOf(t, srv)
	if st["hasRoot"] != false {
		t.Errorf("hasRoot = %v, want false for vanished dir", st["hasRoot"])
	}
	if st["rootPath"] == "" {
		t.Errorf("rootPath should still report the vanished dir for the UI to prompt re-selection")
	}
	recs := recordsOf(t, st)
	rec, ok := recs["keep.pdf"].(map[string]any)
	if !ok || rec["status"] != "reading" {
		t.Errorf("saved progress lost: records[keep.pdf] = %v", recs["keep.pdf"])
	}
	if st["orphanCount"] != float64(1) {
		t.Errorf("orphanCount = %v, want 1", st["orphanCount"])
	}
}

func TestRootChangeRescans(t *testing.T) {
	a, srv, root1 := newTestApp(t)
	makePdfs(t, root1, "one.pdf")
	resp, st := post(t, srv, "/api/root", map[string]any{"path": root1})
	if resp.StatusCode != 200 {
		t.Fatalf("POST /api/root: %d %v", resp.StatusCode, st)
	}
	if len(st["scanned"].([]any)) != 1 {
		t.Fatalf("scanned = %v", st["scanned"])
	}

	root2 := t.TempDir()
	makePdfs(t, root2, "two.pdf", "three.pdf")
	resp, st = post(t, srv, "/api/root", map[string]any{"path": root2})
	if resp.StatusCode != 200 {
		t.Fatalf("POST /api/root 2: %d", resp.StatusCode)
	}
	if len(st["scanned"].([]any)) != 2 {
		t.Errorf("scanned after root change = %v, want 2", st["scanned"])
	}

	_ = a
}

func TestRootMissingPath400(t *testing.T) {
	_, srv, root := newTestApp(t)
	gone := filepath.Join(root, "nope")
	resp, body := post(t, srv, "/api/root", map[string]any{"path": gone})
	if resp.StatusCode != 400 {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
	if e, ok := body["error"].(string); !ok || e == "" {
		t.Fatalf("error body = %v", body)
	}
}

func TestMutationsRoundTrip(t *testing.T) {
	_, srv, root := newTestApp(t)
	makePdfs(t, root, "a.pdf")
	_, st := post(t, srv, "/api/root", map[string]any{"path": root})
	if sc, ok := st["scanned"].([]any); !ok || len(sc) != 1 {
		t.Fatalf("setup scan: %v", st["scanned"])
	}

	// status
	resp, st := post(t, srv, "/api/status", map[string]any{"path": "a.pdf", "status": "reading"})
	if resp.StatusCode != 200 || recOf(st, "a.pdf")["status"] != "reading" {
		t.Fatalf("status mutation: %d %v", resp.StatusCode, st)
	}
	resp, body := post(t, srv, "/api/status", map[string]any{"path": "a.pdf", "status": "bogus"})
	if resp.StatusCode < 400 || body["error"] == nil {
		t.Fatalf("bad status must 4xx+error: %d %v", resp.StatusCode, body)
	}

	// star
	post(t, srv, "/api/star", map[string]any{"path": "a.pdf", "starred": true})
	if recOf(stateOf(t, srv), "a.pdf")["starred"] != true {
		t.Fatal("star round trip")
	}

	// progress + totalpages
	post(t, srv, "/api/progress", map[string]any{"path": "a.pdf", "page": 7})
	post(t, srv, "/api/totalpages", map[string]any{"path": "a.pdf", "totalPages": 42})
	rec := recOf(stateOf(t, srv), "a.pdf")
	if rec["page"] != float64(7) || rec["totalPages"] != float64(42) {
		t.Fatalf("progress/totalPages = %v", rec)
	}

	// notes add/update/delete
	resp, note := post(t, srv, "/api/notes", map[string]any{"path": "a.pdf", "text": "hello", "page": 3})
	if resp.StatusCode != 200 || note["text"] != "hello" {
		t.Fatalf("add note: %d %v", resp.StatusCode, note)
	}
	id, _ := note["id"].(string)
	if id == "" {
		t.Fatal("note id missing")
	}
	post(t, srv, "/api/notes/update", map[string]any{"id": id, "text": "edited"})
	rec = recOf(stateOf(t, srv), "a.pdf")
	notes := rec["notes"].([]any)
	if len(notes) != 1 || notes[0].(map[string]any)["text"] != "edited" {
		t.Fatalf("note update: %v", notes)
	}
	if annIDs, ok := notes[0].(map[string]any)["annIds"].([]any); !ok || len(annIDs) != 0 {
		t.Fatalf("note.annIds = %v, want []", notes[0].(map[string]any)["annIds"])
	}

	// annotations list/add
	resp, ann := post(t, srv, "/api/annotations", map[string]any{"path": "a.pdf", "page": 1, "kind": "highlight", "data": `{"rects":[]}`})
	if resp.StatusCode != 200 || ann["kind"] != "highlight" {
		t.Fatalf("add annotation: %d %v", resp.StatusCode, ann)
	}
	annID, _ := ann["id"].(string)
	_, list := get(t, srv, "/api/annotations?path=a.pdf")
	if arr, ok := list["annotations"].([]any); !ok || len(arr) != 1 {
		t.Fatalf("GET annotations = %v", list)
	}

	// link + note sees annIds; unlink
	if resp, _ := post(t, srv, "/api/links", map[string]any{"noteId": id, "annotationId": annID}); resp.StatusCode != 200 {
		t.Fatalf("link: %d", resp.StatusCode)
	}
	rec = recOf(stateOf(t, srv), "a.pdf")
	notes = rec["notes"].([]any)
	if annIDs := notes[0].(map[string]any)["annIds"].([]any); len(annIDs) != 1 || annIDs[0] != annID {
		t.Fatalf("annIds after link = %v", annIDs)
	}
	post(t, srv, "/api/links/delete", map[string]any{"noteId": id, "annotationId": annID})
	rec = recOf(stateOf(t, srv), "a.pdf")
	if annIDs := rec["notes"].([]any)[0].(map[string]any)["annIds"].([]any); len(annIDs) != 0 {
		t.Fatalf("annIds after unlink = %v", annIDs)
	}

	// annotation delete
	if resp, _ := post(t, srv, "/api/annotations/delete", map[string]any{"id": annID}); resp.StatusCode != 200 {
		t.Fatalf("annotation delete: %d", resp.StatusCode)
	}

	// note delete
	if resp, _ := post(t, srv, "/api/notes/delete", map[string]any{"id": id}); resp.StatusCode != 200 {
		t.Fatalf("note delete: %d", resp.StatusCode)
	}
	rec = recOf(stateOf(t, srv), "a.pdf")
	if n := rec["notes"].([]any); len(n) != 0 {
		t.Fatalf("notes after delete = %v", n)
	}

	// ui state
	if resp, st := post(t, srv, "/api/ui", map[string]any{"collapsed": []string{"b"}, "filter": "reading", "notesOpen": true}); resp.StatusCode != 200 {
		t.Fatalf("ui: %d %v", resp.StatusCode, st)
	} else {
		ui := st["ui"].(map[string]any)
		if ui["filter"] != "reading" || ui["notesOpen"] != true {
			t.Fatalf("ui not persisted: %v", ui)
		}
		if c := ui["collapsed"].([]any); len(c) != 1 || c[0] != "b" {
			t.Fatalf("ui.collapsed = %v", ui["collapsed"])
		}
	}
}

func recOf(st map[string]any, path string) map[string]any {
	recs, ok := st["records"].(map[string]any)
	if !ok {
		panic("no records")
	}
	rec, ok := recs[path].(map[string]any)
	if !ok {
		panic("record missing for " + path)
	}
	return rec
}

func TestOrphanCount(t *testing.T) {
	_, srv, root := newTestApp(t)
	makePdfs(t, root, "here.pdf")
	post(t, srv, "/api/root", map[string]any{"path": root})

	// gone.pdf: substantive (reading) and not scanned -> orphan
	post(t, srv, "/api/status", map[string]any{"path": "gone.pdf", "status": "reading"})
	// starred only -> orphan
	post(t, srv, "/api/star", map[string]any{"path": "starred.pdf", "starred": true})
	// page>0 only -> orphan
	post(t, srv, "/api/progress", map[string]any{"path": "paged.pdf", "page": 5})
	// note only -> orphan
	post(t, srv, "/api/notes", map[string]any{"path": "noted.pdf", "text": "t", "page": nil})
	// todo default with no content -> NOT orphan
	post(t, srv, "/api/status", map[string]any{"path": "quiet.pdf", "status": "todo"})

	st := stateOf(t, srv)
	if st["orphanCount"] != float64(4) {
		t.Fatalf("orphanCount = %v, want 4 (state: %v)", st["orphanCount"], st)
	}
}

func TestImportExportEndpoints(t *testing.T) {
	a, srv, root := newTestApp(t)
	exportDir := t.TempDir()
	a.exportDir = exportDir
	a.saveAs = func(defaultName string) (string, error) {
		return filepath.Join(exportDir, defaultName), nil
	}

	makePdfs(t, root, "a.pdf")
	post(t, srv, "/api/root", map[string]any{"path": root})
	post(t, srv, "/api/status", map[string]any{"path": "a.pdf", "status": "done"})
	post(t, srv, "/api/notes", map[string]any{"path": "a.pdf", "text": "n", "page": nil})

	resp, body := post(t, srv, "/api/export", map[string]any{})
	if resp.StatusCode != 200 {
		t.Fatalf("export: %d %v", resp.StatusCode, body)
	}
	path, _ := body["path"].(string)
	if path == "" {
		t.Fatalf("export path missing: %v", body)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("exported file unreadable: %v", err)
	}
	if !strings.Contains(string(data), `"version"`) {
		t.Fatalf("exported JSON lacks legacy shape: %.200s", data)
	}

	// merge import of the same payload: ids preserved, nothing new -> imported counts records applied
	resp, body = post(t, srv, "/api/import?replace=false", string(data))
	if resp.StatusCode != 200 {
		t.Fatalf("import merge: %d %v", resp.StatusCode, body)
	}
	if _, ok := body["imported"]; !ok {
		t.Fatalf("import body = %v", body)
	}

	// replace import
	resp, body = post(t, srv, "/api/import?replace=true", string(data))
	if resp.StatusCode != 200 {
		t.Fatalf("import replace: %d %v", resp.StatusCode, body)
	}

	// bad JSON -> 4xx with error
	resp, body = post(t, srv, "/api/import", "not json")
	if resp.StatusCode < 400 || body["error"] == nil {
		t.Fatalf("bad import must 4xx+error: %d %v", resp.StatusCode, body)
	}

	// dialog unavailable -> fallback file under exportDir named 阅读进度-YYYYMMDD.json
	a.saveAs = func(string) (string, error) { return "", errDialogUnavailable }
	resp, body = post(t, srv, "/api/export", map[string]any{})
	if resp.StatusCode != 200 {
		t.Fatalf("fallback export: %d %v", resp.StatusCode, body)
	}
	fbPath, _ := body["path"].(string)
	if !strings.HasPrefix(filepath.Base(fbPath), "阅读进度-") || !strings.HasSuffix(fbPath, ".json") {
		t.Fatalf("fallback name = %v", fbPath)
	}
	if _, err := os.Stat(fbPath); err != nil {
		t.Fatalf("fallback file missing: %v", err)
	}
}

func TestReveal(t *testing.T) {
	a, srv, root := newTestApp(t)
	makePdfs(t, root, "sub/a.pdf")
	post(t, srv, "/api/root", map[string]any{"path": root})

	var revealed string
	a.reveal = func(fullPath string) error {
		revealed = fullPath
		return nil
	}

	resp, _ := post(t, srv, "/api/reveal", map[string]any{"path": "sub/a.pdf"})
	if resp.StatusCode != 200 {
		t.Fatalf("reveal: %d", resp.StatusCode)
	}
	want := filepath.Join(root, "sub", "a.pdf")
	if filepath.Clean(revealed) != filepath.Clean(want) {
		t.Fatalf("revealed %q, want %q", revealed, want)
	}

	// traversal outside root is rejected
	resp, body := post(t, srv, "/api/reveal", map[string]any{"path": "../escape.pdf"})
	if resp.StatusCode < 400 || body["error"] == nil {
		t.Fatalf("traversal must 4xx+error: %d %v", resp.StatusCode, body)
	}

	// no root configured -> error
	a2, srv2, _ := newTestApp(t)
	a2.reveal = func(string) error { return nil }
	resp, body = post(t, srv2, "/api/reveal", map[string]any{"path": "a.pdf"})
	if resp.StatusCode < 400 || body["error"] == nil {
		t.Fatalf("reveal without root must error: %d %v", resp.StatusCode, body)
	}
}

func TestUnknownRouteAndBadBodies(t *testing.T) {
	_, srv, root := newTestApp(t)
	post(t, srv, "/api/root", map[string]any{"path": root})

	resp, body := get(t, srv, "/api/nope")
	if resp.StatusCode != 404 {
		t.Fatalf("unknown /api route = %d", resp.StatusCode)
	}
	if body != nil && body["error"] == nil && resp.StatusCode >= 400 {
		t.Fatalf("404 body must be {error}: %v", body)
	}

	// every mutation endpoint rejects a malformed body with 4xx {"error"}
	for _, ep := range []string{
		"/api/root", "/api/status", "/api/star", "/api/progress", "/api/totalpages",
		"/api/notes", "/api/notes/update", "/api/notes/delete", "/api/annotations",
		"/api/annotations/delete", "/api/links", "/api/links/delete", "/api/ui",
		"/api/reveal",
	} {
		resp, body = post(t, srv, ep, "{not json")
		if resp.StatusCode != 400 || body["error"] == nil {
			t.Fatalf("bad JSON body at %s must 400+error: %d %v", ep, resp.StatusCode, body)
		}
	}

	resp, body = post(t, srv, "/api/status", map[string]any{"path": "x.pdf"})
	if resp.StatusCode < 400 || body["error"] == nil {
		t.Fatalf("missing field must 4xx+error: %d %v", resp.StatusCode, body)
	}

	resp, body = post(t, srv, "/api/annotations", map[string]any{"path": "x.pdf", "page": 1, "kind": "circle", "data": "{}"})
	if resp.StatusCode < 400 || body["error"] == nil {
		t.Fatalf("bad annotation kind must 4xx+error: %d %v", resp.StatusCode, body)
	}
}

func TestRootNameUsesSetting(t *testing.T) {
	a, srv, root := newTestApp(t)
	post(t, srv, "/api/root", map[string]any{"path": root})
	if err := a.store.SetSetting("root_name", "自定义库"); err != nil {
		t.Fatal(err)
	}
	if name := stateOf(t, srv)["rootName"]; name != "自定义库" {
		t.Fatalf("rootName = %v, want the persisted setting", name)
	}
}

func TestStaticFrontendServed(t *testing.T) {
	_, srv, _ := newTestApp(t)
	resp, err := http.Get(srv.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("GET / = %d", resp.StatusCode)
	}
	buf := make([]byte, 64)
	n, _ := resp.Body.Read(buf)
	if !strings.Contains(string(buf[:n]), "<html>real</html>") {
		t.Fatalf("index not served: %q", buf[:n])
	}
}

// getNoRedirect refuses to follow a redirect: a traversal attempt has to be
// answered with 404 here and now, not bounced to some cleaned path.
func getNoRedirect(t *testing.T, url string) *http.Response {
	t.Helper()
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	resp, err := client.Get(url)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	return resp
}

func TestFilesRouteServesPDF(t *testing.T) {
	_, srv, root := newTestApp(t)
	makePdfs(t, root, "sub/a.pdf")
	post(t, srv, "/api/root", map[string]any{"path": root})

	resp, err := http.Get(srv.URL + "/files/sub/a.pdf")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("GET /files/sub/a.pdf = %d, want 200", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "application/pdf" {
		t.Fatalf("Content-Type = %q, want application/pdf", ct)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(body), "%PDF-1.4") {
		t.Fatalf("body = %q, want PDF bytes", body)
	}
}

func TestFilesRouteRejectsTraversal(t *testing.T) {
	_, srv, root := newTestApp(t)
	makePdfs(t, root, "a.pdf")
	// A file just outside the root: if it ever comes back in a response,
	// the containment check failed.
	secret := filepath.Join(filepath.Dir(root), "secret.pdf")
	if err := os.WriteFile(secret, []byte("TOP SECRET"), 0o644); err != nil {
		t.Fatal(err)
	}
	post(t, srv, "/api/root", map[string]any{"path": root})

	for _, path := range []string{
		"/files/../secret.pdf",
		"/files/../../secret.pdf",
		"/files/%2e%2e/%2e%2e/secret.pdf",
		"/files/..%2f..%2fsecret.pdf",
		"/files/%2e%2e%5c%2e%2e%5csecret.pdf",
	} {
		resp := getNoRedirect(t, srv.URL+path)
		if resp.StatusCode != 404 {
			t.Fatalf("GET %s = %d, want a direct 404 (no redirect)", path, resp.StatusCode)
		}
	}

	// With no root configured the route must stay shut as well.
	_, srv2, _ := newTestApp(t)
	if resp := getNoRedirect(t, srv2.URL+"/files/a.pdf"); resp.StatusCode != 404 {
		t.Fatalf("GET /files with no root = %d, want 404", resp.StatusCode)
	}
}

func TestStaticServesMjsAsJavascript(t *testing.T) {
	_, srv, _ := newTestApp(t)
	resp, err := http.Get(srv.URL + "/vendor/pdfjs/web/viewer.mjs")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("GET viewer.mjs = %d, want 200", resp.StatusCode)
	}
	// A module script served as anything but JavaScript is refused by the
	// browser, and the viewer never boots.
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/javascript") && !strings.HasPrefix(ct, "application/javascript") {
		t.Fatalf("Content-Type of .mjs = %q, want a JavaScript type", ct)
	}
}
