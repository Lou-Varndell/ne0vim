// Package move implements the image-browser move command: filtering
// database file records by field and relocating their files to a
// destination directory.
package move

import (
	"context"
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

	invlib "inv-lib"

	"image-browser/internal/images"
	"image-browser/internal/invdb"
	"image-browser/internal/paths"
	"image-browser/internal/viewer"
)

// dateLayout is the expected format for -discoveredat values and the
// layout DiscoveredAt is compared against.
const dateLayout = "2006-01-02"

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

// criteria is the set of field filters a move invocation matches database
// file records against. All set fields must match (AND); matches reports
// false if any set field fails to match.
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

// derefString, derefInt, and derefTime return the pointed-to value, or the
// type's zero value for a nil pointer — invlib.FileRecord's Origin and
// Image are nil when a file has no origin or no images row, and their
// fields are themselves nullable pointers.
func derefString(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

func derefInt(p *int) int {
	if p == nil {
		return 0
	}
	return *p
}

func derefTime(p *time.Time) time.Time {
	if p == nil {
		return time.Time{}
	}
	return *p
}

func (c criteria) matches(r *invlib.FileRecord) bool {
	var site, sourceURL, originalFile string
	var discoveredAt time.Time
	if r.Origin != nil {
		site = derefString(r.Origin.Site)
		sourceURL = derefString(r.Origin.URL)
		originalFile = derefString(r.Origin.OriginalFile)
		discoveredAt = derefTime(r.Origin.DiscoveredAt)
	}
	var width, height int
	if r.Image != nil {
		width = derefInt(r.Image.Width)
		height = derefInt(r.Image.Height)
	}
	hash := derefString(r.File.Hash)
	status := derefString(r.File.Status)
	errMsg := derefString(r.File.Error)

	if c.site != "" && !containsFold(site, c.site) {
		return false
	}
	if c.sourceURL != "" && !containsFold(sourceURL, c.sourceURL) {
		return false
	}
	if c.file != "" && !containsFold(r.File.Path, c.file) {
		return false
	}
	if c.hash != "" && !strings.EqualFold(hash, c.hash) {
		return false
	}
	if c.status != "" && !strings.EqualFold(status, c.status) {
		return false
	}
	if c.errSubstr != "" && !containsFold(errMsg, c.errSubstr) {
		return false
	}
	if c.original != "" && !containsFold(originalFile, c.original) {
		return false
	}
	if c.discoveredAt != "" && discoveredAt.Format(dateLayout) != c.discoveredAt {
		return false
	}
	if c.hasWidth && width != c.width {
		return false
	}
	if c.hasHeight && height != c.height {
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

// underRoot reports whether path is root itself or lives somewhere under
// it, used to scope move's database query to a subtree the same way the
// old manifest-file search was scoped to a directory.
func underRoot(path, root string) bool {
	if path == root {
		return true
	}
	return strings.HasPrefix(path, root+string(filepath.Separator))
}

// Run executes the move command.
func Run(args []string) error {
	fs := flag.NewFlagSet("move", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)

	rootFlag := fs.String("root", "", "scope matches to files under this directory (default: ~/Images)")
	siteFlag := fs.String("site", "", "match files whose site contains value (case-insensitive)")
	sourceURLFlag := fs.String("sourceurl", "", "match files whose source_url contains value (case-insensitive)")
	fileFlag := fs.String("file", "", "match files whose path contains value (case-insensitive)")
	hashFlag := fs.String("hash", "", "match files with exactly this hash")
	statusFlag := fs.String("status", "", "match files with exactly this status")
	errorFlag := fs.String("error", "", "match files whose error contains value (case-insensitive)")
	originalFlag := fs.String("original", "", "match files whose original_file contains value (case-insensitive)")
	discoveredAtFlag := fs.String("discoveredat", "", "match files discovered on this date (YYYY-MM-DD)")
	widthFlag := fs.Int("width", 0, "match files with exactly this width")
	heightFlag := fs.Int("height", 0, "match files with exactly this height")
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

	dbPath, err := invlib.DefaultPath()
	if err != nil {
		return fmt.Errorf("move: resolve inv.db path: %w", err)
	}
	ctx := context.Background()
	inv, err := invlib.Open(ctx, dbPath)
	if err != nil {
		return fmt.Errorf("move: open inv.db: %w", err)
	}
	defer inv.Close()

	if removed, err := invdb.PruneMissing(ctx, inv); err != nil {
		return fmt.Errorf("move: pruning missing files: %w", err)
	} else if removed > 0 {
		fmt.Printf("Removed %d database record(s) whose file no longer exists\n", removed)
	}

	matched, err := loadAndFilter(ctx, inv, root, c)
	if err != nil {
		return fmt.Errorf("move: %w", err)
	}
	if len(matched) == 0 {
		fmt.Println("No files matched.")
		return nil
	}

	if *previewFlag {
		return previewMove(ctx, inv, matched, root, destDir)
	}

	return moveMatches(ctx, inv, matched, destDir)
}

// loadAndFilter returns every database file record under root that matches
// c.
func loadAndFilter(ctx context.Context, inv *invlib.DB, root string, c criteria) ([]*invlib.FileRecord, error) {
	all, err := inv.ListFileRecords(ctx, invlib.FileQuery{})
	if err != nil {
		return nil, fmt.Errorf("listing files: %w", err)
	}

	var matched []*invlib.FileRecord
	for _, r := range all {
		if !underRoot(r.File.Path, root) {
			continue
		}
		if c.matches(r) {
			matched = append(matched, r)
		}
	}
	return matched, nil
}

// moveMatches moves every matched record into destDir and reports a
// summary.
func moveMatches(ctx context.Context, inv *invlib.DB, matched []*invlib.FileRecord, destDir string) error {
	fmt.Printf("Matched %d file(s); moving to %s\n", len(matched), destDir)

	moved, duplicates, failureMsgs, _, errs := performMoves(ctx, inv, matched, destDir)

	fmt.Printf("\nMoved %d; duplicates %d; failed %d (of %d matched)\n", moved, duplicates, len(failureMsgs), len(matched))

	if len(failureMsgs) > 0 {
		errs = append(errs, fmt.Errorf("%d file(s) failed to move", len(failureMsgs)))
	}
	if len(errs) > 0 {
		return fmt.Errorf("move: %w", errors.Join(errs...))
	}
	return nil
}

// previewPaths returns the current path of every matched record, sorted.
func previewPaths(matched []*invlib.FileRecord) []string {
	paths := make([]string, 0, len(matched))
	for _, r := range matched {
		paths = append(paths, r.File.Path)
	}
	sort.Strings(paths)
	return paths
}

// previewMove opens the viewer on every matched record's file so the user
// can look the batch over before committing to the move, with the option
// to redirect to a different destination (see viewer.Browser.SetMoveAll's
// Change Destination control) or quit without moving anything.
//
// The Move All button's actual work is performMoves over the full,
// original matched slice every time it's confirmed, rather than just the
// paths viewer still has on screen: an entry that already moved still has
// its in-memory Path rewritten to its new, already-in-destDir path (see
// performMoves), so re-running performMoves against it resolves as a
// harmless duplicate-of-itself no-op. That makes a retry (after a partial
// failure) or a destination change mid-session both safe to implement as
// "just run it again".
func previewMove(ctx context.Context, inv *invlib.DB, matched []*invlib.FileRecord, root, destDir string) error {
	paths := previewPaths(matched)

	// This mover already has its own inv-lib connection via closure (inv,
	// captured from Run), so it ignores the db parameter RunImagesWithMover
	// always passes — no second connection is opened here.
	mover := func(_ context.Context, _ *invlib.DB, _ []string, dest string) viewer.MoveAllResult {
		moved, duplicates, failureMsgs, movedPaths, errs := performMoves(ctx, inv, matched, dest)
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

	return viewer.RunImagesWithMover(root, "", paths, false, destDir, inv, mover)
}

// performMoves moves every matched record's file into destDir and updates
// its database row's path in place. On success, matched[i].File.Path is
// rewritten to the new location — see previewMove's doc comment for why
// that matters for a retried or redirected Move All.
func performMoves(ctx context.Context, inv *invlib.DB, matched []*invlib.FileRecord, destDir string) (moved, duplicates int, failureMsgs, movedPaths []string, errs []error) {
	if err := os.MkdirAll(destDir, 0o755); err != nil {
		errs = append(errs, fmt.Errorf("creating dest %s: %w", destDir, err))
		return moved, duplicates, failureMsgs, movedPaths, errs
	}

	for i := range matched {
		src := matched[i].File.Path

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

		if err := inv.UpdateFilePath(ctx, matched[i].File.ID, destPath, filepath.Base(destPath)); err != nil {
			errs = append(errs, fmt.Errorf("updating database for %s: %w", destPath, err))
		}

		fmt.Printf("  %s -> %s\n", src, destPath)
		matched[i].File.Path = destPath
		movedPaths = append(movedPaths, src)
		moved++
	}

	return moved, duplicates, failureMsgs, movedPaths, errs
}
