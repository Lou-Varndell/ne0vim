// Package invdb holds the small amount of database-adjacent logic that
// doesn't belong in inv-lib itself because it mixes filesystem I/O with
// database calls — inv-lib has no business statting paths on disk.
package invdb

import (
	"context"
	"errors"
	"fmt"
	"os"

	invlib "inv-lib"
)

// PruneMissing deletes every files row whose path no longer exists on disk
// (images/videos rows cascade via their foreign key), reporting how many
// were removed. move and size both call this at startup to clear out
// records left behind by a file that was deleted, renamed, or moved
// outside this program's own tooling.
func PruneMissing(ctx context.Context, db *invlib.DB) (int, error) {
	files, err := db.ListFiles(ctx)
	if err != nil {
		return 0, fmt.Errorf("invdb: list files for prune: %w", err)
	}

	var removed int
	for _, f := range files {
		if _, err := os.Stat(f.Path); errors.Is(err, os.ErrNotExist) {
			if err := db.DeleteFile(ctx, f.ID); err != nil {
				return removed, fmt.Errorf("invdb: delete missing file %s: %w", f.Path, err)
			}
			removed++
		}
	}

	return removed, nil
}
