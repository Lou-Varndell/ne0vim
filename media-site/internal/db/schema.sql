PRAGMA foreign_keys = ON;

BEGIN;

-- ============================================================
-- Files
--
-- One row represents one physical file in the library.
-- This is the central table of the database.
-- ============================================================

CREATE TABLE IF NOT EXISTS files (
    id               INTEGER PRIMARY KEY,

    path             TEXT NOT NULL UNIQUE,
    filename         TEXT NOT NULL,

    extension        TEXT,
    mime_type        TEXT,

    filesize         INTEGER NOT NULL,
    hash             TEXT,              -- content fingerprint; not unique, see guide/db design notes

    origin_id        INTEGER REFERENCES origins(id),

    created_at       DATETIME,
    modified_at      DATETIME,
    scanned_at       DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    last_verified_at DATETIME,
    missing_since    DATETIME,

    -- Set only by a local processing step (e.g. image-browser's `process`
    -- command recording a decode outcome) — never by import/ingest, which
    -- records provenance on origins instead. Nullable with no default: a
    -- plain scan or ingest never touches either column.
    status           TEXT,
    error            TEXT
);


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
-- Unpopulated until a separate ffprobe effort.
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


-- ============================================================
-- Audio
--
-- One-to-one extension of files. Unpopulated until a separate
-- ffprobe effort.
-- ============================================================

CREATE TABLE IF NOT EXISTS audio (
    file_id     INTEGER PRIMARY KEY,

    duration_ms INTEGER,
    codec       TEXT,
    sample_rate INTEGER,
    channels    INTEGER,
    bitrate     INTEGER,

    FOREIGN KEY (file_id)
        REFERENCES files(id)
        ON DELETE CASCADE
);


-- ============================================================
-- Media probe
--
-- Raw ffprobe output, if ever captured. Unpopulated until a
-- separate ffprobe effort.
-- ============================================================

CREATE TABLE IF NOT EXISTS media_probe (
    file_id      INTEGER PRIMARY KEY,
    ffprobe_json TEXT NOT NULL,
    probed_at    TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,

    FOREIGN KEY (file_id)
        REFERENCES files(id)
        ON DELETE CASCADE
);


-- ============================================================
-- Origins
--
-- Where a file came from. A file may have no known origin
-- (origin_id NULL) — attached only via manifest/site-scraper
-- ingestion, never by a filesystem-only sync.
-- ============================================================

CREATE TABLE IF NOT EXISTS origins (
    id            INTEGER PRIMARY KEY,
    type          TEXT NOT NULL,
    site          TEXT,
    url           TEXT,
    identifier    TEXT,
    metadata      TEXT,

    -- original_file and discovered_at are manifest.json provenance (the
    -- pre-collision-resolution filename a scraper run would have given
    -- this file, and when that run discovered it) — the same two facts a
    -- file's physical path and files.scanned_at already capture, just
    -- from the scrape's point of view instead of the filesystem's.
    original_file TEXT,
    discovered_at DATETIME,

    UNIQUE(site, identifier)
);


-- ============================================================
-- Tags / file_tags
--
-- Plain many-to-many junction. Tag names are created on first
-- use; removing the last file reference to a tag does not
-- delete the tag itself.
-- ============================================================

CREATE TABLE IF NOT EXISTS tags (
    id   INTEGER PRIMARY KEY,
    name TEXT NOT NULL UNIQUE
);

CREATE TABLE IF NOT EXISTS file_tags (
    file_id INTEGER NOT NULL,
    tag_id  INTEGER NOT NULL,

    PRIMARY KEY (file_id, tag_id),

    FOREIGN KEY (file_id) REFERENCES files(id) ON DELETE CASCADE,
    FOREIGN KEY (tag_id)  REFERENCES tags(id)  ON DELETE CASCADE
);

COMMIT;
