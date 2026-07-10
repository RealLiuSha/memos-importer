package api

import (
	"io/fs"
	"net/http"
	"strings"

	"memos-importer/web"
)

func StaticHandler() http.HandlerFunc {
	dist, err := fs.Sub(web.Assets, "dist")
	if err != nil {
		return func(w http.ResponseWriter, r *http.Request) {
			http.NotFound(w, r)
		}
	}
	fileServer := http.FileServer(http.FS(dist))
	return func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/") {
			http.NotFound(w, r)
			return
		}
		path := strings.TrimPrefix(r.URL.Path, "/")
		if path == "" {
			path = "index.html"
		}
		if _, err := fs.Stat(dist, path); err != nil {
			r.URL.Path = "/"
		}
		fileServer.ServeHTTP(w, r)
	}
}
