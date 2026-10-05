// Package move implements the image-browser move command: filtering
// manifest entries by field and relocating their files to a destination
// directory.
package move

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"image-browser/internal/images"
	"image-browser/internal/manifest"
	"image-browser/internal/paths"
	"image-browser/internal/viewer"
)

// dateLayout is the expected format for -discoveredat values and the
// layout DiscoveredAt is compared against.
const dateLayout = "2006-01-02"

// manifestFilePattern is the glob pattern identifying a manifest file; see
// manifest.FilePattern.
const manifestFilePattern = manifest.FilePattern

// valueFlags lists every move flag that consumes a following argument as
// its value. Used to separate flags (and their values) from positional
// arguments before handing args to flag.FlagSet, the same way organize
// does for its own -root/-dest flags.
var valueFlags = []string{
	"-root", "-site", "-sourceurl", "-file", "-hash", "-status",
	"-error", "-original", "-discoveredat", "-width", "-height", "-dest",
}

func isValueFlag(arg string) bool {
	return slices.Contains(valueFlags, arg)
}

// criteria is the set of field filters a move invocation matches manifest
// entries against. All set fields must match (AND); matches reports false
// if any set field fails to match.
type criteria struct {
	site, sourceURL, file, hash, status, errSubstr, original, discoveredAt string
	width, height                                                          int
	hasWidth, hasHeight                                                    bool
}

func (c criteria) active() bool {
	return c.site != "" || c.sourceURL != "" || c.file != "" || c.hash != "" ||
		c.status != "" || c.errSubstr != "" || c.original != "" ||
		c.discoveredAt != "" || c.hasWidth || c.hasHeight
}

func (c criteria) matches(e manifest.Entry) bool {
	if c.site != "" && !containsFold(e.Site, c.site) {
		return false
	}
	if c.sourceURL != "" && !containsFold(e.SourceURL, c.sourceURL) {
		return false
	}
	if c.file != "" && !containsFold(e.File, c.file) {
		return false
	}
	if c.hash != "" && !strings.EqualFold(e.Hash, c.hash) {
		return false
	}
	if c.status != "" && !strings.EqualFold(string(e.Status), c.status) {
		return false
	}
	if c.errSubstr != "" && !containsFold(e.Error, c.errSubstr) {
		return false
	}
	if c.original != "" && !containsFold(e.Original, c.original) {
		return false
	}
	if c.discoveredAt != "" && e.DiscoveredAt.Format(dateLayout) != c.discoveredAt {
		return false
	}
	if c.hasWidth && e.Width != c.width {
		return false
	}
	if c.hasHeight && e.Height != c.height {
		return false
	}
	return true
}

func containsFold(s, substr string) bool {
	return strings.Contains(strings.ToLower(s), strings.ToLower(substr))
}

// deriveDestName builds a destination directory name from whichever
// criteria are set, in flag-declaration order, for use when -dest is not
// given.
func deriveDestName(c criteria) string {
	var parts []string
	if c.site != "" {
		parts = append(parts, c.site)
	}
	if c.sourceURL != "" {
		parts = append(parts, c.sourceURL)
	}
	if c.file != "" {
		parts = append(parts, c.file)
	}
	if c.hash != "" {
		parts = append(parts, c.hash)
	}
	if c.status != "" {
		parts = append(parts, c.status)
	}
	if c.errSubstr != "" {
		parts = append(parts, c.errSubstr)
	}
	if c.original != "" {
		parts = append(parts, c.original)
	}
	if c.discoveredAt != "" {
		parts = append(parts, c.discoveredAt)
	}
	if c.hasWidth {
		parts = append(parts, strconv.Itoa(c.width))
	}
	if c.hasHeight {
		parts = append(parts, strconv.Itoa(c.height))
	}
	return sanitizeDirName(strings.Join(parts, "-"))
}

// sanitizeDirName replaces characters that would turn a filter value (a
// full URL, say) into an unintended nested path or an invalid directory
// name, so the result is always safe as a single path component.
func sanitizeDirName(name string) string {
	var b strings.Builder
	for _, r := range name {
		switch r {
		case '/', '\\', ':', '*', '?', '"', '<', '>', '|':
			b.WriteByte('-')
		default:
			b.WriteRune(r)
		}
	}
	return strings.Trim(b.String(), "-")
}

// isManifestFileName reports whether path's base name matches the manifest
// file naming convention (manifest.json, image-manifest.json, ...).
func isManifestFileName(path string) bool {
	return manifest.IsFileName(path)
}

// findManifestFiles resolves root to the manifest file(s) it names: root
// itself if it is a single manifest file, or every file matching
// manifestFilePattern found by recursively walking root if it is a
// directory. See manifest.FindFiles.
func findManifestFiles(root string) ([]string, error) {
	return manifest.FindFiles(root)
}

// Run executes the move command.
func Run(args []string) error {
	fs := flag.NewFlagSet("move", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)

	rootFlag := fs.String("root", "", "manifest file, or directory to search recursively for manifest files (default: ~/Images)")
	siteFlag := fs.String("site", "", "match entries whose site contains value (case-insensitive)")
	sourceURLFlag := fs.String("sourceurl", "", "match entries whose source_url contains value (case-insensitive)")
	fileFlag := fs.String("file", "", "match entries whose file contains value (case-insensitive)")
	hashFlag := fs.String("hash", "", "match entries with exactly this hash")
	statusFlag := fs.String("status", "", "match entries with exactly this status")
	errorFlag := fs.String("error", "", "match entries whose error contains value (case-insensitive)")
	originalFlag := fs.String("original", "", "match entries whose original_file contains value (case-insensitive)")
	discoveredAtFlag := fs.String("discoveredat", "", "match entries discovered on this date (YYYY-MM-DD)")
	widthFlag := fs.Int("width", 0, "match entries with exactly this width")
	heightFlag := fs.Int("height", 0, "match entries with exactly this height")
	destFlag := fs.String("dest", "", "destination directory (default: derived from the match value)")
	previewFlag := fs.Bool("preview", false, "preview matches in the viewer before moving, with the option to change destination")

	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "Usage: image-browser move [value] [flags]")
		fmt.Fprintln(os.Stderr)
		fmt.Fprintln(os.Stderr, "A bare [value] is shorthand for -site. Multiple flags combine with AND.")
		fmt.Fprintln(os.Stderr)
		fmt.Fprintln(os.Stderr, "Flags:")
		fs.PrintDefaults()
	}

	// The standard flag package stops parsing at the first positional
	// argument. Extract positionals first, preserving flags and their
	// values for FlagSet to parse, the same way organize does.
	parseArgs := make([]string, 0, len(args))
	var positional []string

	for i := 0; i < len(args); i++ {
		arg := args[i]

		if !strings.HasPrefix(arg, "-") {
			positional = append(positional, arg)
			continue
		}

		parseArgs = append(parseArgs, arg)

		if isValueFlag(arg) {
			if i+1 >= len(args) {
				return fmt.Errorf("move: %s requires a value", arg)
			}
			i++
			parseArgs = append(parseArgs, args[i])
		}
	}

	if err := fs.Parse(parseArgs); err != nil {
		return err
	}

	var positionalValue string
	switch len(positional) {
	case 0:
	case 1:
		positionalValue = positional[0]
	default:
		return errors.New("move: too many positional arguments")
	}

	if positionalValue != "" && *siteFlag != "" {
		return errors.New("move: cannot combine a positional value with -site")
	}
	if positionalValue != "" {
		*siteFlag = positionalValue
	}

	if *discoveredAtFlag != "" {
		if _, err := time.Parse(dateLayout, *discoveredAtFlag); err != nil {
			return fmt.Errorf("move: invalid -discoveredat %q: %w", *discoveredAtFlag, err)
		}
	}

	set := make(map[string]bool)
	fs.Visit(func(f *flag.Flag) { set[f.Name] = true })

	c := criteria{
		site:         *siteFlag,
		sourceURL:    *sourceURLFlag,
		file:         *fileFlag,
		hash:         *hashFlag,
		status:       *statusFlag,
		errSubstr:    *errorFlag,
		original:     *originalFlag,
		discoveredAt: *discoveredAtFlag,
		width:        *widthFlag,
		height:       *heightFlag,
		hasWidth:     set["width"],
		hasHeight:    set["height"],
	}

	if !c.active() {
		return errors.New("move: no filter given (positional value or at least one -field flag required)")
	}

	root := *rootFlag
	if root == "" {
		root = paths.DefaultRoot()
	}
	root, err := paths.Abs(root)
	if err != nil {
		return fmt.Errorf("move: resolving root: %w", err)
	}

	var destDir string
	if *destFlag != "" {
		destDir, err = paths.Abs(*destFlag)
		if err != nil {
			return fmt.Errorf("move: resolving dest: %w", err)
		}
	} else {
		name := deriveDestName(c)
		if name == "" {
			return errors.New("move: could not derive a destination directory from the given filter; use -dest")
		}
		destDir, err = paths.Abs(name)
		if err != nil {
			return fmt.Errorf("move: resolving derived dest %q: %w", name, err)
		}
	}

	manifestFiles, err := findManifestFiles(root)
	if err != nil {
		return fmt.Errorf("move: finding manifest files under %s: %w", root, err)
	}
	if len(manifestFiles) == 0 {
		fmt.Printf("No manifest files found under %s\n", root)
		return nil
	}

	toProcess, totalMatched, loadErrs := loadAndFilter(manifestFiles, c)
	if totalMatched == 0 {
		fmt.Println("No manifest entries matched.")
		return errors.Join(loadErrs...)
	}

	if *previewFlag {
		return previewMove(toProcess, root, destDir)
	}

	return moveMatches(toProcess, totalMatched, destDir, loadErrs)
}

// manifestMatch pairs a loaded manifest file with the indexes of its
// entries that matched the given criteria.
type manifestMatch struct {
	mf      *manifest.File
	indexes []int
}

// loadAndFilter loads every manifest file, removes any entry that no
// longer belongs in it — its file no longer exists, or it lives outside
// this manifest's own directory tree, most likely a leftover from an
// older version of this program that rewrote a moved entry's File instead
// of removing it (see manifest.File.PruneInvalid, persisted immediately
// via Save so the removal survives even for a manifest with zero criteria
// matches) — and collects the indexes of the entries in each that match c.
// Manifest files with zero matches are omitted from toProcess entirely so
// later steps never touch them, though a pruned file is still saved above
// regardless of whether anything in it matched.
func loadAndFilter(manifestFiles []string, c criteria) (toProcess []manifestMatch, totalMatched int, errs []error) {
	for _, path := range manifestFiles {
		mf, err := manifest.LoadFile(path)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", path, err))
			continue
		}

		if removed := mf.PruneInvalid(); removed > 0 {
			fmt.Printf("Removed %d entries from %s that no longer belong in this manifest\n", removed, path)
			if err := mf.Save(); err != nil {
				errs = append(errs, fmt.Errorf("saving %s: %w", path, err))
			}
		}

		var indexes []int
		for i, e := range mf.Entries {
			if c.matches(e) {
				indexes = append(indexes, i)
			}
		}
		if len(indexes) == 0 {
			continue
		}

		totalMatched += len(indexes)
		toProcess = append(toProcess, manifestMatch{mf: mf, indexes: indexes})
	}
	return toProcess, totalMatched, errs
}

// moveMatches moves every entry in toProcess into destDir and reports a
// summary, folding in any errors already collected while loading manifests
// (loadErrs).
func moveMatches(toProcess []manifestMatch, totalMatched int, destDir string, loadErrs []error) error {
	fmt.Printf("Matched %d entries across %d manifest file(s); moving to %s\n", totalMatched, len(toProcess), destDir)

	moved, duplicates, failureMsgs, _, errs := performMoves(toProcess, destDir)
	errs = append(loadErrs, errs...)

	fmt.Printf("\nMoved %d; duplicates %d; failed %d (of %d matched)\n", moved, duplicates, len(failureMsgs), totalMatched)

	if len(failureMsgs) > 0 {
		errs = append(errs, fmt.Errorf("%d file(s) failed to move", len(failureMsgs)))
	}
	if len(errs) > 0 {
		return fmt.Errorf("move: %w", errors.Join(errs...))
	}
	return nil
}

// previewMove opens the viewer on every matched entry's file so the user
// can look the batch over before committing to the move, with the option
// to redirect to a different destination (see viewer.Browser.SetMoveAll's
// Change Destination control) or quit without moving anything.
//
// The Move All button's actual work is performMoves over the full,
// original toProcess every time it's confirmed, rather than just the
// paths viewer still has on screen: an entry that already moved still has
// its in-memory File rewritten to its new, already-in-destDir path (see
// moveEntries), even though that entry is no longer written back to the
// source manifest on disk (see performMoves' use of SavePruned) — so
// re-running performMoves against it resolves as a harmless
// duplicate-of-itself no-op, using the same in-memory index moveEntries
// used the first time. That makes a retry (after a partial failure) or a
// destination change mid-session both safe to implement as "just run it
// again".
// previewPaths resolves every matched entry in toProcess to an absolute
// source path for the preview viewer, skipping any entry with no File at
// all — the same guard moveEntries applies before resolving a source path.
// Without it, resolveSrc("", manifestDir) resolves to manifestDir itself
// (filepath.Join with an empty component is a no-op), handing the viewer a
// bare directory path it can neither thumbnail nor open.
func previewPaths(toProcess []manifestMatch) []string {
	paths := make([]string, 0, len(toProcess))
	for _, pm := range toProcess {
		manifestDir := filepath.Dir(pm.mf.Path)
		for _, i := range pm.indexes {
			if pm.mf.Entries[i].File == "" {
				continue
			}
			paths = append(paths, resolveSrc(pm.mf.Entries[i].File, manifestDir))
		}
	}
	sort.Strings(paths)
	return paths
}

func previewMove(toProcess []manifestMatch, root, destDir string) error {
	paths := previewPaths(toProcess)

	mover := func(_ []string, dest string) viewer.MoveAllResult {
		moved, duplicates, failureMsgs, movedPaths, errs := performMoves(toProcess, dest)
		for _, err := range errs {
			failureMsgs = append(failureMsgs, err.Error())
		}
		return viewer.MoveAllResult{
			Moved:      moved,
			Duplicates: duplicates,
			Failures:   failureMsgs,
			MovedPaths: movedPaths,
		}
	}

	return viewer.RunImagesWithMover(root, "", paths, false, destDir, mover)
}

// performMoves moves every matched entry in toProcess into destDir,
// updating and saving each affected manifest file, and merges the moved
// entries into destDir's own manifest. It is the shared core behind both
// move's direct (non-preview) path and the preview's Move All button.
//
// A moved entry is dropped from the source manifest file entirely rather
// than rewritten to point at destDir: a manifest only describes images in
// its own directory or a subdirectory of it (see manifest.InTree), and a
// stale pointer left behind serves no purpose — the destination directory
// already gets its own accurate entry below — while actively causing a
// later, separate run of this same command to find that pointer, see its
// filter criteria still match, and move the (already-moved) file a second
// time. Save uses SavePruned rather than Save for exactly this reason: see
// its doc comment for why the drop happens at save time, not by mutating
// pm.mf.Entries directly.
func performMoves(toProcess []manifestMatch, destDir string) (moved, duplicates int, failureMsgs, movedPaths []string, errs []error) {
	if err := os.MkdirAll(destDir, 0o755); err != nil {
		errs = append(errs, fmt.Errorf("creating dest %s: %w", destDir, err))
		return moved, duplicates, failureMsgs, movedPaths, errs
	}

	var destEntries []manifest.Entry

	for _, pm := range toProcess {
		manifestDir := filepath.Dir(pm.mf.Path)
		m, d, fMsgs, mPaths, movedEntries := moveEntries(pm.mf, pm.indexes, manifestDir, destDir)

		moved += m
		duplicates += d
		failureMsgs = append(failureMsgs, fMsgs...)
		movedPaths = append(movedPaths, mPaths...)
		destEntries = append(destEntries, movedEntries...)

		if m > 0 {
			if err := pm.mf.SavePruned(); err != nil {
				errs = append(errs, fmt.Errorf("saving %s: %w", pm.mf.Path, err))
			}
		}
	}

	if len(destEntries) > 0 {
		if err := manifest.MergeIntoDirectoryManifest(destDir, destEntries); err != nil {
			errs = append(errs, fmt.Errorf("updating destination manifest: %w", err))
		}
	}

	return moved, duplicates, failureMsgs, movedPaths, errs
}

// resolveSrc resolves a manifest entry's File to an absolute path, joining
// it against manifestDir when it was stored as a bare relative filename.
func resolveSrc(file, manifestDir string) string {
	if filepath.IsAbs(file) {
		return file
	}
	return filepath.Join(manifestDir, file)
}

// moveEntries moves the files named by mf.Entries[indexes] into destDir,
// rewriting each moved entry's File in place to its new absolute path.
// movedPaths holds the pre-move source path of each successfully moved
// entry, in moved order, for callers that need to know which originally
// requested paths are now done (see previewMove's doc comment).
func moveEntries(mf *manifest.File, indexes []int, manifestDir, destDir string) (moved, duplicates int, failureMsgs, movedPaths []string, movedEntries []manifest.Entry) {
	for _, i := range indexes {
		entry := &mf.Entries[i]

		if entry.File == "" {
			fmt.Fprintf(os.Stderr, "  skip (no file): site=%s\n", entry.Site)
			continue
		}

		src := resolveSrc(entry.File, manifestDir)

		if _, err := os.Stat(src); err != nil {
			msg := fmt.Sprintf("%s: %v", src, err)
			fmt.Fprintf(os.Stderr, "  error: %s\n", msg)
			failureMsgs = append(failureMsgs, msg)
			continue
		}

		destPath, duplicate, err := images.ResolveDestination(src, destDir)
		if err != nil {
			msg := fmt.Sprintf("resolving destination for %s: %v", src, err)
			fmt.Fprintf(os.Stderr, "  error: %s\n", msg)
			failureMsgs = append(failureMsgs, msg)
			continue
		}

		if duplicate {
			fmt.Printf("  duplicate (left in place): %s\n", src)
			duplicates++
			continue
		}

		if err := images.Move(src, destPath); err != nil {
			msg := fmt.Sprintf("moving %s: %v", src, err)
			fmt.Fprintf(os.Stderr, "  error: %s\n", msg)
			failureMsgs = append(failureMsgs, msg)
			continue
		}

		fmt.Printf("  %s -> %s\n", src, destPath)
		entry.File = destPath
		movedEntries = append(movedEntries, *entry)
		movedPaths = append(movedPaths, src)
		moved++
	}
	return moved, duplicates, failureMsgs, movedPaths, movedEntries
}
