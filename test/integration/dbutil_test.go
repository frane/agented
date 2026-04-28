package integration_test

import (
	"database/sql"

	"github.com/frane/agented/internal/db"
)

// openTestDB opens the test database directly (without re-running migrations,
// which would race with the binary).
func openTestDB(path string) (*sql.DB, error) {
	return db.Open(path)
}
