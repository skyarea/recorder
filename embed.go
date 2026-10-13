package main

import "embed"

// FrontendFiles is the placeholder page the window opens on. Later tasks
// replace what it serves with the pdf.js viewer.
//
//go:embed frontend
var FrontendFiles embed.FS
