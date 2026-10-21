package images

import (
	"fmt"
	"os/exec"
)

func PlayAnimation(path string) error {
	ffplay, err := exec.LookPath("ffplay")
	if err != nil {
		return fmt.Errorf("ffplay not found; install FFmpeg or check PATH: %w", err)
	}

	cmd := exec.Command(ffplay,
		"-loglevel", "error",
		"-fs",
		"-loop", "0",
		path,
	)

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start ffplay for %q: %w", path, err)
	}

	// Reap the process asynchronously so the Fyne UI stays responsive.
	go func() {
		_ = cmd.Wait()
	}()

	return nil
}
