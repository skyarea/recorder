package app

import (
	"encoding/json"
	"os"
	"path"
	"path/filepath"

	"github.com/skyarea/recorder/internal/export"
)

// View types fix the JSON contract the frontend consumes (Task 8+). They
// exist so storage models never leak their Go field names into the API.

type scannedView struct {
	Path string `json:"path"`
	Name string `json:"name"`
}

type noteView struct {
	ID     string   `json:"id"`
	Text   string   `json:"text"`
	Page   *int     `json:"page"`
	TS     string   `json:"ts"`
	AnnIDs []string `json:"annIds"`
}

type recordView struct {
	Status     string     `json:"status"`
	Page       int        `json:"page"`
	TotalPages int        `json:"totalPages"`
	Starred    bool       `json:"starred"`
	UpdatedAt  string     `json:"updatedAt"`
	Notes      []noteView `json:"notes"`
}

type annotationView struct {
	ID        string `json:"id"`
	Page      int    `json:"page"`
	Kind      string `json:"kind"`
	Data      string `json:"data"`
	CreatedAt string `json:"createdAt"`
}

type uiView struct {
	Collapsed []string `json:"collapsed"`
	Filter    string   `json:"filter"`
	NotesOpen bool     `json:"notesOpen"`
	// Theme is the user's explicit choice, "light" or "dark". Empty means
	// they have not chosen, and the shell follows the system instead.
	Theme string `json:"theme"`
}

type stateView struct {
	HasRoot     bool                  `json:"hasRoot"`
	RootPath    string                `json:"rootPath"`
	RootName    string                `json:"rootName"`
	Scanned     []scannedView         `json:"scanned"`
	Records     map[string]recordView `json:"records"`
	UI          uiView                `json:"ui"`
	OrphanCount int                   `json:"orphanCount"`
}

// State assembles the full snapshot GET /api/state returns: scanned tree,
// one record per scanned or stored path with its notes, persisted UI
// settings, and the orphan count.
func (a *App) State() (stateView, error) {
	a.mu.Lock()
	root, scanned := a.root, a.scanned
	a.mu.Unlock()

	hasRoot := root != ""
	if hasRoot {
		st, err := os.Stat(filepath.FromSlash(root))
		hasRoot = err == nil && st.IsDir()
	}

	out := stateView{
		HasRoot:  hasRoot,
		RootPath: root,
		Scanned:  make([]scannedView, 0, len(scanned)),
		Records:  map[string]recordView{},
	}
	for _, m := range scanned {
		out.Scanned = append(out.Scanned, scannedView{Path: m.Path, Name: m.Name})
	}
	out.RootName = a.rootName()

	d, err := a.store.Dump()
	if err != nil {
		return stateView{}, err
	}

	// Notes grouped by path, in Dump's (file_path, ts, rowid) order; a
	// note's annIds come from the link table.
	annIDs := map[string][]string{}
	for _, l := range d.Links {
		annIDs[l[0]] = append(annIDs[l[0]], l[1])
	}
	notesByPath := map[string][]noteView{}
	for _, n := range d.Notes {
		ids := annIDs[n.ID]
		if ids == nil {
			ids = []string{}
		}
		notesByPath[n.FilePath] = append(notesByPath[n.FilePath],
			noteView{ID: n.ID, Text: n.Text, Page: n.Page, TS: n.TS, AnnIDs: ids})
	}

	for _, r := range d.Files {
		out.Records[r.Path] = recordView{
			Status: r.Status, Page: r.Page, TotalPages: r.TotalPages,
			Starred: r.Starred, UpdatedAt: r.UpdatedAt,
		}
	}
	// Scanned paths and note-only paths without a files row still get a
	// record, so the UI shows them and orphan rules can see their notes.
	for _, m := range scanned {
		if _, ok := out.Records[m.Path]; !ok {
			out.Records[m.Path] = recordView{Status: "todo"}
		}
	}
	for p := range notesByPath {
		if _, ok := out.Records[p]; !ok {
			out.Records[p] = recordView{Status: "todo"}
		}
	}
	for p, r := range out.Records {
		if r.Notes == nil {
			if r.Notes = notesByPath[p]; r.Notes == nil {
				r.Notes = []noteView{}
			}
		}
		out.Records[p] = r
	}

	out.UI, err = a.uiState()
	if err != nil {
		return stateView{}, err
	}
	out.OrphanCount = a.orphanCount(out)
	return out, nil
}

// orphanCount counts stored records outside the scanned tree that hold
// real content: non-todo status, any note, progress, or a star.
func (a *App) orphanCount(st stateView) int {
	inScanned := make(map[string]bool, len(st.Scanned))
	for _, s := range st.Scanned {
		inScanned[s.Path] = true
	}
	n := 0
	for p, r := range st.Records {
		if inScanned[p] {
			continue
		}
		substantive := (r.Status != "todo" && r.Status != "") || len(r.Notes) > 0 || r.Page > 0 || r.Starred
		if substantive {
			n++
		}
	}
	return n
}

const (
	settingRootPath    = "root_path"
	settingUICollapsed = "ui_collapsed"
	settingUIFilter    = "ui_filter"
	settingUINotesOpen = "ui_notes_open"
	settingUITheme     = "ui_theme"
)

func (a *App) rootName() string {
	a.mu.Lock()
	root := a.root
	a.mu.Unlock()
	if root == "" {
		return ""
	}
	if v, ok, err := a.store.Setting(export.RootNameSetting); err == nil && ok && v != "" {
		return v
	}
	return path.Base(filepath.ToSlash(root))
}

func (a *App) uiState() (uiView, error) {
	v := uiView{Collapsed: []string{}}
	if s, ok, err := a.store.Setting(settingUICollapsed); err != nil {
		return v, err
	} else if ok && s != "" {
		if err := json.Unmarshal([]byte(s), &v.Collapsed); err != nil {
			v.Collapsed = []string{}
		}
	}
	if s, ok, err := a.store.Setting(settingUIFilter); err != nil {
		return v, err
	} else if ok {
		v.Filter = s
	}
	if s, ok, err := a.store.Setting(settingUINotesOpen); err != nil {
		return v, err
	} else if ok {
		v.NotesOpen = s == "true"
	}
	if s, ok, err := a.store.Setting(settingUITheme); err != nil {
		return v, err
	} else if ok {
		v.Theme = s
	}
	if v.Collapsed == nil {
		v.Collapsed = []string{}
	}
	return v, nil
}
