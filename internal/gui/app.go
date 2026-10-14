// Package gui holds the Wails v3 application: the window and everything
// hung off it. The HTTP API lives in internal/app; this package only
// points a window at its address and lends the window's native dialogs
// to the service layer.
package gui

import (
	"github.com/wailsapp/wails/v3/pkg/application"
)

// Run starts the Wails v3 app with one main window loading url (the
// loopback HTTP address of the app service). onAppReady receives the
// created application before the loop starts, to wire native dialogs.
// Run blocks until the app quits and returns any error from the run.
func Run(url string, onAppReady func(*application.App) error) error {
	app := application.New(application.Options{
		Name:        "recorder",
		Description: "PDF 阅读进度 · 笔记",
	})

	if onAppReady != nil {
		if err := onAppReady(app); err != nil {
			return err
		}
	}

	mainWindow := app.Window.NewWithOptions(application.WebviewWindowOptions{
		Name:   "main",
		Title:  "PDF 阅读进度 · 笔记",
		URL:    url,
		Width:  1280,
		Height: 860,
	})
	mainWindow.Show()

	return app.Run()
}
