// Package skip holds the rules for what fddup ignores during a scan.
//
// Three categories are tracked separately so the call sites can apply each
// at the appropriate point in the pipeline:
//   - Dirs:  basenames whose subtrees are pruned during the walk
//   - Names: filenames whose duplicate clusters are reported but not queued
//   - Globs: like Names, but matched as filepath.Match patterns on the basename
package skip

import "path/filepath"

// Dirs are basenames pruned during stage 1 walk. Anything inside one of
// these directories is invisible to fddup.
var Dirs = map[string]bool{
	".git":         true,
	"node_modules": true,
	"venv":         true,
	".venv":        true,
	"target":       true,
}

// Names are filenames where identical content across copies is intentional,
// not waste. A duplicate cluster composed entirely of these is reported
// but never queued for deletion.
//
// Each entry has a real-world story:
//
//	go.sum / go.mod        — per-Go-module pin, deleting one breaks that build
//	package-lock.json      — per-Node-project lockfile
//	Cargo.lock             — per-Rust-project lockfile
//	.gitignore             — semantically per-directory
//	.dockerignore          — semantically per-directory
//	.npmrc                 — per-project npm config
//	.python-version        — per-project pyenv/asdf pin (deleting these is how
//	                         fddup nearly bricked 8 Python projects in testing)
//	.tool-versions         — asdf multi-tool pin
//	CACHEDIR.TAG           — Ruff/pytest cache marker, regenerated automatically
var Names = map[string]bool{
	"go.sum":            true,
	"go.mod":            true,
	".gitignore":        true,
	".dockerignore":     true,
	".npmrc":            true,
	".python-version":   true,
	".tool-versions":    true,
	"package-lock.json": true,
	"Cargo.lock":        true,
	"CACHEDIR.TAG":      true,
}

// Globs are filepath.Match patterns evaluated against the basename. Useful
// for per-environment configs like CloudFormation `vars.dev.yml` /
// `vars.prod.yml` that look like duplicates because they happen to share
// minimal content.
var Globs = []string{"vars.*.yml", "vars.*.yaml"}

// Match reports whether the given path's basename is in Names or matches
// any pattern in Globs.
func Match(path string) bool {
	base := filepath.Base(path)
	if Names[base] {
		return true
	}
	for _, g := range Globs {
		if ok, _ := filepath.Match(g, base); ok {
			return true
		}
	}
	return false
}

// MatchAll reports whether every path in the slice matches.
// Used to decide whether a duplicate cluster is composed entirely of
// known false-positive filenames (in which case it gets logged but not
// queued for deletion).
func MatchAll(paths []string) bool {
	for _, p := range paths {
		if !Match(p) {
			return false
		}
	}
	return true
}
