// Package migrations embeds the SQL schema files in this directory so the
// server binary carries them without needing the migrations/ directory to
// be present alongside it at runtime (useful once it's just an scp'd
// binary on the mini PC).
package migrations

import _ "embed"

//go:embed 0001_init.sql
var Init string
