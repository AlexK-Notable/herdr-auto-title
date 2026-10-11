//go:build !windows

package main

import "syscall"

// execPanel runs the panel in this process's place, so Herdr's close signals
// reach it directly. It returns only on failure.
func execPanel(argv, env []string) error {
	// argv is our own bin/herdr-panel and panel.toml, next to this binary.
	//nolint:gosec // G204: see above
	return syscall.Exec(argv[0], argv, env)
}
