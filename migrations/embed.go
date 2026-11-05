// Package migrations embeds the SQL schema migrations so the binary can
// apply them without access to the source tree.
package migrations

import "embed"

// FS holds every NNNN_name.sql file in this directory.
//
//go:embed *.sql
var FS embed.FS
