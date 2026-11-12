// SPDX-License-Identifier: GPL-3.0-or-later

// Package ui embeds the compiled frontend (built by `npm run build` in web/ into
// internal/ui/dist) and serves it as a single-page application.
package ui

import (
	"embed"
	"errors"
	"io/fs"
	"net/http"
	"path"
	"strings"
)

//go:embed all:dist
var embedded embed.FS

// Dist returns the embedded frontend build.
func Dist() fs.FS {
	sub, err := fs.Sub(embedded, "dist")
	if err != nil {
		panic(err) // cannot happen: "dist" is a valid, embedded directory
	}
	return sub
}

const indexFile = "index.html"

// Handler serves static files from fsys. Paths without a file extension that
// do not exist (client-side routes) are answered with index.html.
//
// Hashed build assets below /assets/ are cached for a year; everything else
// must be revalidated so that new releases are picked up immediately.
func Handler(fsys fs.FS) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
			return
		}

		name := strings.TrimPrefix(path.Clean("/"+r.URL.Path), "/")
		if name == "" || name == indexFile {
			serveIndex(w, r, fsys)
			return
		}

		if fi, err := fs.Stat(fsys, name); err == nil && !fi.IsDir() {
			if strings.HasPrefix(name, "assets/") {
				w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
			} else {
				w.Header().Set("Cache-Control", "no-cache")
			}
			http.ServeFileFS(w, r, fsys, name)
			return
		}

		if path.Ext(name) != "" {
			http.NotFound(w, r) // a missing file, not a client-side route
			return
		}
		serveIndex(w, r, fsys)
	})
}

func serveIndex(w http.ResponseWriter, r *http.Request, fsys fs.FS) {
	data, err := fs.ReadFile(fsys, indexFile)
	if errors.Is(err, fs.ErrNotExist) {
		http.Error(w, "frontend not built: run `make build-web`", http.StatusServiceUnavailable)
		return
	}
	if err != nil {
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	if r.Method == http.MethodHead {
		return
	}
	_, _ = w.Write(data)
}
