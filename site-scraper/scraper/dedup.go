package scraper

import (
	"encoding/hex"
	"fmt"
	"io"
	"os"

	"lukechampine.com/blake3"
)

// hashedFile is a survivor of dedup, carrying the blake3 hash computed to
// confirm it's not a duplicate (or to record what a later duplicate
// matched against).
type hashedFile struct {
	downloadedFile
	hash string
}

// dupInfo is a file dropped by dedup, and the hash of the survivor it
// duplicates.
type dupInfo struct {
	file   downloadedFile
	ofHash string
}

// dedupe applies the progressive filename -> size -> blake3-hash
// comparison: only files sharing a logical name are ever compared, and
// within a name+size group, equal hashes are true duplicates (first seen
// wins). Two files with different names but identical content are NOT
// deduplicated by design — see prompt.md's "Explicit limitation" note.
func dedupe(files []downloadedFile) ([]hashedFile, []dupInfo, error) {
	var survivors []hashedFile
	var dupes []dupInfo

	for _, nameGroup := range groupByName(files) {
		for _, sizeGroup := range groupBySize(nameGroup) {
			s, d, err := confirmByHash(sizeGroup)
			if err != nil {
				return nil, nil, err
			}
			survivors = append(survivors, s...)
			dupes = append(dupes, d...)
		}
	}

	return survivors, dupes, nil
}

func groupByName(files []downloadedFile) map[string][]downloadedFile {
	groups := make(map[string][]downloadedFile)
	for _, f := range files {
		groups[f.name] = append(groups[f.name], f)
	}
	return groups
}

func groupBySize(files []downloadedFile) map[int64][]downloadedFile {
	groups := make(map[int64][]downloadedFile)
	for _, f := range files {
		groups[f.size] = append(groups[f.size], f)
	}
	return groups
}

// confirmByHash hashes every file in a name+size group and splits it into
// one survivor per distinct hash plus the rest as duplicates of it.
func confirmByHash(group []downloadedFile) ([]hashedFile, []dupInfo, error) {
	byHash := make(map[string][]downloadedFile)
	var order []string

	for _, f := range group {
		h, err := hashFile(f.path)
		if err != nil {
			return nil, nil, fmt.Errorf("hash %s: %w", f.path, err)
		}
		if _, ok := byHash[h]; !ok {
			order = append(order, h)
		}
		byHash[h] = append(byHash[h], f)
	}

	var survivors []hashedFile
	var dupes []dupInfo

	for _, h := range order {
		hashGroup := byHash[h]
		survivors = append(survivors, hashedFile{downloadedFile: hashGroup[0], hash: h})
		for _, dup := range hashGroup[1:] {
			dupes = append(dupes, dupInfo{file: dup, ofHash: h})
		}
	}

	return survivors, dupes, nil
}

func hashFile(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()

	h := blake3.New(32, nil)
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}

	return hex.EncodeToString(h.Sum(nil)), nil
}
