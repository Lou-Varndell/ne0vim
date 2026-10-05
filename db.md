# inv.db schema

Authoritative schema reference. Treat this file as the source of truth for
recreating `inv.db` from scratch or writing a migration against it — not as a
design discussion log.

## Current state (to be replaced)

Single table, `entries`, keyed on `(manifest_path, source_url)`. Mixes file
identity, media properties, and source/origin fields in one row.

```sql
CREATE TABLE entries (
    manifest_path TEXT NOT NULL,
    source_url    TEXT NOT NULL,
    site          TEXT,
    file          TEXT,
    hash          TEXT,
    status        TEXT,
    error         TEXT,
    original_file TEXT,
    discovered_at DATETIME,
    width         INTEGER,
    height        INTEGER,
    imported_at   DATETIME NOT NULL,
    PRIMARY KEY (manifest_path, source_url)
);
```

## Target schema

```
origins ─┐
         │
       files ──┬── images
         │      ├── videos
         │      └── audio
         │
       tags ── file_tags
```

- `files` is the authoritative record of the physical object on disk.
- `images` / `videos` / `audio` hold type-specific media properties,
  1:1 with `files` via `file_id` as primary key (not a separate `media`
  table — type is implicit in which child table has a row).
- `origins` holds where a file came from; `files.origin_id` is nullable
  (a file may have no known origin). An origin's raw payload (e.g. a
  manifest.json) lives in `origins.metadata` as JSON — do not add
  per-field columns for provenance data you don't query on directly.
- `tags` / `file_tags` is a plain many-to-many junction.
- Raw `ffprobe` output, if ever captured, goes in `media_probe` — never
  inline a JSON blob into `images`/`videos`/`audio`. Columns on those
  tables are only values actually queried (`width`, `video_codec`, etc.).

### DDL

```sql
PRAGMA foreign_keys = ON;

CREATE TABLE files (
    id          INTEGER PRIMARY KEY,

    path        TEXT NOT NULL UNIQUE,
    filename    TEXT NOT NULL,
    extension   TEXT,
    mime_type   TEXT,

    filesize    INTEGER NOT NULL,
    hash        TEXT UNIQUE,            -- content fingerprint (sha256/blake3), not identity

    origin_id   INTEGER REFERENCES origins(id),

    created_at  DATETIME,
    modified_at DATETIME,
    scanned_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,

    status      TEXT,                   -- e.g. 'processed', 'error'
    error       TEXT
);

CREATE INDEX idx_files_hash      ON files(hash);
CREATE INDEX idx_files_mime_type ON files(mime_type);
CREATE INDEX idx_files_origin_id ON files(origin_id);

CREATE TABLE images (
    file_id     INTEGER PRIMARY KEY,

    width       INTEGER,
    height      INTEGER,
    format      TEXT,
    color_space TEXT,

    FOREIGN KEY (file_id) REFERENCES files(id) ON DELETE CASCADE
);

CREATE TABLE videos (
    file_id           INTEGER PRIMARY KEY,

    duration_ms       INTEGER,
    width             INTEGER,
    height            INTEGER,

    video_codec       TEXT,
    video_profile     TEXT,
    video_level       TEXT,
    pix_fmt           TEXT,
    fps               REAL,
    video_bitrate     INTEGER,

    audio_codec       TEXT,
    audio_channels    INTEGER,
    audio_sample_rate INTEGER,
    audio_bitrate     INTEGER,

    FOREIGN KEY (file_id) REFERENCES files(id) ON DELETE CASCADE
);

CREATE TABLE audio (
    file_id     INTEGER PRIMARY KEY,

    duration_ms INTEGER,
    codec       TEXT,
    sample_rate INTEGER,
    channels    INTEGER,
    bitrate     INTEGER,

    FOREIGN KEY (file_id) REFERENCES files(id) ON DELETE CASCADE
);

CREATE TABLE media_probe (
    file_id      INTEGER PRIMARY KEY,
    ffprobe_json TEXT NOT NULL,
    probed_at    TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,

    FOREIGN KEY (file_id) REFERENCES files(id) ON DELETE CASCADE
);

CREATE TABLE origins (
    id         INTEGER PRIMARY KEY,
    type       TEXT NOT NULL,           -- e.g. 'scrape', 'manual_import'
    site       TEXT,
    url        TEXT,
    identifier TEXT,                    -- site-local id, if any
    metadata   TEXT,                    -- raw JSON (e.g. manifest.json contents)

    UNIQUE(site, identifier)
);

CREATE TABLE tags (
    id   INTEGER PRIMARY KEY,
    name TEXT NOT NULL UNIQUE
);

CREATE TABLE file_tags (
    file_id INTEGER NOT NULL,
    tag_id  INTEGER NOT NULL,

    PRIMARY KEY (file_id, tag_id),

    FOREIGN KEY (file_id) REFERENCES files(id) ON DELETE CASCADE,
    FOREIGN KEY (tag_id)  REFERENCES tags(id)  ON DELETE CASCADE
);
```

### Design rules

- `files.id` is database identity; `files.path` is filesystem location;
  `files.hash` is a content fingerprint — never conflate the three.
  Duplicate detection is a query, not a constraint:
  ```sql
  SELECT hash, COUNT(*) FROM files
  WHERE hash IS NOT NULL
  GROUP BY hash
  HAVING COUNT(*) > 1;
  ```
- A file has at most one of {`images`, `videos`, `audio`} row. Enforced
  by convention (the importer writes to exactly one), not by a CHECK —
  SQLite can't easily express "exactly one of three child tables" without
  triggers, and it isn't worth it here.
- `origins.metadata` and `media_probe.ffprobe_json` are deliberately
  unstructured. Promote a field out of JSON into a real column only once
  you're actually filtering/sorting on it in SQL.

## Migration from `entries`

```sql
BEGIN;

INSERT INTO origins (type, site, url, identifier, metadata)
SELECT DISTINCT
    'scrape',
    site,
    source_url,
    source_url,
    json_object('manifest_path', manifest_path)
FROM entries
WHERE site IS NOT NULL;

INSERT INTO files (path, filename, filesize, hash, origin_id,
                    created_at, scanned_at, status, error)
SELECT
    e.file,
    substr(e.file, instr_last(e.file, '/') + 1),  -- or compute in app code
    0,                                              -- backfill via stat() pass
    e.hash,
    o.id,
    e.discovered_at,
    e.imported_at,
    e.status,
    e.error
FROM entries e
LEFT JOIN origins o ON o.url = e.source_url;

INSERT INTO images (file_id, width, height)
SELECT f.id, e.width, e.height
FROM entries e
JOIN files f ON f.path = e.file
WHERE e.width IS NOT NULL;

COMMIT;
```

Notes:
- SQLite has no `instr_last` — compute `filename` and `filesize` in the
  migration script (app code), not in SQL, since they require `stat()`
  on each path or substring-from-the-right logic. The INSERT above is a
  shape reference, not a copy-paste script.
- `original_file` from `entries` is unused in the target schema — decide
  whether it's dead data or needs a home (likely `origins.metadata`)
  before dropping it.
- Run the migration into a fresh `inv_v2.db`, verify row counts and spot
  checks against `entries`, then swap files — don't migrate in place.
