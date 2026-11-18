// Package migrations holds the golang-migrate SQL files and embeds them into the binary so
// cmd/api can apply pending migrations on boot without needing the .sql files mounted/shipped
// separately — see internal/platform/dbmigrate/dbmigrate.go for the runner that consumes FS.
package migrations

import "embed"

//go:embed *.sql
var FS embed.FS
