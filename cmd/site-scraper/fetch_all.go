package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
)

const batchSize = 500

type PicResult struct {
	GURL     string `json:"g_url"`
	TURL     string `json:"t_url"`
	H        int    `json:"h"`
	Desc     string `json:"desc"`
	TURL460  string `json:"t_url_460"`
	GID      string `json:"gid"`
	MID      string `json:"mid"`
	TID      string `json:"tid"`
	NoFollow bool   `json:"nofollow"`
	OutLink  bool   `json:"outLink"`
}

func fetchPics(offset int, apiURL, query string) ([]PicResult, error) {
	endpoint := picsURL(offset, batchSize, apiURL, query)

	resp, err := http.Get(endpoint)
	if err != nil {
		return nil, fmt.Errorf("fetching pics: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected HTTP status: %s", resp.Status)
	}

	var results []PicResult
	if err := json.NewDecoder(resp.Body).Decode(&results); err != nil {
		return nil, fmt.Errorf("decoding JSON: %w", err)
	}

	return results, nil
}

func picsURL(offset, limit int, apiURL, query string) string {
	q := url.Values{}
	q.Set("offset", strconv.Itoa(offset))
	q.Set("limit", strconv.Itoa(limit))
	q.Set("lang", "en")
	q.Set("q", query)

	return apiURL + "?" + q.Encode()
}

func fetchAllPics(apiURL, query string) ([]PicResult, error) {
	seen := make(map[string]struct{})
	var deduped []PicResult

	for offset := 0; ; offset += batchSize {
		results, err := fetchPics(offset, apiURL, query)
		if err != nil {
			return nil, err
		}

		if len(results) == 0 {
			break
		}

		for _, pic := range results {
			if pic.GURL == "" {
				continue
			}

			if _, exists := seen[pic.GURL]; exists {
				continue
			}

			seen[pic.GURL] = struct{}{}
			deduped = append(deduped, pic)
		}

		if len(results) < batchSize {
			break
		}
	}

	return deduped, nil
}
