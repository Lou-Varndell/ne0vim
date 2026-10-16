package filedialog

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/storage"
	"fyne.io/fyne/v2/storage/repository"
)

// osFileRepository is a minimal, test-only "file" scheme repository backed
// directly by the os package. Fyne only registers a real one as a side
// effect of starting a native app (fyne.io/fyne/v2/app), which needs a
// display and is unsuitable for unit tests; buildDirectoryData depends on
// storage.Parent and storage.ListerForURI, both of which dispatch through
// whatever repository is registered for the URI's scheme. Registering this
// stand-in lets those calls resolve against the real filesystem without
// starting a native app. Only the operations buildDirectoryData's tests
// exercise (Parent/Child, CanList/List) are implemented for real; the rest
// are unused stubs.
type osFileRepository struct{}

func (osFileRepository) Exists(u fyne.URI) (bool, error) {
	_, err := os.Stat(u.Path())
	if err == nil {
		return true, nil
	}
	if os.IsNotExist(err) {
		return false, nil
	}
	return false, err
}

func (osFileRepository) Reader(fyne.URI) (fyne.URIReadCloser, error) {
	return nil, errors.New("osFileRepository: Reader not supported in tests")
}

func (osFileRepository) CanRead(fyne.URI) (bool, error) { return true, nil }

func (osFileRepository) Destroy(string) {}

func (osFileRepository) CanList(u fyne.URI) (bool, error) {
	info, err := os.Stat(u.Path())
	if err != nil {
		return false, err
	}
	return info.IsDir(), nil
}

func (osFileRepository) List(u fyne.URI) ([]fyne.URI, error) {
	entries, err := os.ReadDir(u.Path())
	if err != nil {
		return nil, err
	}
	uris := make([]fyne.URI, 0, len(entries))
	for _, e := range entries {
		uris = append(uris, storage.NewFileURI(filepath.Join(u.Path(), e.Name())))
	}
	return uris, nil
}

func (osFileRepository) CreateListable(u fyne.URI) error {
	return os.MkdirAll(u.Path(), 0o755)
}

func (osFileRepository) Parent(u fyne.URI) (fyne.URI, error) {
	return repository.GenericParent(u)
}

func (osFileRepository) Child(u fyne.URI, component string) (fyne.URI, error) {
	return repository.GenericChild(u, component)
}

func TestMain(m *testing.M) {
	repository.Register("file", osFileRepository{})
	os.Exit(m.Run())
}

func TestIsHiddenURI(t *testing.T) {
	tests := []struct {
		name string
		want bool
	}{
		{"visible.jpg", false},
		{".hidden.jpg", true},
	}

	dir := t.TempDir()
	for _, tt := range tests {
		uri := storage.NewFileURI(filepath.Join(dir, tt.name))
		if got := isHiddenURI(uri); got != tt.want {
			t.Errorf("isHiddenURI(%q) = %v, want %v", tt.name, got, tt.want)
		}
	}

	if got := isHiddenURI(storage.NewFileURI(".")); got {
		t.Errorf(`isHiddenURI(".") = %v, want false`, got)
	}
	if got := isHiddenURI(storage.NewFileURI("..")); got {
		t.Errorf(`isHiddenURI("..") = %v, want false`, got)
	}
	if got := isHiddenURI(nil); got {
		t.Errorf("isHiddenURI(nil) = %v, want false", got)
	}
}

func TestExpandHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	tests := []struct {
		path string
		want string
	}{
		{"~", home},
		{"~/Photos", filepath.Join(home, "Photos")},
		{"/tmp/foo/../bar", "/tmp/bar"},
	}

	for _, tt := range tests {
		if got := expandHome(tt.path); got != tt.want {
			t.Errorf("expandHome(%q) = %q, want %q", tt.path, got, tt.want)
		}
	}
}

func TestCountImagesCountsDirectImagesOnly(t *testing.T) {
	dir := t.TempDir()
	write := func(name string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("a.jpg")
	write("b.png")
	write("notes.txt")

	if err := os.Mkdir(filepath.Join(dir, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "sub", "nested.jpg"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	if got := countImages(storage.NewFileURI(dir)); got != 2 {
		t.Fatalf("countImages() = %d, want 2", got)
	}
}

func TestCountImagesReturnsZeroForUnreadableDir(t *testing.T) {
	if got := countImages(storage.NewFileURI(filepath.Join(t.TempDir(), "missing"))); got != 0 {
		t.Fatalf("countImages() = %d, want 0 for a missing directory", got)
	}
}

func TestBuildDirectoryDataFolderModeListsOnlyDirsWithImageCounts(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "Beach"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "Beach", "a.jpg"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "Beach", "b.jpg"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, "Empty"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "readme.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	list, err := storage.ListerForURI(storage.NewFileURI(root))
	if err != nil {
		t.Fatal(err)
	}
	entries, err := list.List()
	if err != nil {
		t.Fatal(err)
	}

	data := buildDirectoryData(list, entries, true, false, nil)

	// The root's parent is a real directory, so the first entry is the
	// synthetic ".." row; the rest are the two subdirectories, sorted, and
	// the plain file is excluded entirely since folder mode only lists dirs.
	if len(data) != 3 {
		t.Fatalf("buildDirectoryData() returned %d entries, want 3: %+v", len(data), data)
	}
	if !data[0].isDir {
		t.Fatalf("buildDirectoryData()[0] = %+v, want the parent directory entry", data[0])
	}

	names := []string{data[1].uri.Name(), data[2].uri.Name()}
	if names[0] != "Beach" || names[1] != "Empty" {
		t.Fatalf("buildDirectoryData() dir order = %v, want [Beach Empty]", names)
	}
	if data[1].imageCount != 2 {
		t.Fatalf("buildDirectoryData() Beach imageCount = %d, want 2", data[1].imageCount)
	}
	if data[2].imageCount != 0 {
		t.Fatalf("buildDirectoryData() Empty imageCount = %d, want 0", data[2].imageCount)
	}
}

func TestBuildDirectoryDataFileModeAppliesFilterAndKeepsDirs(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "a.jpg"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "notes.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	list, err := storage.ListerForURI(storage.NewFileURI(root))
	if err != nil {
		t.Fatal(err)
	}
	entries, err := list.List()
	if err != nil {
		t.Fatal(err)
	}

	filter := storage.NewExtensionFileFilter([]string{".jpg"})
	data := buildDirectoryData(list, entries, false, false, filter)

	// data[0] is the synthetic "go to parent" row that buildDirectoryData
	// always prepends when the location has a parent; the rest is what's
	// actually inside root.
	var names []string
	for _, e := range data[1:] {
		names = append(names, e.uri.Name())
	}
	if len(names) != 2 || names[0] != "a.jpg" || names[1] != "sub" {
		t.Fatalf("buildDirectoryData() file-mode entries = %v, want [a.jpg sub]", names)
	}
}

func TestBuildDirectoryDataHiddenFiles(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "visible.jpg"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".hidden.jpg"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	list, err := storage.ListerForURI(storage.NewFileURI(root))
	if err != nil {
		t.Fatal(err)
	}
	entries, err := list.List()
	if err != nil {
		t.Fatal(err)
	}

	hidden := buildDirectoryData(list, entries, false, false, nil)
	shown := buildDirectoryData(list, entries, false, true, nil)

	if len(hidden) != len(shown)-1 {
		t.Fatalf("hiding vs showing dotfiles: got %d vs %d entries, want a difference of 1", len(hidden), len(shown))
	}
}

func TestFindTypeAhead(t *testing.T) {
	f := &FileDialog{
		data: []dirEntry{
			{uri: storage.NewFileURI("/root/Beach"), isDir: true},
			{uri: storage.NewFileURI("/root/apple.jpg"), isDir: false},
			{uri: storage.NewFileURI("/root/banana.jpg"), isDir: false},
		},
	}

	if i := f.findTypeAhead("ba", false); i != 2 {
		t.Fatalf("findTypeAhead(%q, false) = %d, want 2", "ba", i)
	}
	if i := f.findTypeAhead("b", true); i != 0 {
		t.Fatalf("findTypeAhead(%q, true) = %d, want 0 (Beach, dirs only)", "b", i)
	}
	if i := f.findTypeAhead("zz", false); i != -1 {
		t.Fatalf("findTypeAhead(%q, false) = %d, want -1", "zz", i)
	}
}
