package db

// sqlc regenerates internal/db/gen from queries.sql and the migration schema
// (§8.1). The generated code is committed; run `make generate` after changing
// queries.sql or the schema.
//
//go:generate sqlc generate
