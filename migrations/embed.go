// Package migrations embeds the numbered SQL files so every binary that
// migrates (cmd/migrate, tests) carries the exact same set.
package migrations

import "embed"

// FS holds every NNNN_name.sql file in this directory.
//
//go:embed *.sql
var FS embed.FS
