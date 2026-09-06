// Package migrations embeds the goose SQL migrations so that a built binary
// carries its own schema and needs no files on disk beside it.
package migrations

import "embed"

// FS holds every migration, in goose's numbered-filename convention.
//
//go:embed *.sql
var FS embed.FS
