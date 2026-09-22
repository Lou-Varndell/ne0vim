package trash

import (
	"fmt"
	"os/exec"
	"runtime"
	"strings"
)

// Move sends a file to the OS trash/recycle bin. macOS uses the built-in
// `trash` command when available and falls back to Finder via AppleScript.
func Move(path string) error {
	switch runtime.GOOS {
	case "darwin":
		if _, err := exec.LookPath("trash"); err == nil {
			cmd := exec.Command("trash", path)
			if out, err := cmd.CombinedOutput(); err != nil {
				return fmt.Errorf("trash: %w: %s", err, strings.TrimSpace(string(out)))
			}
			return nil
		}
		// Finder's delete operation moves the item to the user's Trash.
		script := fmt.Sprintf("tell application \"Finder\" to delete POSIX file %s", appleScriptString(path))
		cmd := exec.Command("osascript", "-e", script)
		if out, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("Finder trash: %w: %s", err, strings.TrimSpace(string(out)))
		}
		return nil
	case "linux":
		// Keep the dependency-free implementation small. Prefer the desktop
		// `gio trash` command when available.
		cmd := exec.Command("gio", "trash", path)
		if out, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("gio trash: %w: %s", err, strings.TrimSpace(string(out)))
		}
		return nil
	default:
		return fmt.Errorf("sending files to Trash is not implemented on %s", runtime.GOOS)
	}
}

func appleScriptString(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `"`, `\"`)
	return `"` + s + `"`
}
