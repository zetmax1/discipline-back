// Package migrations holds the versioned SQL schema migrations (goose format).
// Every change after the baseline is a new file with both Up and Down sections.
package migrations

import "embed"

// FS contains every *.sql migration, embedded into the binaries.
//
//go:embed *.sql
var FS embed.FS
