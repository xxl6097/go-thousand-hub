package server

import (
	"embed"
	"io/fs"
)

//go:embed webroot
var webFS embed.FS

func mustSubFS(fsys fs.FS, dir string) fs.FS {
	sub, err := fs.Sub(fsys, dir)
	if err != nil {
		panic(err)
	}
	return sub
}
