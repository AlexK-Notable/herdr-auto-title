// Command herdr-auto-title is the Herdr Auto Title plugin process: started once,
// it stays alive and keeps every tab's title in step with what that tab is
// doing. `herdr-auto-title restart` starts a fresh one in its place.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/kryptamine/herdr-auto-title/internal/app"
	"github.com/kryptamine/herdr-auto-title/internal/herdr"
	"github.com/kryptamine/herdr-auto-title/internal/inbox"
	"github.com/kryptamine/herdr-auto-title/internal/instance"
	"github.com/kryptamine/herdr-auto-title/internal/namer"
	"github.com/kryptamine/herdr-auto-title/internal/panel"
)

func main() {
	var err error

	switch {
	case len(os.Args) == 1:
		err = run()
	case len(os.Args) == 2 && os.Args[1] == "restart":
		err = restart()
	case len(os.Args) == 3 && os.Args[1] == "panel" && os.Args[2] == "workspaces":
		os.Exit(panelCommand(func(ctx context.Context, env panel.Env) int {
			return panel.Workspaces(ctx, os.Stdout, env)
		}))
	case len(os.Args) == 4 && os.Args[1] == "workspace" && workspaceOps[os.Args[2]]:
		op, id := os.Args[2], os.Args[3]

		os.Exit(panelCommand(func(ctx context.Context, env panel.Env) int {
			return panel.Act(ctx, os.Stdout, env, op, id)
		}))
	default:
		err = errors.New(usage)
	}

	if err != nil {
		slog.Error("auto title stopped", "error", err)
		os.Exit(1)
	}
}

const usage = "usage: herdr-auto-title [restart | panel workspaces | " +
	"workspace rename-now <id> | workspace release <id>]"

// workspaceOps are the requests `workspace` takes, which are the inbox's.
var workspaceOps = map[string]bool{inbox.OpRenameNow: true, inbox.OpRelease: true}

// panelCommand runs one panel command against the session this process was
// started in, and returns its exit status. It prints no log: the panel shows
// stdout, and a failure is a JSON message there.
func panelCommand(command func(context.Context, panel.Env) int) int {
	cfg, _ := app.LoadConfig()

	client, err := herdr.New()
	if err != nil {
		return panel.Fail(os.Stdout, "not running inside a Herdr session")
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	return command(ctx, panel.Env{
		Client:     client,
		Socket:     client.Path(),
		StateDir:   app.StateDir(),
		ManualPath: cfg.ManualPath,
	})
}

// setup reads the configuration, makes a logger at the level it asks for, and
// a client for the session this process was started in.
func setup() (app.Config, *slog.Logger, *herdr.SocketClient, error) {
	cfg, warnings := app.LoadConfig()

	level := slog.LevelInfo
	if cfg.Debug {
		level = slog.LevelDebug
	}

	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level}))
	slog.SetDefault(log)

	for _, warning := range warnings {
		log.Warn(warning)
	}

	client, err := herdr.New()

	return cfg, log, client, err
}

func run() error {
	cfg, log, client, err := setup()
	if err != nil {
		return err
	}

	log.Info("starting auto title", "poll", cfg.Poll, "max_length", cfg.MaxLength)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// Take waits for the instance it displaces, which looks at the claim
	// before every poll; the manual-name locks are loaded only once it has.
	claim, stayed, err := instance.Take(
		ctx,
		instance.File(app.StateDir(), client.Path()),
		app.LeaveTimeout,
	)
	if err != nil {
		log.Warn(
			"the session could not be claimed, so a restart will not end this instance",
			"error",
			err,
		)
	}

	if stayed != 0 {
		log.Warn("the instance this one replaces is still running", "pid", stayed)
	}

	titles, panes, topics := app.Resolvers(cfg)
	auto := app.New(cfg, log, titles, panes, topics, claim).
		WithInbox(instance.Inbox(app.StateDir(), client.Path()))

	if service := modelNamer(ctx, cfg, log); service != nil {
		go service.Run(ctx)

		if namesAgents(cfg, log) {
			auto.WithAgentNamer(service)
		}

		if cfg.NameWorkspaces {
			auto.WithWorkspaceNamer(service)
		}
	}

	auto.Run(ctx, client)

	return nil
}

// modelNamer builds the service that names agents and workspaces, or nil when
// both are off or it cannot work. A namer that cannot start costs those names,
// never tab titles.
func modelNamer(ctx context.Context, cfg app.Config, log *slog.Logger) *namer.Service {
	if !cfg.NameAgents && !cfg.NameWorkspaces {
		return nil
	}

	backend, err := app.NamerBackend(ctx, cfg)
	if err != nil {
		log.Warn("agents and workspaces are not named", "error", err)
		return nil
	}

	log.Info("naming with a model",
		"backend", backend.String(),
		"agents", cfg.NameAgents, "interval", cfg.NamerInterval,
		"workspaces", cfg.NameWorkspaces, "workspace_interval", cfg.WorkspaceInterval)

	return namer.New(namer.Options{
		Backend:           backend,
		Interval:          cfg.NamerInterval,
		WorkspaceInterval: cfg.WorkspaceInterval,
		Path:              cfg.AgentNamesPath,
		Log:               log,
	})
}

// namesAgents reports whether agents are to be named. Naming one reads its
// transcript, so it needs transcripts read; a workspace does without.
func namesAgents(cfg app.Config, log *slog.Logger) bool {
	if cfg.NameAgents && !cfg.ReadTranscripts {
		log.Warn("agents are not named: naming them reads their transcripts, which " +
			app.EnvTranscript + " turned off")

		return false
	}

	return cfg.NameAgents
}

// restart starts a fresh instance in this one's session and says how that went,
// in the log Herdr keeps of the action and in a notification.
func restart() error {
	_, log, client, err := setup()
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	exe, err := os.Executable()
	if err != nil {
		return err
	}

	// The instance being replaced has LeaveTimeout to go, and the new one a
	// first poll after that.
	pid, err := instance.Restart(
		ctx,
		instance.File(app.StateDir(), client.Path()),
		exe,
		app.LeaveTimeout+app.PollTimeout,
	)

	title, body := "Auto Title restarted", fmt.Sprintf("pid %d is naming the session", pid)
	if err != nil {
		title, body = "Auto Title did not restart", err.Error()
	}

	notice, notifyErr := herdr.ShowNotification(ctx, client, title, body)

	switch {
	case notifyErr != nil:
		log.Warn("notification failed", "error", notifyErr)
	case !notice.Shown:
		log.Debug("notification not shown", "reason", notice.Reason)
	}

	if err != nil {
		return err
	}

	log.Info(title, "pid", pid)

	return nil
}
