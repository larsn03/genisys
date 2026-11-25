// Package ui embeds the built web front-end (SPEC §19: go:embed ui/dist,
// served by the same binary).
package ui

import (
	"embed"
	"io/fs"
)

//go:embed all:dist
var dist embed.FS

// Dist returns the embedded static file tree.
func Dist() fs.FS {
	sub, err := fs.Sub(dist, "dist")
	if err != nil {
		panic("ui: " + err.Error())
	}
	return sub
}
