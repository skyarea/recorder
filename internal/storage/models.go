// Package storage persists reading progress for PDF files and app settings
// in a local SQLite database (pure-Go driver, no CGO).
package storage

// FileRec is one row of the files table: everything we know about a PDF
// on disk. UpdatedAt is RFC3339 formatted and refreshed by every setter
// that mutates the record.
type FileRec struct {
	Path, Status string // status ∈ todo|reading|done|skip
	Page, TotalPages int
	Starred bool
	UpdatedAt string // RFC3339
}

// validStatuses is the closed set accepted by SetStatus.
var validStatuses = map[string]bool{
	"todo":    true,
	"reading": true,
	"done":    true,
	"skip":    true,
}
