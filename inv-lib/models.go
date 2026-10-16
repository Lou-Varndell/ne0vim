package invlib

import "time"

// Origin is a row in the origins table: where a file came from, if known.
type Origin struct {
	ID           int64
	Type         string
	Site         *string
	URL          *string
	Identifier   *string
	Metadata     *string
	OriginalFile *string
	DiscoveredAt *time.Time
}

// File is a row in the files table, the central table of the library.
type File struct {
	ID             int64
	Path           string
	Filename       string
	Extension      *string
	MimeType       *string
	Filesize       int64
	Hash           *string
	OriginID       *int64
	CreatedAt      *time.Time
	ModifiedAt     *time.Time
	ScannedAt      time.Time
	LastVerifiedAt *time.Time
	MissingSince   *time.Time
	Status         *string
	Error          *string
}

// Image is the one-to-one image extension of a File.
type Image struct {
	FileID     int64
	Width      *int
	Height     *int
	Format     *string
	ColorSpace *string
}

// Video is the one-to-one video extension of a File.
type Video struct {
	FileID          int64
	DurationMs      *int64
	Width           *int
	Height          *int
	VideoCodec      *string
	VideoProfile    *string
	VideoLevel      *string
	PixFmt          *string
	FPS             *float64
	VideoBitrate    *int64
	AudioCodec      *string
	AudioChannels   *int
	AudioSampleRate *int
	AudioBitrate    *int64
}

// Audio is the one-to-one audio extension of a File.
type Audio struct {
	FileID     int64
	DurationMs *int64
	Codec      *string
	SampleRate *int
	Channels   *int
	Bitrate    *int64
}

// MediaProbe holds raw ffprobe output captured for a File.
type MediaProbe struct {
	FileID      int64
	FFprobeJSON string
	ProbedAt    time.Time
}

// Tag is a row in the tags table.
type Tag struct {
	ID   int64
	Name string
}
