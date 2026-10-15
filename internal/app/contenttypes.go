package app

import "mime"

// pinnedContentTypes are the media types the frontend stops working without.
//
// Go asks the operating system first and only falls back to its own table, and
// on Windows the answer comes from a registry any installed program may have
// rewritten: the CI image resolves ".mjs" to "text/plain". Chromium refuses to
// run a module script and to stream-compile WebAssembly under the wrong type,
// so the reader would work on one machine and fail silently on the next.
// Entries registered here outrank the system table, which makes the response
// headers a property of this program rather than of whoever last touched the
// registry.
//
// ".pdf" is pinned for the same reason and for the same registry quirk —
// internal/httpfiles serves the documents and would otherwise inherit whatever
// some installed program claimed ".pdf" was. Pinning it here keeps "we do not
// trust the operating system for a type" a single decision in a single file.
var pinnedContentTypes = map[string]string{
	".html": "text/html; charset=utf-8",
	".css":  "text/css; charset=utf-8",
	".js":   "text/javascript; charset=utf-8",
	".mjs":  "text/javascript; charset=utf-8",
	".json": "application/json",
	".pdf":  "application/pdf",
	".wasm": "application/wasm",
}

func init() {
	for ext, typ := range pinnedContentTypes {
		mime.AddExtensionType(ext, typ)
	}
}
