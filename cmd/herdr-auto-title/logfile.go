package main

import (
	"io"
	"os"
	"path/filepath"
	"sync"
)

// logName is the file a detached instance logs to, in its plugin state
// directory, and logLimit the size at which it starts over, keeping one
// earlier file beside it as logName.1.
const (
	logName  = "herdr-auto-title.log"
	logLimit = 5 << 20
)

// logOutput is where the log goes: stderr, unless stderr is the null device,
// which is what a restart gives the instance it starts. Then the log goes to
// a file in stateDir, so a restarted instance is not silent. Herdr sets the
// directory in HERDR_PLUGIN_STATE_DIR; with none, or a file that cannot be
// opened, stderr it is.
func logOutput(stderr *os.File, stateDir string) io.Writer {
	if stateDir == "" || !isNull(stderr) {
		return stderr
	}

	//nolint:gosec // Herdr names the plugin's own state directory.
	if os.MkdirAll(stateDir, 0o700) != nil {
		return stderr
	}

	w, err := openLog(filepath.Join(stateDir, logName), logLimit)
	if err != nil {
		return stderr
	}

	return w
}

func isNull(f *os.File) bool {
	got, err := f.Stat()
	if err != nil {
		return false
	}

	null, err := os.Stat(os.DevNull)

	return err == nil && os.SameFile(got, null)
}

// rotating appends to a file and, once it passes limit, moves it to path.1
// and starts a new one, so a long run with debug logging stays bounded.
type rotating struct {
	mu    sync.Mutex
	path  string
	limit int64
	file  *os.File
	size  int64
}

func openLog(path string, limit int64) (*rotating, error) {
	r := &rotating{path: path, limit: limit}

	return r, r.open()
}

func (r *rotating) Write(p []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.size+int64(len(p)) > r.limit && r.size > 0 {
		_ = r.file.Close()
		_ = os.Rename(r.path, r.path+".1")

		if err := r.open(); err != nil {
			return 0, err
		}
	}

	n, err := r.file.Write(p)
	r.size += int64(n)

	return n, err
}

// Close closes the file being written.
func (r *rotating) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()

	return r.file.Close()
}

func (r *rotating) open() error {
	f, err := os.OpenFile(r.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}

	info, err := f.Stat()
	if err != nil {
		_ = f.Close()

		return err
	}

	r.file, r.size = f, info.Size()

	return nil
}
