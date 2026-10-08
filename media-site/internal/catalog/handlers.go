package catalog

import (
	"errors"
	"log/slog"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"

	"media-site/internal/catalogdb"
	"media-site/internal/catalogmodel"
	catalogtpl "media-site/web/templates/catalog"

	"github.com/go-chi/chi/v5"
)

type handler struct {
	log      *slog.Logger
	imageDir string
	db       *catalogdb.DB
}

// Mount wires the catalog routes onto r. Catalog pages are served entirely
// from db, which must already hold a complete index of imageDir (built by
// ScanToDB at startup) — the handlers never walk the filesystem themselves,
// only jail user-supplied paths to imageDir before looking them up.
func Mount(r chi.Router, log *slog.Logger, imageDir string, db *catalogdb.DB) {
	h := &handler{
		log:      log,
		imageDir: imageDir,
		db:       db,
	}

	r.Get("/catalog", h.home)
	r.Get("/catalog/images/more", h.moreImages)
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

	rel, err := relCatalogPath(absRoot, target)
	if err != nil {
		h.log.Error("compute relative catalog path failed", "path", target, "err", err)
		http.Error(w, "server misconfiguration", http.StatusInternalServerError)
		return
	}

	page, err := LoadCatalogPage(h.db, rel)
	if err != nil {
		if errors.Is(err, ErrDirectoryNotFound) {
			http.Error(w, "invalid directory", http.StatusBadRequest)
			return
		}
		h.log.Error("load catalog page failed", "dir", rel, "err", err)
		http.Error(w, "failed to load catalog", http.StatusInternalServerError)
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

// moreImages serves one additional batch of images for infinite scroll,
// continuing dir from offset. It renders only the appended grid cells
// (plus a fresh sentinel, if more remain) — never the breadcrumb or
// directory listing, which the browser already has from the initial load.
func (h *handler) moreImages(w http.ResponseWriter, r *http.Request) {
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

	rel, err := relCatalogPath(absRoot, target)
	if err != nil {
		h.log.Error("compute relative catalog path failed", "path", target, "err", err)
		http.Error(w, "server misconfiguration", http.StatusInternalServerError)
		return
	}

	offset, err := strconv.Atoi(r.URL.Query().Get("offset"))
	if err != nil || offset < 0 {
		http.Error(w, "invalid offset", http.StatusBadRequest)
		return
	}

	batch, err := loadImageBatch(h.db, rel, offset)
	if err != nil {
		h.log.Error("load image batch failed", "dir", rel, "offset", offset, "err", err)
		http.Error(w, "failed to load images", http.StatusInternalServerError)
		return
	}

	if err := catalogtpl.ImageGridItems(rel, batch).Render(r.Context(), w); err != nil {
		h.log.Error("render image batch failed", "err", err)
	}
}

// view renders a single image at full size, in its own page with
// navigation back to the grid. It's confined to imageDir by the same jail
// as home, reusing resolveDir even though the target here is a file rather
// than a directory — the jail logic is purely lexical path arithmetic, so
// it applies equally to both.
func (h *handler) view(w http.ResponseWriter, r *http.Request) {
	absRoot, err := canonicalRoot(h.imageDir)
	if err != nil {
		h.log.Error("resolve image dir failed", "err", err)
		http.Error(w, "server misconfiguration", http.StatusInternalServerError)
		return
	}

	target, ok := resolveDir(h.imageDir, r.URL.Query().Get("path"))
	if !ok {
		http.Error(w, "invalid path", http.StatusBadRequest)
		return
	}

	rel, err := relCatalogPath(absRoot, target)
	if err != nil {
		h.log.Error("compute relative catalog path failed", "path", target, "err", err)
		http.Error(w, "server misconfiguration", http.StatusInternalServerError)
		return
	}

	img, found, err := h.db.GetImage(rel)
	if err != nil {
		h.log.Error("load image failed", "path", rel, "err", err)
		http.Error(w, "failed to load image", http.StatusInternalServerError)
		return
	}
	if !found {
		http.NotFound(w, r)
		return
	}

	backDir := path.Dir(rel)
	if backDir == "." {
		backDir = ""
	}

	viewImg := catalogmodel.Image{
		Name: img.Name,
		Path: path.Join("/images", rel),
	}

	if err := catalogtpl.ImageView(viewImg, backDir).Render(r.Context(), w); err != nil {
		h.log.Error("render image view failed", "err", err)
	}
}
