package reconcile

import "time"

// hasChanged reports whether a file already in the database still matches
// what reconcile just observed on disk, or whether its content likely
// changed since the last scan — the signal that triggers a re-hash and a
// reset of its images/videos/audio/media_probe rows (see applyPresentFile
// in reconcile.go), so a later `process` run regenerates them from the new
// content instead of silently keeping stale dimensions tied to bytes that
// no longer exist.
//
// dbModTime is nil for a file recorded before modified_at was tracked, or
// one attached only via `import` (which never sets it) — there's nothing
// to compare against, so that case is treated as changed.
//
// Otherwise this errs toward re-hashing: size OR mtime differing counts as
// changed, even though a bare `touch` or a backup/restore can bump mtime
// without the bytes actually changing. For this library — read-mostly,
// occasional edits — a false positive just costs an extra hash and a
// `process` re-run; a missed real edit would leave stale dimensions
// sitting in the database indefinitely. That asymmetry is worth the
// occasional wasted hash.
func hasChanged(dbSize int64, dbModTime *time.Time, fsSize int64, fsModTime time.Time) bool {
	if dbModTime == nil {
		return true
	}
	return dbSize != fsSize || !dbModTime.Equal(fsModTime)
}
