package storage

// Dump is a snapshot of every data table plus the settings, used by the
// JSON backup/restore path (internal/export). It is the only way to
// read the whole store or to write ids and timestamps verbatim, and is
// deliberately import-specific: the everyday API generates both.
type Dump struct {
	Files       []FileRec
	Notes       []Note
	Annotations []Annotation
	Links       [][2]string // {note_id, annotation_id}
	Settings    map[string]string
}

// Dump snapshots the whole store. Row order is deterministic so two
// dumps of an unchanged store compare equal and exports are stable:
// files by path, notes by (file_path, ts, rowid), annotations by
// (file_path, created_at, rowid), links by insertion order, settings
// by key. AnnIDs is left empty on every note — the link table carries
// the relationships (Dump.Links).
func (s *Store) Dump() (Dump, error) {
	var d Dump

	files, err := s.ListFiles()
	if err != nil {
		return Dump{}, err
	}
	d.Files = files

	rows, err := s.db.Query(`
		SELECT id, file_path, text, page, ts
		  FROM notes
		 ORDER BY file_path, ts, rowid`)
	if err != nil {
		return Dump{}, err
	}
	// scanNotes drains and closes the rows and gives each note an empty
	// (non-nil) AnnIDs slice, keeping Dump values comparable in tests.
	if d.Notes, err = scanNotes(rows); err != nil {
		return Dump{}, err
	}

	rows, err = s.db.Query(`
		SELECT id, file_path, page, kind, data, created_at
		  FROM annotations
		 ORDER BY file_path, created_at, rowid`)
	if err != nil {
		return Dump{}, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var a Annotation
		if err := rows.Scan(&a.ID, &a.FilePath, &a.Page, &a.Kind, &a.Data, &a.CreatedAt); err != nil {
			return Dump{}, err
		}
		d.Annotations = append(d.Annotations, a)
	}
	if err := rows.Err(); err != nil {
		return Dump{}, err
	}

	rows, err = s.db.Query(`
		SELECT note_id, annotation_id
		  FROM note_annotations
		 ORDER BY rowid`)
	if err != nil {
		return Dump{}, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var l [2]string
		if err := rows.Scan(&l[0], &l[1]); err != nil {
			return Dump{}, err
		}
		d.Links = append(d.Links, l)
	}
	if err := rows.Err(); err != nil {
		return Dump{}, err
	}

	rows, err = s.db.Query(`SELECT "key", value FROM settings ORDER BY "key"`)
	if err != nil {
		return Dump{}, err
	}
	defer func() { _ = rows.Close() }()
	d.Settings = map[string]string{}
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			return Dump{}, err
		}
		d.Settings[k] = v
	}
	if err := rows.Err(); err != nil {
		return Dump{}, err
	}

	return d, nil
}

// normalizeFileRec enforces the files-table invariants the everyday
// setters maintain (SetStatus/SetProgress/SetTotalPages), so both
// write paths into the files table agree on what a stored row may look
// like: negative page/total_pages clamp to 0, a page above total_pages
// links down (SetTotalPages' rule) and a status outside
// todo|reading|done|skip becomes "todo". RestoreBatch applies it at the
// import boundary — bad input is repaired rather than rejected, because
// a backup must import.
func normalizeFileRec(f FileRec) FileRec {
	if f.Page < 0 {
		f.Page = 0
	}
	if f.TotalPages < 0 {
		f.TotalPages = 0
	}
	if f.Page > f.TotalPages {
		f.Page = f.TotalPages
	}
	if !validStatuses[f.Status] {
		f.Status = "todo"
	}
	return f
}

// RestoreBatch replaces the entire content of the files, notes,
// annotations and note_annotations tables with d and upserts d.Settings
// — all in one transaction, so either the whole restore commits or
// nothing changes. It is the id/ts-preserving counterpart of the
// Add*/Set* API for JSON import: ids, ts, created_at and updated_at are
// stored exactly as given, and notes and annotations restore verbatim
// (Note.Page is a nullable pointer with no invariant to keep, and
// annotation kinds are not re-validated here).
//
// File records are the one exception: every record passes through
// normalizeFileRec at this boundary, so the import — the second write
// path into the files table, after the Set* setters — can never store
// rows the rest of the app assumes cannot exist (page < 0,
// total_pages < 0, page > total_pages, status outside
// todo|reading|done|skip). Import must not be fragile, so offending
// values are repaired, not rejected. Settings keys absent from
// d.Settings are left alone. Duplicate paths, note ids, annotation ids
// or link pairs in d make the batch fail and roll back.
func (s *Store) RestoreBatch(d Dump) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }() // no-op after Commit

	// Children first: note_annotations references notes and annotations.
	for _, del := range []string{
		`DELETE FROM note_annotations`,
		`DELETE FROM notes`,
		`DELETE FROM annotations`,
		`DELETE FROM files`,
	} {
		if _, err := tx.Exec(del); err != nil {
			return err
		}
	}

	for _, f := range d.Files {
		// The single normalisation point: both the replace and the
		// merge path of JSON import reach the files table here.
		f = normalizeFileRec(f)
		if _, err := tx.Exec(`
			INSERT INTO files (path, status, page, total_pages, starred, updated_at)
			VALUES (?, ?, ?, ?, ?, ?)`,
			f.Path, f.Status, f.Page, f.TotalPages, f.Starred, f.UpdatedAt); err != nil {
			return err
		}
	}
	for _, n := range d.Notes {
		if _, err := tx.Exec(`
			INSERT INTO notes (id, file_path, text, page, ts)
			VALUES (?, ?, ?, ?, ?)`,
			n.ID, n.FilePath, n.Text, pageArg(n.Page), n.TS); err != nil {
			return err
		}
	}
	for _, a := range d.Annotations {
		if _, err := tx.Exec(`
			INSERT INTO annotations (id, file_path, page, kind, data, created_at)
			VALUES (?, ?, ?, ?, ?, ?)`,
			a.ID, a.FilePath, a.Page, a.Kind, a.Data, a.CreatedAt); err != nil {
			return err
		}
	}
	for _, l := range d.Links {
		if _, err := tx.Exec(`
			INSERT INTO note_annotations (note_id, annotation_id) VALUES (?, ?)`,
			l[0], l[1]); err != nil {
			return err
		}
	}
	for k, v := range d.Settings {
		if _, err := tx.Exec(`
			INSERT INTO settings ("key", value) VALUES (?, ?)
			ON CONFLICT("key") DO UPDATE SET value = excluded.value`,
			k, v); err != nil {
			return err
		}
	}

	return tx.Commit()
}
