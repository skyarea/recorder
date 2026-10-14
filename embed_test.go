package main

import (
	"io/fs"
	"path"
	"strings"
	"testing"
)

// The browser hands out frontend files from this embed, so a vendored path
// that only exists on disk (or was trimmed away) 404s in the app while every
// Go test stays green. These are the paths pdf.js fetches lazily, long after
// the viewer itself has loaded.
func TestEmbeddedViewerAssetsAreShipped(t *testing.T) {
	mustExist := []string{
		"frontend/index.html",
		"frontend/lib/bridge.mjs",
		"frontend/viewer-bridge.mjs",
		"frontend/vendor/pdfjs/VERSION",
		"frontend/vendor/pdfjs/LICENSE",
		"frontend/vendor/pdfjs/web/viewer.html",
		"frontend/vendor/pdfjs/web/viewer.mjs",
		"frontend/vendor/pdfjs/web/viewer.css",
		"frontend/vendor/pdfjs/build/pdf.mjs",
		"frontend/vendor/pdfjs/build/pdf.sandbox.mjs",
		"frontend/vendor/pdfjs/build/pdf.worker.mjs",
		"frontend/vendor/pdfjs/web/cmaps/UniGB-UCS2-H.bcmap",
		"frontend/vendor/pdfjs/web/standard_fonts/FoxitSerif.pfb",
		"frontend/vendor/pdfjs/web/standard_fonts/LiberationSans-Regular.ttf",
		"frontend/vendor/pdfjs/web/wasm/jbig2.wasm",
		"frontend/vendor/pdfjs/web/wasm/openjpeg.wasm",
		"frontend/vendor/pdfjs/web/iccs/CGATS001Compat-v2-micro.icc",
		"frontend/vendor/pdfjs/web/locale/locale.json",
		"frontend/vendor/pdfjs/web/locale/en-US/viewer.ftl",
		"frontend/vendor/pdfjs/web/locale/zh-CN/viewer.ftl",
	}
	for _, p := range mustExist {
		if _, err := FrontendFiles.Open(p); err != nil {
			t.Errorf("embedded frontend is missing %s: %v", p, err)
		}
	}
}

// Junk from the release zip must not be shipped: source maps, the debugger
// and the example document are all dead weight in the binary.
func TestEmbeddedFrontendDropsTrimmedJunk(t *testing.T) {
	err := fs.WalkDir(FrontendFiles, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		base := path.Base(p)
		if strings.HasSuffix(base, ".map") || strings.HasPrefix(base, "debugger") ||
			base == "compressed.tracemonkey-pldi-09.pdf" {
			t.Errorf("frontend embed ships trimmed junk: %s", p)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
