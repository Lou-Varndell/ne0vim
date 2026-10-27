package viewer

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync"

	invlib "inv-lib"
)

// isAnimatedWebP reports whether path is an animated WebP. Neither the
// image decoders registered in viewer.go nor the thumbnail cache can handle
// one (golang.org/x/image/webp rejects it outright), so callers route it to
// an external player instead. Any error reading the file counts as "not
// animated": the normal decode path will surface the real problem.
func isAnimatedWebP(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()

	animated, err := invlib.IsAnimatedWebP(f)
	return err == nil && animated
}

// animatedPlayer runs at most one ffplay process at a time, so paging
// through a folder of animated images doesn't leave a trail of windows.
type animatedPlayer struct {
	mu  sync.Mutex
	cmd *exec.Cmd
}

// play stops any current playback and starts ffplay on path, looping until
// its window is closed or stop is called. A failure to start is returned;
// a later non-zero exit of ffplay itself is reported on stderr.
func (p *animatedPlayer) play(path string) error {
	p.stop()

	bin, err := exec.LookPath("ffplay")
	if err != nil {
		return fmt.Errorf("ffplay not found in PATH: %w", err)
	}

	cmd := exec.Command(bin, "-loglevel", "error", "-loop", "0", "-window_title", filepath.Base(path), path)
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start ffplay: %w", err)
	}

	p.mu.Lock()
	p.cmd = cmd
	p.mu.Unlock()

	go func() {
		waitErr := cmd.Wait()

		// stop clears p.cmd before killing, so a kill we asked for is not
		// reported as a failure.
		p.mu.Lock()
		current := p.cmd == cmd
		if current {
			p.cmd = nil
		}
		p.mu.Unlock()

		if waitErr != nil && current {
			fmt.Fprintf(os.Stderr, "image-browser: ffplay %s: %v\n", path, waitErr)
		}
	}()
	return nil
}

// stop kills the current ffplay process, if any.
func (p *animatedPlayer) stop() {
	p.mu.Lock()
	cmd := p.cmd
	p.cmd = nil
	p.mu.Unlock()

	if cmd != nil {
		_ = cmd.Process.Kill()
	}
}
