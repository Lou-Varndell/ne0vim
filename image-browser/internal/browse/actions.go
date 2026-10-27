package browse

import (
	"context"
	"errors"

	tea "charm.land/bubbletea/v2"

	invlib "inv-lib"

	"image-browser/internal/move"
)

// recordsLoadedMsg reports the result of loadRecordsCmd.
type recordsLoadedMsg struct {
	records []*invlib.FileRecord
	err     error
}

// loadRecordsCmd queries every database record whose path falls under
// root, for display in the browse list. RequireImage/ExcludeMissing match
// the same structural filters inventory-manager's own browser always
// applies (see FileQuery's doc comments): the point of this screen is
// browsing images that are actually still on disk, not every files row.
func loadRecordsCmd(ctx context.Context, inv *invlib.DB, root string) tea.Cmd {
	return func() tea.Msg {
		records, err := inv.ListFileRecords(ctx, invlib.FileQuery{
			PathPrefix:     root,
			RequireImage:   true,
			ExcludeMissing: true,
			WithTags:       true,
		})
		return recordsLoadedMsg{records: records, err: err}
	}
}

// moveDoneMsg reports the result of performMoveCmd.
type moveDoneMsg struct {
	moved, duplicates int
	failures          []string
	err               error
}

// performMoveCmd relocates every selected record's file into destDir,
// reusing move.PerformMoves — the same resolve-destination/move/update-db
// logic the `move` subcommand's own -preview flow drives — rather than
// reimplementing it here.
func performMoveCmd(ctx context.Context, inv *invlib.DB, selected []*invlib.FileRecord, destDir string) tea.Cmd {
	return func() tea.Msg {
		moved, duplicates, failureMsgs, _, errs := move.PerformMoves(ctx, inv, selected, destDir)

		var err error
		if len(errs) > 0 {
			err = errors.Join(errs...)
		}

		return moveDoneMsg{moved: moved, duplicates: duplicates, failures: failureMsgs, err: err}
	}
}

// defaultMoveDest suggests a destination directory name to pre-fill in
// the move prompt, given the records currently selected for a move.
//
// This has no obviously-correct answer the way move.deriveDestName's does:
// that function derives a name from the -site/-tag/etc filter that
// produced the match set, but browse's selection is built by hand, one
// record at a time, with no shared filter value to fall back on. A few
// heuristics are all independently reasonable:
//
//   - Common site: if every selected record shares the same origin site,
//     suggest that — mirrors deriveDestName's behavior for a -site move.
//   - Longest shared directory: suggest the deepest directory every
//     selected file already lives under, nudging toward "group with
//     existing siblings" moves rather than ones that cut across folders.
//   - Leave it blank: force an explicit destination every time, trading
//     convenience for never silently guessing wrong.
//
// Pick whichever fits how you expect to actually use this screen. Return
// "" for "no suggestion" (including whenever selected is empty) — the
// move-destination prompt treats that as "leave the input blank" rather
// than pre-filling anything, so leaving this unimplemented doesn't break
// the move flow, it just never pre-fills.
func defaultMoveDest(selected []*invlib.FileRecord) string {
	return ""
}
