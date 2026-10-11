package main

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestSettingsCommandPassesTheConfigFile(t *testing.T) {
	config := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", config)

	dir := t.TempDir()

	if _, _, err := settingsCommand(
		dir,
		nil,
	); err == nil ||
		!strings.Contains(err.Error(), "make panel") {
		t.Fatalf("without the panel built, err = %v", err)
	}

	if err := os.MkdirAll(filepath.Join(dir, "bin"), 0o755); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(
		filepath.Join(dir, panelBinary),
		nil,
		0o755,
	); err != nil {
		t.Fatal(err)
	}

	// The file already in XDG_CONFIG_HOME is the one the plugin reads.
	want := filepath.Join(config, "herdr-auto-title", "config.env")
	if err := os.MkdirAll(filepath.Dir(want), 0o700); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(want, nil, 0o600); err != nil {
		t.Fatal(err)
	}

	argv, env, err := settingsCommand(dir, []string{"KEPT=1"})
	if err != nil {
		t.Fatal(err)
	}

	if !slices.Equal(
		argv,
		[]string{filepath.Join(dir, panelBinary), filepath.Join(dir, panelSpec)},
	) {
		t.Errorf("argv = %q", argv)
	}

	if !slices.Equal(env, []string{"KEPT=1", EnvConfigFile + "=" + want}) {
		t.Errorf("env = %q", env)
	}
}
