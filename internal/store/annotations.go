package store

import (
	"database/sql"
	"errors"
)

// AnnotationAdd appends a new annotation to fileID.
func (s *Store) AnnotationAdd(actor string, fileID int64, content string) (*Annotation, error) {
	var out *Annotation
	err := s.withWriteTx(func(tx *sql.Tx) error {
		// Ensure file exists.
		if _, err := s.fileInfoByID(tx, fileID); err != nil {
			return err
		}
		now := s.nowMs()
		res, err := tx.Exec(
			`INSERT INTO annotations(file_id, actor, content, created_at) VALUES (?, ?, ?, ?)`,
			fileID, actor, content, now,
		)
		if err != nil {
			return err
		}
		id, _ := res.LastInsertId()
		out = &Annotation{
			ID: id, FileID: fileID, Actor: actor, Content: content,
			CreatedAt: FromEpochMs(now),
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// AnnotationList returns annotations on a file. If includeRemoved=false, only
// active ones.
func (s *Store) AnnotationList(fileID int64, includeRemoved bool) ([]Annotation, error) {
	var anns []Annotation
	err := s.withReadTx(func(tx *sql.Tx) error {
		var rows *sql.Rows
		var err error
		if includeRemoved {
			rows, err = tx.Query(
				`SELECT id, file_id, actor, content, created_at, removed_at
				 FROM annotations WHERE file_id = ? ORDER BY id ASC`, fileID,
			)
		} else {
			rows, err = tx.Query(
				`SELECT id, file_id, actor, content, created_at, removed_at
				 FROM annotations WHERE file_id = ? AND removed_at IS NULL ORDER BY id ASC`, fileID,
			)
		}
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var a Annotation
			var createdAt int64
			var removedAt sql.NullInt64
			if err := rows.Scan(&a.ID, &a.FileID, &a.Actor, &a.Content, &createdAt, &removedAt); err != nil {
				return err
			}
			a.CreatedAt = FromEpochMs(createdAt)
			if removedAt.Valid {
				t := FromEpochMs(removedAt.Int64)
				a.RemovedAt = &t
			}
			anns = append(anns, a)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, err
	}
	return anns, nil
}

// activeAnnotations is a tx-scoped variant used inside the open path.
func (s *Store) activeAnnotations(tx *sql.Tx, fileID int64) ([]Annotation, error) {
	rows, err := tx.Query(
		`SELECT id, file_id, actor, content, created_at, removed_at
		 FROM annotations WHERE file_id = ? AND removed_at IS NULL ORDER BY id ASC`, fileID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Annotation
	for rows.Next() {
		var a Annotation
		var createdAt int64
		var removedAt sql.NullInt64
		if err := rows.Scan(&a.ID, &a.FileID, &a.Actor, &a.Content, &createdAt, &removedAt); err != nil {
			return nil, err
		}
		a.CreatedAt = FromEpochMs(createdAt)
		if removedAt.Valid {
			t := FromEpochMs(removedAt.Int64)
			a.RemovedAt = &t
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// AnnotationGet returns a single annotation.
func (s *Store) AnnotationGet(id int64) (*Annotation, error) {
	var a Annotation
	err := s.withReadTx(func(tx *sql.Tx) error {
		var createdAt int64
		var removedAt sql.NullInt64
		if err := tx.QueryRow(
			`SELECT id, file_id, actor, content, created_at, removed_at FROM annotations WHERE id = ?`, id,
		).Scan(&a.ID, &a.FileID, &a.Actor, &a.Content, &createdAt, &removedAt); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return ErrAnnotationNotFound
			}
			return err
		}
		a.CreatedAt = FromEpochMs(createdAt)
		if removedAt.Valid {
			t := FromEpochMs(removedAt.Int64)
			a.RemovedAt = &t
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &a, nil
}

// AnnotationRemove soft-deletes an annotation by setting removed_at.
func (s *Store) AnnotationRemove(id int64) error {
	return s.withWriteTx(func(tx *sql.Tx) error {
		now := s.nowMs()
		res, err := tx.Exec(
			`UPDATE annotations SET removed_at = ? WHERE id = ? AND removed_at IS NULL`,
			now, id,
		)
		if err != nil {
			return err
		}
		n, _ := res.RowsAffected()
		if n == 0 {
			return ErrAnnotationNotFound
		}
		return nil
	})
}

// AnnotationSearch returns annotations whose content contains query (case
// sensitive substring), across all files.
func (s *Store) AnnotationSearch(query string) ([]Annotation, []string, error) {
	type result struct {
		ann  Annotation
		path string
	}
	var rs []result
	err := s.withReadTx(func(tx *sql.Tx) error {
		rows, err := tx.Query(`
			SELECT a.id, a.file_id, a.actor, a.content, a.created_at, a.removed_at, f.path
			FROM annotations a JOIN files f ON f.id = a.file_id
			WHERE a.removed_at IS NULL AND a.content LIKE ?
			ORDER BY a.created_at`, "%"+query+"%")
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var r result
			var createdAt int64
			var removedAt sql.NullInt64
			if err := rows.Scan(&r.ann.ID, &r.ann.FileID, &r.ann.Actor, &r.ann.Content,
				&createdAt, &removedAt, &r.path); err != nil {
				return err
			}
			r.ann.CreatedAt = FromEpochMs(createdAt)
			if removedAt.Valid {
				t := FromEpochMs(removedAt.Int64)
				r.ann.RemovedAt = &t
			}
			rs = append(rs, r)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, nil, err
	}
	anns := make([]Annotation, len(rs))
	paths := make([]string, len(rs))
	for i, r := range rs {
		anns[i] = r.ann
		paths[i] = r.path
	}
	return anns, paths, nil
}
