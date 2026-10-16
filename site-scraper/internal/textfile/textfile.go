// Package textfile reads newline-delimited, whitespace-trimmed lines,
// skipping blanks — the shape shared by URL list files, the system
// clipboard, and the on-disk persisted link set.
package textfile

import (
	"bufio"
	"io"
	"os"
	"strings"
)

// Lines reads r and returns each non-blank line with surrounding
// whitespace trimmed.
func Lines(r io.Reader) ([]string, error) {
	var lines []string

	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		lines = append(lines, line)
	}

	return lines, scanner.Err()
}

// ReadLines opens path and returns its lines via Lines. If path doesn't
// exist, the returned error satisfies os.IsNotExist so callers can treat a
// missing file as "no lines yet" when that's meaningful.
func ReadLines(path string) ([]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	return Lines(f)
}
