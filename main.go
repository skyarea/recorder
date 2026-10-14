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

// run performs the startup order fixed by the tracer bullet: open the DB
// → read the saved root → rescan → listen on 127.0.0.1:0 → point the
// window at that address.
func run() error {
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
	log.Printf("recorder: serving %s", url)
	return gui.Run(url, func(app *application.App) error {
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
		log.Fatal(err)
	}
}
