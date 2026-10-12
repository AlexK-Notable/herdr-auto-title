# Security Policy

This is the security policy of a fork,
[AlexK-Notable/herdr-auto-title](https://github.com/AlexK-Notable/herdr-auto-title).
The original plugin is [kryptamine/herdr-auto-title](https://github.com/kryptamine/herdr-auto-title),
which has a policy of its own.

## Supported versions

This fork makes no releases of its own: only the newest commit on `main` is
supported.

## Reporting a vulnerability

Report it privately through GitHub: the repository's **Security** tab, then
**Report a vulnerability**
([direct link](https://github.com/AlexK-Notable/herdr-auto-title/security/advisories/new)).
Do not open a public issue for anything exploitable. A flaw that is in the
original plugin too belongs with its author as well; their policy says how.

Include what makes it reproducible:

- the plugin version (the `version` field in `herdr-plugin.toml`), your Herdr
  version and your OS;
- what an attacker controls (a directory name, a terminal title, a file an
  agent wrote) and what that gets them;
- the shortest sequence that shows it.

This fork is kept by one person, so there is no fixed response time, but you
will hear where a report stands. Say whether you plan to disclose it and when,
and the fix is timed to that. You get credit for it unless you would rather not
be named.

## Scope

Auto Title polls a local Herdr session over a local socket — a Unix socket, or
a named pipe on Windows — and renames tabs. Every value that reaches a tab
title comes from terminal output and is treated as hostile;
[docs/architecture/sanitization.md](docs/architecture/sanitization.md) says
what is stripped and what is rejected.

The original plugin talks to nothing but that socket. This fork adds two things
that do more:

- **Naming agents and workspaces asks a model.** The default `cli` backend runs
  `claude -p` with no tools, no MCP servers and no saved session, in an
  environment without `HERDR_*` or Anthropic keys, and passes the request on
  stdin, never in its arguments. The `api` backend calls `api.anthropic.com`
  with a key from `ANTHROPIC_API_KEY`, or from running
  `HERDR_AUTO_TITLE_API_KEY_CMD` without a shell. **What it sends leaves your
  machine:** for an agent, its working directory, its terminal title, its
  current name and the last four prompts typed into it (400 characters each at
  most); for a workspace, its folder and repository names, its agents' names
  and the latest request to each. With `HERDR_AUTO_TITLE_AGENTS` and
  `HERDR_AUTO_TITLE_NAME_WORKSPACES` both off, nothing is sent.
- **The settings panel** is [herdr-panel](https://github.com/AlexK-Notable/herdr-panel)
  drawing `panel.toml`. It edits `config.env` and runs the commands
  `panel.toml` names, which are this plugin's own, without a shell.

Worth reporting:

- a terminal-derived value reaching a shell, a subprocess or a path;
- an escape, control or format character that survives `Sanitize` into a tab
  label, or a label that forges structure the session does not have;
- a read escaping Claude Code's `projects` directory: a session id arrives over
  the socket and becomes part of a transcript path;
- a crash or an unbounded read a pane can trigger on its own;
- a prompt or a terminal-derived value reaching the arguments of `claude -p`,
  the key command, or a shell;
- anything sent to the model that this page does not list.

Not this project's to fix:

- vulnerabilities in Herdr itself, which go to [herdr.dev](https://herdr.dev);
- anything that presupposes an attacker who already runs code as you. Whoever
  reaches the Herdr socket owns the session with or without this plugin;
- a title that is wrong or unhelpful. That is a bug, so open an issue.
