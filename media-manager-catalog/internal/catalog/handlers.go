package catalog

import (
	"log/slog"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"

	"media-manager/internal/catalogmodel"
	catalogtpl "media-manager/web/templates/catalog"

	"github.com/go-chi/chi/v5"
)

type handler struct {
	log      *slog.Logger
	imageDir string
}

// Mount wires the catalog routes onto r. imageDir is scanned fresh on every
// request to /catalog, so images added to the directory are picked up
// without a server restart.
func Mount(r chi.Router, log *slog.Logger, imageDir string) {
	h := &handler{
		log:      log,
		imageDir: imageDir,
	}

	r.Get("/catalog", h.home)
	r.Get("/catalog/view", h.view)
}

// resolveDir confines a user-supplied ?dir value to imageDir. It returns the
// absolute filesystem path to scan, or false if the request escapes the jail.
func resolveDir(imageDir, dir string) (string, bool) {
	absRoot, err := canonicalRoot(imageDir)
	if err != nil {
		return "", false
	}

	// Prevent ".." escaping the image root.
	rel := strings.TrimPrefix(
		filepath.Clean("/"+filepath.FromSlash(dir)),
		string(os.PathSeparator),
	)

	target := filepath.Join(absRoot, rel)

	// Resolve symlinks if possible.
	if resolved, err := filepath.EvalSymlinks(target); err == nil {
		target = resolved
	}

	if target != absRoot &&
		!strings.HasPrefix(target, absRoot+string(os.PathSeparator)) {
		return "", false
	}

	return target, true
}

// canonicalRoot returns the absolute, symlink-resolved form of imageDir.
func canonicalRoot(imageDir string) (string, error) {
	absRoot, err := filepath.Abs(imageDir)
	if err != nil {
		return "", err
	}

	if resolved, err := filepath.EvalSymlinks(absRoot); err == nil {
		return resolved, nil
	}

	return absRoot, nil
}

func (h *handler) home(w http.ResponseWriter, r *http.Request) {
	absRoot, err := canonicalRoot(h.imageDir)
	if err != nil {
		h.log.Error("resolve image dir failed", "err", err)
		http.Error(w, "server misconfiguration", http.StatusInternalServerError)
		return
	}

	target, ok := resolveDir(h.imageDir, r.URL.Query().Get("dir"))
	if !ok {
		http.Error(w, "invalid directory", http.StatusBadRequest)
		return
	}

	page, err := ScanCatalog(absRoot, target, h.log)
	if err != nil {
		h.log.Error("scan catalog failed", "dir", target, "err", err)
		http.Error(w, "failed to scan directory", http.StatusInternalServerError)
		return
	}

	// HTMX requests only receive the catalog fragment.
	if r.Header.Get("HX-Request") == "true" {
		if err := catalogtpl.CatalogContent(page).Render(r.Context(), w); err != nil {
			h.log.Error("render catalog content failed", "err", err)
		}
		return
	}

	// Normal browser request gets the full page.
	if err := catalogtpl.Catalog(page).Render(r.Context(), w); err != nil {
		h.log.Error("render catalog page failed", "err", err)
	}
}

// view renders a single image at full size, in its own page with
// navigation back to the grid. It's confined to imageDir by the same jail
// as home, reusing resolveDir even though the target here is a file rather
// than a directory — the jail logic is purely lexical path arithmetic, so
// it applies equally to both.
func (h *handler) view(w http.ResponseWriter, r *http.Request) {
	target, ok := resolveDir(h.imageDir, r.URL.Query().Get("path"))
	if !ok {
		http.Error(w, "invalid path", http.StatusBadRequest)
		return
	}

	info, err := os.Stat(target)
	if err != nil || info.IsDir() {
		http.NotFound(w, r)
		return
	}

	absRoot, err := canonicalRoot(h.imageDir)
	if err != nil {
		h.log.Error("resolve image dir failed", "err", err)
		http.Error(w, "server misconfiguration", http.StatusInternalServerError)
		return
	}

	rel, err := filepath.Rel(absRoot, target)
	if err != nil {
		h.log.Error("compute relative image path failed", "path", target, "err", err)
		http.Error(w, "server misconfiguration", http.StatusInternalServerError)
		return
	}
	rel = filepath.ToSlash(rel)

	backDir := path.Dir(rel)
	if backDir == "." {
		backDir = ""
	}

	img := catalogmodel.Image{
		Name: info.Name(),
		Path: path.Join("/images", rel),
	}

	if err := catalogtpl.ImageView(img, backDir).Render(r.Context(), w); err != nil {
		h.log.Error("render image view failed", "err", err)
	}
}
