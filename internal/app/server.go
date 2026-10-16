// Package app is the service layer between the desktop window and the
// store: it owns the SQLite database, the scanned file tree, and the
// loopback HTTP API the frontend talks to.
package app

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/skyarea/recorder/internal/export"
	"github.com/skyarea/recorder/internal/files"
	"github.com/skyarea/recorder/internal/httpfiles"
	"github.com/skyarea/recorder/internal/storage"
)

// errDialogUnavailable marks "this build has no native dialog for that".
var errDialogUnavailable = errors.New("dialog unavailable")

// App owns the store, the current root + scan result, and the injectable
// hooks for the native pieces (folder/save dialogs, reveal in Explorer).
type App struct {
	store *storage.Store

	mu      sync.Mutex
	root    string // '/'-separated, as stored in settings
	scanned []files.FileMeta

	// Dialog/OS hooks. Production wires these to Wails/exec; tests replace them.
	chooseRoot func() (string, error)
	saveAs     func(defaultName string) (string, error)
	reveal     func(fullPath string) error

	exportDir string // fallback export dir when no save dialog
}

// New opens the database at dbPath and prepares the app with no root set.
func New(dbPath string) (*App, error) {
	st, err := storage.Open(dbPath)
	if err != nil {
		return nil, err
	}
	a := &App{
		store:      st,
		chooseRoot: func() (string, error) { return "", errDialogUnavailable },
		saveAs:     func(string) (string, error) { return "", errDialogUnavailable },
		reveal:     revealFile,
	}
	if cfg, err := os.UserConfigDir(); err == nil {
		a.exportDir = filepath.Join(cfg, "recorder", "exports")
	}
	return a, nil
}

// SetDialogs wires the native folder/save dialogs (called from gui with
// the running Wails app).
func (a *App) SetDialogs(chooseRoot func() (string, error), saveAs func(string) (string, error)) {
	a.chooseRoot = chooseRoot
	a.saveAs = saveAs
}

// Close releases the database.
func (a *App) Close() error { return a.store.Close() }

// LoadRoot restores the saved root and scans it best-effort: a root that
// vanished since the last run keeps its path (so the UI can prompt
// re-selection) and its stored records, and never errors.
func (a *App) LoadRoot() error {
	v, ok, err := a.store.Setting(settingRootPath)
	if err != nil || !ok {
		return err
	}
	a.mu.Lock()
	a.root = v
	a.mu.Unlock()
	// A root that vanished since last run keeps its path (so the UI can
	// prompt re-selection); the scan error is swallowed by design.
	_ = a.rescan()
	return nil
}

// SetRoot validates path, persists it, rescans, and swaps in the result.
func (a *App) SetRoot(path string) error {
	if path == "" {
		return errors.New("app: empty root path")
	}
	slashed := filepath.ToSlash(path)
	st, err := os.Stat(filepath.FromSlash(slashed))
	if err != nil {
		return fmt.Errorf("app: root not accessible: %w", err)
	}
	if !st.IsDir() {
		return fmt.Errorf("app: root is not a directory: %s", slashed)
	}
	if err := a.store.SetSetting(settingRootPath, slashed); err != nil {
		return err
	}
	if err := a.store.SetSetting(export.RootNameSetting, pathBase(slashed)); err != nil {
		return err
	}
	a.mu.Lock()
	a.root = slashed
	a.mu.Unlock()
	return a.rescan()
}

func (a *App) rescan() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.root == "" {
		a.scanned = nil
		return nil
	}
	metas, err := files.Scan(filepath.FromSlash(a.root))
	if err != nil {
		a.scanned = nil
		return err
	}
	a.scanned = metas
	return nil
}

func pathBase(p string) string { return path.Base(filepath.ToSlash(p)) }

// Routes builds the full handler: /api/* JSON endpoints, /files/* document
// streaming, and the embedded frontend as the catch-all.
func (a *App) Routes(frontend fs.FS) http.Handler {
	sub, err := fs.Sub(frontend, "frontend")
	if err != nil {
		sub = frontend
	}
	static := http.FileServerFS(sub)
	files := httpfiles.NewHandler(a.currentRoot)

	api := http.NewServeMux()
	api.HandleFunc("GET /api/state", a.handleState)
	api.HandleFunc("POST /api/root", a.handleRoot)
	api.HandleFunc("POST /api/status", a.handleStatus)
	api.HandleFunc("POST /api/star", a.handleStar)
	api.HandleFunc("POST /api/progress", a.handleProgress)
	api.HandleFunc("POST /api/totalpages", a.handleTotalPages)
	api.HandleFunc("POST /api/notes", a.handleAddNote)
	api.HandleFunc("POST /api/notes/update", a.handleUpdateNote)
	api.HandleFunc("POST /api/notes/delete", a.handleDeleteNote)
	api.HandleFunc("GET /api/annotations", a.handleListAnnotations)
	api.HandleFunc("POST /api/annotations", a.handleAddAnnotation)
	api.HandleFunc("POST /api/annotations/delete", a.handleDeleteAnnotation)
	api.HandleFunc("POST /api/links", a.handleLink)
	api.HandleFunc("POST /api/links/delete", a.handleUnlink)
	api.HandleFunc("POST /api/ui", a.handleUI)
	api.HandleFunc("POST /api/export", a.handleExport)
	api.HandleFunc("POST /api/import", a.handleImport)
	api.HandleFunc("POST /api/reveal", a.handleReveal)
	var notFound http.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeErr(w, http.StatusNotFound, "unknown api route")
	})

	root := http.NewServeMux()
	root.Handle("/api/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h, pattern := api.Handler(r)
		if pattern == "" {
			notFound.ServeHTTP(w, r)
			return
		}
		h.ServeHTTP(w, r)
	}))
	root.Handle("/", static)

	// /files/* is matched ahead of the mux on purpose: a ServeMux (and the
	// static catch-all) cleans "/files/../x" and answers with a redirect,
	// but trying to leave the root must be a flat 404.
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, httpfiles.Prefix) {
			files.ServeHTTP(w, r)
			return
		}
		root.ServeHTTP(w, r)
	})
}

// currentRoot reads the configured root under the lock that guards it.
func (a *App) currentRoot() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.root
}

// --- helpers -----------------------------------------------------------

func writeErr(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func decodeBody[T any](w http.ResponseWriter, r *http.Request) (T, bool) {
	var v T
	defer r.Body.Close()
	if err := json.NewDecoder(r.Body).Decode(&v); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid JSON body: "+err.Error())
		return v, false
	}
	return v, true
}

func (a *App) handleState(w http.ResponseWriter, _ *http.Request) {
	st, err := a.State()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, st)
}

// writeState replies to mutations with the fresh state so the frontend
// re-renders from one source of truth.
func (a *App) writeState(w http.ResponseWriter, err error) {
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	a.handleState(w, nil)
}

// --- root --------------------------------------------------------------

type rootReq struct {
	Path string `json:"path"`
}

func (a *App) handleRoot(w http.ResponseWriter, r *http.Request) {
	req, ok := decodeBody[rootReq](w, r)
	if !ok {
		return
	}
	root := req.Path
	if root == "" {
		picked, err := a.chooseRoot()
		if err != nil {
			if errors.Is(err, errDialogUnavailable) {
				writeErr(w, http.StatusNotImplemented, "no root given and no folder dialog available")
			} else {
				writeErr(w, http.StatusBadRequest, err.Error())
			}
			return
		}
		if picked == "" { // user cancelled
			writeErr(w, http.StatusBadRequest, "no folder selected")
			return
		}
		root = picked
	}
	if _, err := os.Stat(filepath.FromSlash(filepath.ToSlash(root))); err != nil {
		writeErr(w, http.StatusBadRequest, "root path does not exist")
		return
	}
	if err := a.SetRoot(root); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	a.handleState(w, r)
}

// --- simple file-record mutations --------------------------------------

type statusReq struct {
	Path   string `json:"path"`
	Status string `json:"status"`
}

func (a *App) handleStatus(w http.ResponseWriter, r *http.Request) {
	req, ok := decodeBody[statusReq](w, r)
	if !ok {
		return
	}
	if req.Path == "" {
		writeErr(w, http.StatusBadRequest, "path required")
		return
	}
	a.writeState(w, a.store.SetStatus(req.Path, req.Status))
}

type starReq struct {
	Path    string `json:"path"`
	Starred bool   `json:"starred"`
}

func (a *App) handleStar(w http.ResponseWriter, r *http.Request) {
	req, ok := decodeBody[starReq](w, r)
	if !ok {
		return
	}
	a.writeState(w, a.store.SetStar(req.Path, req.Starred))
}

type progressReq struct {
	Path string `json:"path"`
	Page int    `json:"page"`
}

func (a *App) handleProgress(w http.ResponseWriter, r *http.Request) {
	req, ok := decodeBody[progressReq](w, r)
	if !ok {
		return
	}
	a.writeState(w, a.store.SetProgress(req.Path, req.Page))
}

type totalPagesReq struct {
	Path       string `json:"path"`
	TotalPages int    `json:"totalPages"`
}

func (a *App) handleTotalPages(w http.ResponseWriter, r *http.Request) {
	req, ok := decodeBody[totalPagesReq](w, r)
	if !ok {
		return
	}
	a.writeState(w, a.store.SetTotalPages(req.Path, req.TotalPages))
}

// --- notes ---------------------------------------------------------------

type addNoteReq struct {
	Path string `json:"path"`
	Text string `json:"text"`
	Page *int   `json:"page"`
}

func (a *App) handleAddNote(w http.ResponseWriter, r *http.Request) {
	req, ok := decodeBody[addNoteReq](w, r)
	if !ok {
		return
	}
	n, err := a.store.AddNote(req.Path, req.Text, req.Page)
	if err != nil {
		a.writeState(w, err)
		return
	}
	writeJSON(w, http.StatusOK, noteView{ID: n.ID, Text: n.Text, Page: n.Page, TS: n.TS, AnnIDs: []string{}})
}

type updateNoteReq struct {
	ID   string `json:"id"`
	Text string `json:"text"`
}

func (a *App) handleUpdateNote(w http.ResponseWriter, r *http.Request) {
	req, ok := decodeBody[updateNoteReq](w, r)
	if !ok {
		return
	}
	a.writeState(w, a.store.UpdateNote(req.ID, req.Text))
}

type idReq struct {
	ID string `json:"id"`
}

func (a *App) handleDeleteNote(w http.ResponseWriter, r *http.Request) {
	req, ok := decodeBody[idReq](w, r)
	if !ok {
		return
	}
	a.writeState(w, a.store.DeleteNote(req.ID))
}

// --- annotations -----------------------------------------------------------

type addAnnotationReq struct {
	Path string `json:"path"`
	Page int    `json:"page"`
	Kind string `json:"kind"`
	Data string `json:"data"`
}

func (a *App) handleListAnnotations(w http.ResponseWriter, r *http.Request) {
	anns, err := a.store.ListAnnotations(r.URL.Query().Get("path"))
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	views := make([]annotationView, 0, len(anns))
	for _, an := range anns {
		views = append(views, annotationView{ID: an.ID, Page: an.Page, Kind: an.Kind, Data: an.Data, CreatedAt: an.CreatedAt})
	}
	writeJSON(w, http.StatusOK, map[string]any{"annotations": views})
}

func (a *App) handleAddAnnotation(w http.ResponseWriter, r *http.Request) {
	req, ok := decodeBody[addAnnotationReq](w, r)
	if !ok {
		return
	}
	an, err := a.store.AddAnnotation(req.Path, req.Page, req.Kind, req.Data)
	if err != nil {
		a.writeState(w, err)
		return
	}
	writeJSON(w, http.StatusOK, annotationView{ID: an.ID, Page: an.Page, Kind: an.Kind, Data: an.Data, CreatedAt: an.CreatedAt})
}

func (a *App) handleDeleteAnnotation(w http.ResponseWriter, r *http.Request) {
	req, ok := decodeBody[idReq](w, r)
	if !ok {
		return
	}
	a.writeState(w, a.store.DeleteAnnotation(req.ID))
}

// --- note↔annotation links -------------------------------------------------

type linkReq struct {
	NoteID       string `json:"noteId"`
	AnnotationID string `json:"annotationId"`
}

func (a *App) handleLink(w http.ResponseWriter, r *http.Request) {
	req, ok := decodeBody[linkReq](w, r)
	if !ok {
		return
	}
	a.writeState(w, a.store.LinkNote(req.NoteID, req.AnnotationID))
}

func (a *App) handleUnlink(w http.ResponseWriter, r *http.Request) {
	req, ok := decodeBody[linkReq](w, r)
	if !ok {
		return
	}
	a.writeState(w, a.store.UnlinkNote(req.NoteID, req.AnnotationID))
}

// --- ui state ---------------------------------------------------------------

type uiReq struct {
	Collapsed   *[]string `json:"collapsed"`
	Filter      *string   `json:"filter"`
	NotesOpen   *bool     `json:"notesOpen"`
	SidebarOpen *bool     `json:"sidebarOpen"`
	Theme       *string   `json:"theme"`
}

// themes are the only values ui_theme may hold. A theme the shell does not
// know leaves it with no stylesheet to apply, so the API refuses the write
// instead of storing something the frontend will silently ignore.
var themes = map[string]bool{"light": true, "dark": true}

func (a *App) handleUI(w http.ResponseWriter, r *http.Request) {
	req, ok := decodeBody[uiReq](w, r)
	if !ok {
		return
	}
	if req.Theme != nil {
		if !themes[*req.Theme] {
			writeErr(w, http.StatusBadRequest, "theme must be light or dark")
			return
		}
		if err := a.store.SetSetting(settingUITheme, *req.Theme); err != nil {
			a.writeState(w, err)
			return
		}
	}
	if req.Collapsed != nil {
		b, err := json.Marshal(*req.Collapsed)
		if err != nil {
			a.writeState(w, err)
			return
		}
		if err := a.store.SetSetting(settingUICollapsed, string(b)); err != nil {
			a.writeState(w, err)
			return
		}
	}
	if req.Filter != nil {
		if err := a.store.SetSetting(settingUIFilter, *req.Filter); err != nil {
			a.writeState(w, err)
			return
		}
	}
	if req.NotesOpen != nil {
		v := "false"
		if *req.NotesOpen {
			v = "true"
		}
		if err := a.store.SetSetting(settingUINotesOpen, v); err != nil {
			a.writeState(w, err)
			return
		}
	}
	if req.SidebarOpen != nil {
		v := "false"
		if *req.SidebarOpen {
			v = "true"
		}
		if err := a.store.SetSetting(settingUISidebarOpen, v); err != nil {
			a.writeState(w, err)
			return
		}
	}
	a.handleState(w, r)
}

// --- export / import ---------------------------------------------------------

func (a *App) handleExport(w http.ResponseWriter, r *http.Request) {
	data, err := export.ExportJSON(a.store, a.rootName())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	name := fmt.Sprintf("阅读进度-%s.json", time.Now().Format("20060102"))

	target, derr := a.saveAs(name)
	switch {
	case errors.Is(derr, errDialogUnavailable):
		if a.exportDir == "" {
			writeErr(w, http.StatusInternalServerError, "no export location available")
			return
		}
		if err := os.MkdirAll(a.exportDir, 0o755); err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		target = filepath.Join(a.exportDir, name)
	case derr != nil:
		writeErr(w, http.StatusInternalServerError, derr.Error())
		return
	case target == "":
		writeErr(w, http.StatusBadRequest, "no file selected")
		return
	}

	if err := os.WriteFile(target, data, 0o644); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	d, err := a.store.Dump()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"path": filepath.ToSlash(target), "count": len(d.Files)})
}

func (a *App) handleImport(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	defer r.Body.Close()
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	n, err := export.ImportJSON(a.store, body, r.URL.Query().Get("replace") == "true")
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"imported": n})
}

// --- reveal ----------------------------------------------------------------

type revealReq struct {
	Path string `json:"path"`
}

func (a *App) handleReveal(w http.ResponseWriter, r *http.Request) {
	req, ok := decodeBody[revealReq](w, r)
	if !ok {
		return
	}
	full, err := a.resolveInRoot(req.Path)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := a.reveal(full); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// resolveInRoot maps rel onto the root through the same containment check
// that guards /files/*, and requires the target to exist.
func (a *App) resolveInRoot(rel string) (string, error) {
	full, err := httpfiles.Resolve(a.currentRoot(), rel)
	if err != nil {
		return "", err
	}
	if _, err := os.Stat(full); err != nil {
		return "", fmt.Errorf("app: not found: %s", rel)
	}
	return full, nil
}

// revealFile opens the OS file manager with the file selected.
func revealFile(fullPath string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		// explorer exits non-zero even on success; the start itself is the signal.
		cmd = exec.Command("explorer", "/select,"+fullPath)
	case "darwin":
		cmd = exec.Command("open", "-R", fullPath)
	default:
		cmd = exec.Command("xdg-open", filepath.Dir(fullPath))
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	go func() { _ = cmd.Wait() }()
	return nil
}

// --- /files/* belongs to issue #7 (internal/httpfiles), not this package.
