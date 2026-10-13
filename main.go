package main

import (
	"log"

	"github.com/skyarea/recorder/internal/gui"
)

// main starts the desktop app: a Wails v3 window loading the embedded
// frontend. Later tasks hang their modules off this skeleton.
func main() {
	if err := gui.Run(FrontendFiles); err != nil {
		log.Fatal(err)
	}
}
