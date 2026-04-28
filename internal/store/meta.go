package store

import (
	"database/sql"
	"errors"
	"time"
)

// MetaSet stores a single key/value pair in the meta table.
func (s *Store) MetaSet(key, value string) error {
	now := s.nowMs()
	_, err := s.db.Exec(
		`INSERT INTO meta(key, value, updated_at) VALUES (?, ?, ?)
		 ON CONFLICT(key) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at`,
		key, value, now,
	)
	return err
}

// MetaGet retrieves a meta value. Returns "", false if absent.
func (s *Store) MetaGet(key string) (string, bool, error) {
	var v string
	err := s.db.QueryRow(`SELECT value FROM meta WHERE key = ?`, key).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return v, true, nil
}

// MetaGetTime is a typed convenience for unix-ms timestamps stored as strings.
func (s *Store) MetaGetTime(key string) (time.Time, bool, error) {
	v, ok, err := s.MetaGet(key)
	if err != nil || !ok {
		return time.Time{}, ok, err
	}
	ms, err := parseInt64(v)
	if err != nil {
		return time.Time{}, false, err
	}
	return FromEpochMs(ms), true, nil
}

// MetaSetTime is the inverse of MetaGetTime.
func (s *Store) MetaSetTime(key string, t time.Time) error {
	return s.MetaSet(key, formatID(t.UnixMilli()))
}

func parseInt64(s string) (int64, error) {
	var n int64
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c < '0' || c > '9' {
			if i == 0 && c == '-' {
				continue
			}
			return 0, errors.New("not an integer: " + s)
		}
		n = n*10 + int64(c-'0')
	}
	if len(s) > 0 && s[0] == '-' {
		return -n, nil
	}
	return n, nil
}
