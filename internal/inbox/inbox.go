// Package inbox carries requests from a command-line process to the running
// instance of the same session, as one small file each in a directory that
// belongs to that session alone.
package inbox

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// The operations a request can ask for.
const (
	OpRenameNow = "rename-now"
	OpRelease   = "release"
)

const (
	// maxFileSize and maxID bound what a drain will believe: a request is a line.
	maxFileSize = 4096
	maxID       = 128
	// perDrain bounds one drain, so a flooded inbox costs a few polls, not one.
	perDrain = 64
)

// Request asks the instance to do Op to one workspace.
type Request struct {
	Op          string `json:"op"`
	WorkspaceID string `json:"workspace_id"`
}

func (r Request) valid() bool {
	return (r.Op == OpRenameNow || r.Op == OpRelease) &&
		r.WorkspaceID != "" && len(r.WorkspaceID) <= maxID
}

// Send leaves the request in dir. The file is written under a temporary name
// and renamed, so a drain never reads half of one.
func Send(dir string, req Request) error {
	raw, err := json.Marshal(req)
	if err != nil {
		return err
	}

	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}

	tmp, err := os.CreateTemp(dir, ".send-*")
	if err != nil {
		return err
	}

	_, err = tmp.Write(raw)
	if closeErr := tmp.Close(); err == nil {
		err = closeErr
	}

	if err == nil {
		err = os.Chmod(tmp.Name(), 0o600)
	}

	if err == nil {
		err = os.Rename(tmp.Name(), filepath.Join(dir, fileName()))
	}

	if err != nil {
		_ = os.Remove(tmp.Name())
	}

	return err
}

// fileName sorts by time first, so a directory listing is oldest first.
func fileName() string {
	var salt [4]byte

	_, _ = rand.Read(salt[:])

	return fmt.Sprintf("%020d-%s.json", time.Now().UnixNano(), hex.EncodeToString(salt[:]))
}

// Drain returns the requests waiting in dir, oldest first, and removes every
// file it read. A file that says nothing usable is removed unanswered, and a
// missing directory holds nothing.
func Drain(dir string) []Request {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}

	var reqs []Request

	for _, entry := range entries {
		if len(reqs) == perDrain {
			break
		}

		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".json") {
			continue
		}

		path := filepath.Join(dir, name)
		req, ok := read(path)
		_ = os.Remove(path)

		if ok {
			reqs = append(reqs, req)
		}
	}

	return reqs
}

// read is the request in a file, or false when the file is not one.
func read(path string) (Request, bool) {
	file, err := os.Open(path) //nolint:gosec // the path is Auto Title's own
	if err != nil {
		return Request{}, false
	}

	defer func() { _ = file.Close() }()

	raw, err := io.ReadAll(io.LimitReader(file, maxFileSize+1))
	if err != nil || len(raw) > maxFileSize {
		return Request{}, false
	}

	var req Request
	if json.Unmarshal(raw, &req) != nil || !req.valid() {
		return Request{}, false
	}

	return req, true
}
