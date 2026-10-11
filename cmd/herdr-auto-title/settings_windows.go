package main

import "errors"

// execPanel fails: herdr-panel runs on Linux and macOS only.
func execPanel([]string, []string) error {
	return errors.New("the settings panel runs on Linux and macOS only")
}
