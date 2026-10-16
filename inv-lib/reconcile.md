write a filesystem reconciliation operation: compare the paths recorded in your SQLite database with the files that actually exist on disk, then update the database without losing useful history or metadata.

With the schema, I'd make reconciliation a separate image-browser subcommand rather than mixing it into the normal scanning process. That gives you control over what happens when files are moved, deleted, renamed, or modified.

## 1. Define what reconciliation should detect

I'd start with these cases:

| Situation                                             | Database action                                                                     |
| ----------------------------------------------------- | ----------------------------------------------------------------------------------- |
| File exists and matches its recorded path             | Update `last_verified_at`; clear `missing_since`                                    |
| File is no longer present                             | Set `missing_since` and mark its status as missing                                  |
| Previously missing file reappears at the same path    | Clear `missing_since`; restore its status                                           |
| File exists but its size or modification time changed | Refresh filesystem metadata and mark it for reprocessing if needed                  |
| New file exists on disk but has no database record    | Insert it into `files`                                                              |
| File has moved to another path                        | Treat the old path as missing and the new path as a new file initially              |
| File's media metadata needs refreshing                | Reprocess its `images`, `videos`, `audio`, and `media_probe` records as appropriate |

One important distinction: a path is the identity your `files` table currently enforces. A file moved to a different path is not automatically the same database record, even if its content hash matches.

## 2. Use a two-phase reconciliation process

I'd separate discovering what exists from applying database changes.

Phase A — Scan the filesystem

Walk the configured root directory recursively. Collect each file's normalized path, filename, extension, size, modification time, and any other inexpensive filesystem attributes. Compare that inventory against `files`.

Phase B — Reconcile the database

Produce a report of new, unchanged, changed, and missing files. Apply updates in a transaction only after the scan completes successfully.

I'd make the first run a report-only operation, with an explicit flag such as `-apply` required to make changes.

## 3. Use the columns you already have

Your schema already supports most of this without a migration.

- `scanned_at`: when the database last scanned or refreshed the record.
- `last_verified_at`: when the file was last confirmed to exist.
- `missing_since`: when the file was first found missing.
- `status`: the current lifecycle state, such as `present`, `missing`, or `error`.
- `error`: the reason an operation failed, if applicable.
- `hash`: useful for identifying identical content after a move, but only when you choose to calculate or verify hashes.

For a missing file, I'd preserve the `files` row and all associated metadata. The `ON DELETE CASCADE` relationships on `images`, `videos`, `audio`, and `media_probe` mean deleting a file record would destroy valuable information unnecessarily.

Also, don't overwrite `origins` or `file_tags` just because a file is missing. Those relationships can remain useful if the file returns.

## 4. Protect against false missing-file reports

This is the most important operational detail.

If the scan fails halfway through, a mounted volume is unavailable, or a directory can't be read, the reconciler must not conclude that every file it failed to inspect is missing.

I'd recommend:

- Treat inaccessible directories as scan errors, not empty directories.
- Record the scan's start time, completion time, and errors.
- Only mark files missing after a successful scan of the relevant directory.
- Support a grace period before treating a file as definitively missing.
- Never physically delete files as part of reconciliation.

For large collections, process files concurrently in batches and/or routines and avoid hashing every file on every run.

## 5. Suggested command interface

For example, if you were building this into your existing image-browser tool (already handles some media database maintenance):

```
# Inspect differences without changing the database
image-browser reconcile -root /Users/boomer/Images

# Apply the reconciliation
image-browser reconcile -root /Users/boomer/Images -apply

# Reconcile a specific subtree
image-browser reconcile -root /Users/boomer/Images/20260826-100931 -apply

# Recheck missing records
image-browser reconcile -root /Users/boomer/Images -include-missing
```

These are proposed command names, not existing commands.

The report could summarize:

```
Reconciliation summary
----------------------
Files examined:       12,450
Unchanged:            11,980
Changed:                  35
New files:               400
Missing files:            35
Scan errors:               0

Changes applied: no
```

The key design choice is that a scan limited to one subtree should only reconcile records belonging to that subtree. It shouldn't mark files elsewhere in the database as missing.

also reconcile manifest.json files that exist in the scanned subtree. manifest.json is an artifact that is being phased out in favor of the database so it's conents should only ever match what is in the database or filesystem upon reconciliation. this file is still generated as an artifact of the scraper.

 `files.path` contain absolute paths

This decides when reconcile re-hashes a file and wipes its images/videos/audio/media_probe rows for reprocessing. The trade-off is real: size != OR mtime != catches every genuine edit but also fires on a bare touch/backup-restore (wasted re-hash); size != AND mtime != skips that false positive but misses a same-size in-place edit. Both are defensible — pick the failure mode you'd rather live with, or something else entirely (e.g. truncate mtime to a coarser granularity if your filesystem is noisy). It compiles and builds fine as-is; it'll just panic the moment reconcile hits its first already-known file, which is intentional — the report numbers (Changed vs Unchanged) are meaningless until this is filled in, even in dry-run mode.
  - i don't expect to see much change in the files other than viewing so mtime is prolly not a huge factor.
  - i would rather perform some extra hashing should it be required.

One scope note I deliberately left out rather than build speculatively: concurrent hashing for new/changed files. Your spec mentioned it for "large collections," but new+changed files are typically a small fraction of what's examined (400+35 out of 12,450 in your own example), so sequential hashing of that subset is unlikely to be the bottleneck — the full-table ListFileRecords fetch and the walk itself will dominate first. Worth revisiting with real numbers if a run turns out slow.
  - the overall count will only be a subset of the whole. so at most there should only be 100k to 1m. even a full rebuild/scan of the db can be ran without any real detriment.
