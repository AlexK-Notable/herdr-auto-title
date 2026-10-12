package main

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// skipWithoutDevNull skips where the null device cannot be told apart by
// Stat, as Windows' NUL.
func skipWithoutDevNull(t *testing.T) {
	t.Helper()

	if runtime.GOOS == "windows" {
		t.Skip("NUL is not a file Stat can compare")
	}
}

func TestARestartedInstanceLogsToAFileInItsStateDirectory(t *testing.T) {
	t.Parallel()
	skipWithoutDevNull(t)

	null, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = null.Close() }()

	dir := filepath.Join(t.TempDir(), "state")

	out := logOutput(null, dir)
	if out == null {
		t.Fatal("with stderr on the null device, the log still went there")
	}

	if _, err := out.Write([]byte("hello\n")); err != nil {
		t.Fatal(err)
	}

	raw, err := os.ReadFile(filepath.Join(dir, logName))
	if err != nil || string(raw) != "hello\n" {
		t.Errorf("log file = %q, %v", raw, err)
	}
}

func TestAnInstanceWithARealStderrLogsThere(t *testing.T) {
	t.Parallel()

	stderr, err := os.Create(filepath.Join(t.TempDir(), "stderr"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = stderr.Close() }()

	if out := logOutput(stderr, t.TempDir()); out != stderr {
		t.Error("a usable stderr was replaced by a file")
	}
}

func TestWithNoStateDirectoryTheLogStaysOnStderr(t *testing.T) {
	t.Parallel()
	skipWithoutDevNull(t)

	null, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = null.Close() }()

	if out := logOutput(null, ""); out != null {
		t.Error("with no state directory, the log moved anyway")
	}
}

func TestTheLogStartsOverPastItsLimitAndKeepsOneEarlierFile(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), logName)

	w, err := openLog(path, 10)
	if err != nil {
		t.Fatal(err)
	}
	// Windows cannot remove the directory while the file is open.
	t.Cleanup(func() { _ = w.Close() })

	for _, line := range []string{"aaaaaa\n", "bbbbbb\n", "cccccc\n"} {
		if _, err := w.Write([]byte(line)); err != nil {
			t.Fatal(err)
		}
	}

	now, _ := os.ReadFile(path)
	before, _ := os.ReadFile(path + ".1")

	if string(now) != "cccccc\n" || string(before) != "bbbbbb\n" {
		t.Errorf("log = %q, log.1 = %q; want the newest line and the one before", now, before)
	}

	if strings.Contains(string(now)+string(before), "aaaaaa") {
		t.Error("more than one earlier file was kept")
	}
}
