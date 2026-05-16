package web

import "io/fs"

// GetAssets returns the embedded frontend assets (when built with the
// "embed_assets" tag) or an error indicating that assets were not embedded.
func GetAssets() (fs.FS, error) {
	return getAssets()
}
