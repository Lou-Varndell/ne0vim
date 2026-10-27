-- Indexes are applied after migrate.go has brought an existing files table
-- up to the current column set (see migrate.go) — several of these
-- reference columns (hash, origin_id, status) that an older database won't
-- have until that migration step runs. Keeping them in a separate file
-- from schema.sql lets db.Open sequence table creation, column migration,
-- and index creation correctly regardless of whether it's opening a brand
-- new database or an existing one.

CREATE INDEX IF NOT EXISTS idx_files_hash      ON files(hash);
CREATE INDEX IF NOT EXISTS idx_files_mime_type ON files(mime_type);
CREATE INDEX IF NOT EXISTS idx_files_origin_id ON files(origin_id);
CREATE INDEX IF NOT EXISTS idx_files_status    ON files(status);

-- Expression index backing the sorted path range-scan sync's merge-join
-- relies on (see sync.QueryPathRange / store.FilesInPathRange). Ordering by
-- raw `path` would sort '/' (0x2F) above characters like '-' or '.' (0x2D,
-- 0x2E), which disagrees with filepath.WalkDir's traversal order whenever a
-- directory name is a prefix of a sibling's name plus one of those
-- characters (e.g. sibling directories "2024" and "2024-edited"). Replacing
-- '/' with char(1) — lower than any byte a filename can contain — makes the
-- SQL ordering agree with WalkDir's own depth-first, lexically-sorted-
-- siblings order in every case, not just the common one.
CREATE INDEX IF NOT EXISTS idx_files_path_sortkey ON files(replace(path, '/', char(1)));
