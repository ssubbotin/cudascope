package cudascope

import (
	"embed"
	"io/fs"
)

//go:embed all:ui/build
var uiDist embed.FS

// UIFS returns the embedded UI filesystem rooted at ui/build.
func UIFS() (fs.FS, error) {
	return fs.Sub(uiDist, "ui/build")
}
