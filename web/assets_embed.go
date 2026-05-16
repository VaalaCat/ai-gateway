//go:build embed_assets

package web

import (
	"embed"
	"io/fs"
)

//go:embed all:dist
var assets embed.FS

func getAssets() (fs.FS, error) {
	return fs.Sub(assets, "dist")
}
