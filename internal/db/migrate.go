package db

import (
	"io/fs"
)

// newSubFS returns the embedded migrations rooted at the directory goose
// expects (the .sql files at the FS root).
func newSubFS() (fs.FS, error) {
	return fs.Sub(migrationsFS, "migrations")
}
