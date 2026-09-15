package manifest

import "time"

// Status is the processing state of a manifest Entry.
type Status string

const (
	// StatusMissing marks an entry whose File no longer exists on disk.
	StatusMissing Status = "missing"
	// StatusNonImage marks an entry whose File extension is not a
	// recognized image type.
	StatusNonImage Status = "non-image"
	// StatusInvalid marks an entry with no File path to process.
	StatusInvalid Status = "invalid"
	// StatusProcessed marks an entry whose image dimensions have been
	// read successfully.
	StatusProcessed Status = "processed"
	// StatusFailed marks an entry whose image could not be decoded.
	StatusFailed Status = "failed"
)

// Entry is one record in an image manifest: a single discovered image and
// its discovery and processing metadata.
type Entry struct {
	Site         string    `json:"site"`
	SourceURL    string    `json:"source_url"`
	File         string    `json:"file,omitempty"`
	Hash         string    `json:"hash,omitempty"`
	Status       Status    `json:"status"`
	Error        string    `json:"error,omitempty"`
	Original     string    `json:"original_file,omitempty"`
	DiscoveredAt time.Time `json:"discovered_at"`

	Width  int `json:"width,omitempty"`
	Height int `json:"height,omitempty"`
}
