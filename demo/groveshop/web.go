package groveshop

import (
	"embed"
	"io/fs"
	"net/http"
)

// webFiles contains the browser application shipped in the Grove Shop
// deployment artifact.
//
//go:embed web/index.html
var webFiles embed.FS

// WebHandler serves the Grove Shop browser assets without runtime files or a
// separate frontend deployment.
func WebHandler() http.Handler {
	root, err := fs.Sub(webFiles, "web")
	if err != nil {
		panic(err)
	}
	return http.FileServer(http.FS(root))
}

// WebAsset returns one embedded Grove Shop browser asset for artifact tests and
// tooling.
func WebAsset(name string) ([]byte, error) {
	return webFiles.ReadFile("web/" + name)
}
