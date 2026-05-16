//go:build !embed_assets

package web

import (
	"errors"
	"io/fs"
)

func getAssets() (fs.FS, error) {
	return nil, errors.New("web assets not embedded (build without embed_assets tag)")
}
