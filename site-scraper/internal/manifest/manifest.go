package manifest

import "time"

type Entry struct {
	Site         string    `json:"site"`
	SourceURL    string    `json:"source_url"`
	File         string    `json:"file,omitempty"`
	Hash         string    `json:"hash,omitempty"`
	Status       string    `json:"status"`
	Error        string    `json:"error,omitempty"`
	Original     string    `json:"original_file,omitempty"`
	DiscoveredAt time.Time `json:"discovered_at"`

	Width  int `json:"width,omitempty"`
	Height int `json:"height,omitempty"`
}
