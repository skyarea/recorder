// Package gui holds the Wails v3 application: the window and everything
// hung off it. Later tasks add storage, an HTTP API and the pdf.js viewer
// behind the URL this package loads.
package gui

import (
	"io/fs"
	"net/http"

	"github.com/wailsapp/wails/v3/pkg/application"
)

// Run starts the Wails v3 app: one main window, opening on the embedded
// frontend served at the root. It blocks until the app quits and returns
// any error from the run.
func Run(frontend fs.FS) error {
	assets, err := fs.Sub(frontend, "frontend")
	if err != nil {
		return err
	}

	app := application.New(application.Options{
		Name:        "recorder",
		Description: "PDF 阅读进度 · 笔记",
		Assets:      application.AssetOptions{Handler: http.FileServer(http.FS(assets))},
	})

	mainWindow := app.Window.NewWithOptions(application.WebviewWindowOptions{
		Name:   "main",
		Title:  "PDF 阅读进度 · 笔记",
		URL:    "/",
		Width:  1280,
		Height: 860,
	})
	mainWindow.Show()

	return app.Run()
}
