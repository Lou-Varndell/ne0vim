package catalog

import (
	"image"
	"image/color"
	"image/png"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
)

func TestIsText(t *testing.T) {
	cases := []struct {
		mime string
		want bool
	}{
		{"text/plain; charset=utf-8", true},
		{"application/json", true},
		{"application/xml", true},
		{"application/javascript", true},
		{"application/x-yaml", true},
		{"application/yaml", true},
		{"image/png", false},
		{"image/jpeg", false},
		{"application/octet-stream", false},
	}

	for _, c := range cases {
		if got := isText(c.mime); got != c.want {
			t.Errorf("isText(%q) = %v, want %v", c.mime, got, c.want)
		}
	}
}

func writePNG(t *testing.T, path string, width, height int) {
	t.Helper()

	img := image.NewRGBA(image.Rect(0, 0, width, height))
	for y := range height {
		for x := range width {
			img.Set(x, y, color.White)
		}
	}

	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("create %s: %v", path, err)
	}
	defer f.Close()

	if err := png.Encode(f, img); err != nil {
		t.Fatalf("encode png %s: %v", path, err)
	}
}

func quietLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelError + 1}))
}

// TestScanCatalog_ListsOneLevel verifies ScanCatalog returns only the
// immediate directory contents, with slash-separated relative paths and a
// breadcrumb trail.
func TestScanCatalog_ListsOneLevel(t *testing.T) {
	root := t.TempDir()

	writePNG(t, filepath.Join(root, "top.png"), 50, 50)
	sub := filepath.Join(root, "sub")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatalf("mkdir sub: %v", err)
	}
	writePNG(t, filepath.Join(sub, "nested.png"), 50, 50)

	page, err := ScanCatalog(root, root, quietLogger())
	if err != nil {
		t.Fatalf("ScanCatalog: %v", err)
	}

	if len(page.Images) != 1 || page.Images[0].Name != "top.png" {
		t.Errorf("root images = %+v, want just top.png", page.Images)
	}
	if page.Images[0].Path != "/images/top.png" {
		t.Errorf("image path = %q, want /images/top.png", page.Images[0].Path)
	}
	if len(page.Directories) != 1 || page.Directories[0].Name != "sub" {
		t.Errorf("directories = %+v, want just sub", page.Directories)
	}
	if page.Directories[0].Path != "sub" {
		t.Errorf("dir path = %q, want sub", page.Directories[0].Path)
	}
	if len(page.Breadcrumb) != 1 || page.Breadcrumb[0].Name != "Home" {
		t.Errorf("breadcrumb = %+v, want just Home", page.Breadcrumb)
	}
}

// TestScanCatalog_SkipsButKeepsNonImages guards the security fix: scanning
// is read-only, so a text file and .DS_Store are excluded from the result
// but must never be deleted from disk.
func TestScanCatalog_SkipsButKeepsNonImages(t *testing.T) {
	dir := t.TempDir()

	textPath := filepath.Join(dir, "notes.txt")
	if err := os.WriteFile(textPath, []byte("hello world"), 0o644); err != nil {
		t.Fatalf("write text file: %v", err)
	}

	dsStorePath := filepath.Join(dir, ".DS_Store")
	if err := os.WriteFile(dsStorePath, []byte("junk"), 0o644); err != nil {
		t.Fatalf("write .DS_Store: %v", err)
	}

	writePNG(t, filepath.Join(dir, "small.png"), 100, 100)

	page, err := ScanCatalog(dir, dir, quietLogger())
	if err != nil {
		t.Fatalf("ScanCatalog: %v", err)
	}

	if len(page.Images) != 1 || page.Images[0].Name != "small.png" {
		t.Fatalf("images = %+v, want just small.png", page.Images)
	}

	if _, err := os.Stat(textPath); err != nil {
		t.Errorf("notes.txt must not be removed by a scan, stat err = %v", err)
	}
	if _, err := os.Stat(dsStorePath); err != nil {
		t.Errorf(".DS_Store must not be removed by a scan, stat err = %v", err)
	}
}

// TestScanCatalog_BreadcrumbSiblings verifies each non-root crumb carries
// the other directories at its own level, powering the breadcrumb's
// sideways-navigation dropdown.
func TestScanCatalog_BreadcrumbSiblings(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"alpha", "beta"} {
		if err := os.Mkdir(filepath.Join(root, name), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", name, err)
		}
	}
	nested := filepath.Join(root, "alpha", "one")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatalf("mkdir nested: %v", err)
	}
	if err := os.Mkdir(filepath.Join(root, "alpha", "two"), 0o755); err != nil {
		t.Fatalf("mkdir two: %v", err)
	}

	page, err := ScanCatalog(root, nested, quietLogger())
	if err != nil {
		t.Fatalf("ScanCatalog: %v", err)
	}

	if len(page.Breadcrumb) != 3 {
		t.Fatalf("breadcrumb = %+v, want 3 crumbs", page.Breadcrumb)
	}
	home, alpha, one := page.Breadcrumb[0], page.Breadcrumb[1], page.Breadcrumb[2]

	if len(home.Siblings) != 0 {
		t.Errorf("Home siblings = %+v, want none", home.Siblings)
	}

	wantAlphaSiblings := map[string]bool{"alpha": true, "beta": true}
	if len(alpha.Siblings) != len(wantAlphaSiblings) {
		t.Fatalf("alpha siblings = %+v, want %v", alpha.Siblings, wantAlphaSiblings)
	}
	for _, s := range alpha.Siblings {
		if !wantAlphaSiblings[s.Name] {
			t.Errorf("unexpected alpha sibling %q", s.Name)
		}
	}

	wantOneSiblings := map[string]bool{"one": true, "two": true}
	if len(one.Siblings) != len(wantOneSiblings) {
		t.Fatalf("one siblings = %+v, want %v", one.Siblings, wantOneSiblings)
	}
	for _, s := range one.Siblings {
		if !wantOneSiblings[s.Name] {
			t.Errorf("unexpected sibling %q", s.Name)
		}
		if s.Name == "two" && s.Path != "alpha/two" {
			t.Errorf("two's path = %q, want alpha/two", s.Path)
		}
	}
}

func TestResolveDir(t *testing.T) {
	root := t.TempDir()
	sub := filepath.Join(root, "sub")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatalf("mkdir sub: %v", err)
	}

	absRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatalf("resolve root: %v", err)
	}

	cases := []struct {
		name   string
		dir    string
		want   string
		wantOK bool
	}{
		{"empty resolves to root", "", absRoot, true},
		{"sub resolves under root", "sub", filepath.Join(absRoot, "sub"), true},
		// ".." segments are clamped at the root, so this lands at root/etc
		// (inside the jail), never at the real /etc.
		{"dot-dot cannot escape", "../../../etc", filepath.Join(absRoot, "etc"), true},
		{"leading slash is treated relative", "/sub", filepath.Join(absRoot, "sub"), true},
		{"deep traversal is clamped", "sub/../../..", absRoot, true},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, ok := resolveDir(root, c.dir)
			if ok != c.wantOK {
				t.Fatalf("resolveDir(%q) ok = %v, want %v", c.dir, ok, c.wantOK)
			}
			if ok && got != c.want {
				t.Errorf("resolveDir(%q) = %q, want %q", c.dir, got, c.want)
			}
		})
	}
}
