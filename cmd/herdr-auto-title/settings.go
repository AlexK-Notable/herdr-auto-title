package main

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/kryptamine/herdr-auto-title/internal/app"
	"github.com/kryptamine/herdr-auto-title/internal/herdr"
)

// EnvConfigFile tells the settings panel which file to edit. The panel's
// spec reads it, so the panel and the plugin agree on the file even where
// the plugin found it somewhere other than XDG_CONFIG_HOME or ~/.config.
const EnvConfigFile = "HERDR_AUTO_TITLE_CONFIG_FILE"

// The settings panel: a herdr-panel binary built next to this one by
// `make panel`, and the spec it draws.
const (
	panelBinary = "bin/herdr-panel"
	panelSpec   = "panel.toml"
)

// settingsCommand is what `settings` runs, from the directory dir holding
// this binary: the panel's argument vector, and the environment it gets.
func settingsCommand(dir string, environ []string) (argv, env []string, err error) {
	path := app.ConfigPath()
	if path == "" {
		return nil, nil, errors.New("there is no configuration directory to keep settings in")
	}

	bin := filepath.Join(dir, panelBinary)
	if _, err := os.Stat(bin); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil, fmt.Errorf("%s is not built: run `make panel` in %s", panelBinary, dir)
		}

		return nil, nil, err
	}

	// An inherited value would win: of two duplicates, a program exec'd with
	// both reads the first.
	environ = slices.DeleteFunc(slices.Clone(environ), func(kv string) bool {
		return strings.HasPrefix(kv, EnvConfigFile+"=")
	})

	return []string{
		bin,
		filepath.Join(dir, panelSpec),
	}, append(
		environ,
		EnvConfigFile+"="+path,
	), nil
}

// settings replaces this process with the settings panel. The panel runs in
// a Herdr popup, which closes as soon as its process exits, so a failure is
// held on screen until a key is pressed.
func settings() error {
	exe, err := os.Executable()
	if err == nil {
		exe, err = filepath.EvalSymlinks(exe)
	}

	var argv, env []string
	if err == nil {
		argv, env, err = settingsCommand(filepath.Dir(exe), os.Environ())
	}

	if err == nil {
		err = execPanel(argv, env)
	}

	fmt.Printf("Auto Title settings cannot open: %v\n\npress Enter to close\n", err)

	read := make(chan struct{})

	go func() {
		_, _ = os.Stdin.Read(make([]byte, 1))

		close(read)
	}()

	select {
	case <-read:
	case <-time.After(time.Minute):
	}

	return err
}

// The manifest's id, and its [[panes]] entry for the panel.
const (
	pluginID      = "herdr.auto-title"
	settingsEntry = "settings"
	// openTimeout bounds the request to open the popup, which Herdr answers
	// at once.
	openTimeout = 5 * time.Second
)

// openSettings asks Herdr to open the settings popup: the action's way in.
// An action has no screen of its own, so a failure becomes a notification.
func openSettings() error {
	client, err := herdr.New()
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), openTimeout)
	defer cancel()

	id := os.Getenv("HERDR_PLUGIN_ID")
	if id == "" {
		id = pluginID
	}

	err = herdr.OpenPluginPane(ctx, client, id, settingsEntry)
	if err == nil {
		return nil
	}

	body := err.Error()
	if herdr.ErrorCode(err) == herdr.CodeUIBusy {
		body = "Another popup is open; close it first."
	}

	_, _ = herdr.ShowNotification(ctx, client, "Auto Title settings did not open", body)

	return err
}

// envPrefix starts the name of every setting Auto Title reads.
const envPrefix = "HERDR_AUTO_TITLE_"

// settingsOverrides is a line naming the settings the environment sets,
// which win over the configuration file, or "" when there are none: the
// panel shows it, since saving those in the file changes nothing.
func settingsOverrides(environ []string) string {
	var keys []string

	for _, kv := range environ {
		key, _, _ := strings.Cut(kv, "=")
		if strings.HasPrefix(key, envPrefix) && key != EnvConfigFile {
			keys = append(keys, key)
		}
	}

	if len(keys) == 0 {
		return ""
	}

	slices.Sort(keys)

	return "Set by the environment Herdr started Auto Title with, so the file's value is not used: " +
		strings.Join(
			keys,
			", ",
		)
}
