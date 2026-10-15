package main

import (
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"

	appsrv "github.com/skyarea/recorder/internal/app"
	"github.com/skyarea/recorder/internal/gui"
	"github.com/wailsapp/wails/v3/pkg/application"
)

// version is stamped at build time by the packaging command in the Makefile
// (and release.yml): -ldflags "-X main.version=$VERSION". The default marks
// a build that never went through packaging — `go build .`, a dev loop.
var version = "dev"

// run performs the startup order fixed by the tracer bullet: check the
// WebView2 runtime → open the DB → read the saved root → rescan → listen on
// 127.0.0.1:0 → point the window at that address.
func run() error {
	// Wails dies with an opaque COM error when the WebView2 runtime is
	// missing; a double-clicked exe then looks like a crash. Say what is
	// missing and where to get it instead.
	if err := gui.CheckWebView2(); err != nil {
		gui.NotifyWebView2Missing()
		return err
	}

	cfgDir, err := os.UserConfigDir()
	if err != nil {
		return fmt.Errorf("locate config dir: %w", err)
	}
	appDir := filepath.Join(cfgDir, "recorder")

	a, err := appsrv.New(filepath.Join(appDir, "recorder.db"))
	if err != nil {
		return err
	}
	defer func() { _ = a.Close() }()

	// A root that vanished since last run must not stop the app: the
	// window opens on the empty state and prompts for a new folder.
	if err := a.LoadRoot(); err != nil {
		log.Printf("recorder: load saved root: %v", err)
	}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return fmt.Errorf("bind loopback: %w", err)
	}
	srv := &http.Server{Handler: a.Routes(FrontendFiles)}
	go func() {
		if err := srv.Serve(ln); err != nil && err != http.ErrServerClosed {
			log.Printf("recorder: http server: %v", err)
		}
	}()
	defer func() { _ = srv.Close() }()

	url := fmt.Sprintf("http://%s/", ln.Addr().String())
	log.Printf("recorder %s: serving %s", version, url)
	return gui.Run(url, version, func(app *application.App) error {
		a.SetDialogs(
			func() (string, error) {
				return app.Dialog.OpenFile().
					CanChooseFiles(false).
					CanChooseDirectories(true).
					PromptForSingleSelection()
			},
			func(name string) (string, error) {
				return app.Dialog.SaveFileWithOptions(&application.SaveFileDialogOptions{
					Filename: name,
				}).PromptForSingleSelection()
			},
		)
		return nil
	})
}

func main() {
	if err := run(); err != nil {
		// A missing WebView2 has already been explained by the dialog; log.Fatal
		// would only repeat it into a console nobody is watching.
		log.Fatal(err)
	}
}
