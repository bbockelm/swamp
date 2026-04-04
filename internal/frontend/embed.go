//go:build embed_frontend

// Package frontend provides the embedded Next.js static export.
// Build with -tags embed_frontend to include the frontend in the binary.
// The dist/ directory must be populated (from frontend/out/) before building.
package frontend

import (
	"embed"
	"io/fs"
)

//go:embed all:dist
var content embed.FS

// DistFS returns the embedded frontend filesystem rooted at the export dir.
func DistFS() (fs.FS, error) {
	return fs.Sub(content, "dist")
}

// IsEmbedded reports whether the frontend is compiled into this binary.
func IsEmbedded() bool { return true }
