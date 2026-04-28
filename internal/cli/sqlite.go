package cli

import (
	"database/sql"

	"github.com/frane/agented/internal/db"
)

// openSqlite opens (and migrates) a SQLite database. Lifted from internal/db
// for convenience inside the CLI layer.
func openSqlite(path string) (*sql.DB, error) {
	return db.Open(path)
}
