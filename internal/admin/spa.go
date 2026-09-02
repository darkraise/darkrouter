package admin

import (
	"bytes"
	"embed"
	"io/fs"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"time"
)

// distFS is the built SPA.
//
// The bundle lands in this package's own dist/ rather than in web/dist because
// an embed directive cannot reference a path outside its package directory;
// web's Vite config writes here.
//
// "all:" is required, not decorative: without it go:embed skips files beginning
// with a dot, and dist/ holds only .gitkeep on a fresh clone — an empty pattern
// is a compile error, so the repository would not build at all.
//
//go:embed all:dist
var distFS embed.FS

// assetPrefix is where Vite writes content-hashed files. A file under it
// changes name when its content changes, which is what makes a year-long
// cache safe; index.html keeps its name and must be revalidated every time.
const assetPrefix = "/assets/"

// spaHandler serves the built SPA, falling back to index.html.
//
// The fallback is what makes deep links work: a browser reloading on
// /requests/01ABC asks the server for that path, and the router that knows what
// it means only exists once index.html has loaded.
//
// The index is read once at construction rather than per request. It is a few
// kilobytes, it never changes for the life of the process, and http.ServeContent
// needs an io.ReadSeeker that fs.File does not provide.
func (s *Server) spaHandler() http.Handler {
	sub, err := fs.Sub(distFS, "dist")
	if err != nil {
		return notBuilt()
	}
	index, err := fs.ReadFile(sub, "index.html")
	if err != nil {
		// A .gitkeep-only dist: the binary was built without running the
		// frontend build. Saying so beats serving a blank page.
		return notBuilt()
	}
	files := http.FileServer(http.FS(sub))
	modTime := time.Now()

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// A real file wins: assets, the favicon, anything with an extension.
		// A directory never does — http.FileServer would list it.
		if r.URL.Path != "/" {
			if isFile(sub, strings.TrimPrefix(r.URL.Path, "/")) {
				if strings.HasPrefix(r.URL.Path, assetPrefix) {
					w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
				}
				files.ServeHTTP(w, r)
				return
			}
			if strings.HasPrefix(r.URL.Path, assetPrefix) || r.URL.Path == strings.TrimSuffix(assetPrefix, "/") {
				http.NotFound(w, r)
				return
			}
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache")
		http.ServeContent(w, r, "index.html", modTime, bytes.NewReader(index))
	})
}

func isFile(fsys fs.FS, name string) bool {
	info, err := fs.Stat(fsys, name)
	return err == nil && !info.IsDir()
}

func notBuilt() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "the dashboard was not built into this binary", http.StatusNotFound)
	})
}

// devProxy forwards unmatched paths to the Vite dev server so hot reload works
// against the real API rather than a mock. Spec §5.
func devProxy(target string) (http.Handler, error) {
	u, err := url.Parse(target)
	if err != nil {
		return nil, err
	}
	return httputil.NewSingleHostReverseProxy(u), nil
}
