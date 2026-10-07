PRAGMA foreign_keys = ON;

BEGIN;

-- ============================================================
-- Files
--
-- One row represents one physical file in the library.
-- This is the central table of the database.
-- ============================================================

CREATE TABLE IF NOT EXISTS files (
    id            INTEGER PRIMARY KEY,

    path          TEXT NOT NULL UNIQUE,
    filename      TEXT NOT NULL,

    extension     TEXT,
    mime_type     TEXT,

    filesize      INTEGER NOT NULL,
    blake3        TEXT,

    created_at    DATETIME,
    modified_at   DATETIME,
    scanned_at    DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,

    -- Provenance columns, populated by `import` from a scraper's
    -- manifest.json (see internal/ingest) and by `process`'s status/error
    -- tracking. Nullable: a plain `scan` never sets any of these.
    site          TEXT,
    source_url    TEXT,
    original_file TEXT,
    discovered_at DATETIME,
    status        TEXT,
    error         TEXT
);

CREATE INDEX IF NOT EXISTS idx_files_blake3
    ON files(blake3);

CREATE INDEX IF NOT EXISTS idx_files_mime_type
    ON files(mime_type);


-- ============================================================
-- Images
--
-- One-to-one extension of files.
-- Only files that are recognized as images need a row here.
-- ============================================================

CREATE TABLE IF NOT EXISTS images (
    file_id       INTEGER PRIMARY KEY,

    width         INTEGER,
    height        INTEGER,

    format        TEXT,
    color_space   TEXT,

    FOREIGN KEY (file_id)
        REFERENCES files(id)
        ON DELETE CASCADE
);


-- ============================================================
-- Videos
--
-- One-to-one extension of files.
-- Only files that are recognized as videos need a row here.
-- ============================================================

CREATE TABLE IF NOT EXISTS videos (
    file_id          INTEGER PRIMARY KEY,

    duration_ms      INTEGER,

    width            INTEGER,
    height           INTEGER,

    video_codec      TEXT,
    video_profile    TEXT,
    video_level      TEXT,
    pix_fmt          TEXT,
    fps              REAL,
    video_bitrate    INTEGER,

    audio_codec      TEXT,
    audio_channels   INTEGER,
    audio_sample_rate INTEGER,
    audio_bitrate    INTEGER,

    FOREIGN KEY (file_id)
        REFERENCES files(id)
        ON DELETE CASCADE
);


COMMIT;
