// Package ui embeds the compiled React SPA so it ships inside the binary.
//
// In M1 the dist/ directory contains only a .gitkeep placeholder; the SPA
// lands in M3 (`make ui` builds and copies it into dist/). Until then,
// Available() returns false and callers should fall back to a JSON banner
// or static HTML stub.
package ui

import (
	"embed"
	"errors"
	"io/fs"
)

//go:embed all:dist
var distFS embed.FS

// Available reports whether a real SPA was built into dist/. It returns
// false when only .gitkeep is present so the composition layer can avoid
// mounting an empty SPA.
func Available() bool {
	entries, err := distFS.ReadDir("dist")
	if err != nil {
		return false
	}
	for _, e := range entries {
		if e.Name() == "index.html" {
			return true
		}
	}
	return false
}

// FS returns the dist subtree rooted at "dist". Returns an error when the
// embed is empty (no SPA built); callers should check Available() first.
func FS() (fs.FS, error) {
	sub, err := fs.Sub(distFS, "dist")
	if err != nil {
		return nil, err
	}
	if !Available() {
		return nil, errors.New("ui: dist/ has no index.html (run `make ui` to build the SPA)")
	}
	return sub, nil
}
