# claude-remote — project rules

Go CLI that bridges Telegram to a live Claude Code session running inside tmux.
Public repo: https://github.com/yabanci/claude-remote

## Architecture

`cmd/claude-remote` — CLI (`init`, `run`, `service install|uninstall|status`, `version`).

`internal/config` — YAML config at `~/.config/claude-remote/config.yaml` (0600). Holds bot token,
allowlist, session map, settle tuning. Seconds/ms fields expose duration helpers so the conversion
lives in one place.

`internal/telegram` — Bot API client on stdlib `net/http`. No third-party bot library.
`WithBaseURL` exists so tests point it at `httptest`, and so a self-hosted Bot API server works.

`internal/tmux` — thin wrapper over the `tmux` binary. `send-keys -l` sends text literally,
then `Enter` separately — never let user text be parsed as key names.

`internal/bridge` — the loop. `Runner` interface is declared here (call site, per Go style guide)
and implemented by `tmuxRunner`; tests inject a fake. `WaitForSettle`/`DiffTail` are pure and
unit-tested without tmux.

`internal/service` — launchd (darwin) / systemd --user (linux). `CommandRunner` interface keeps
`launchctl`/`systemctl` out of tests, so `go test` never installs a real service.

## Rules

- No comments in code (TODO/FIXME/HACK only) — enforced by the global CLAUDE.md rule.
- Anything that shells out gets an interface at the call site, so tests never touch the real system.
- Bridge commands use the `/cr_` prefix. Everything else is forwarded verbatim into the session,
  including Claude Code's own `/` commands — never intercept those.
- Before claiming done: `gofmt -l .`, `go vet ./...`, `golangci-lint run ./...`, `go test ./... -race`.

## Gotchas

The reply mechanism is screen scraping: capture pane, send keys, poll until the pane stops
changing, diff. It cannot distinguish "thinking" from "finished" — only "screen stopped changing".
`settle.stable_rounds` and `poll_interval_ms` are the tuning knobs; truncated replies mean raise them.

`go.mod` targets go 1.22 deliberately (CI and wider compatibility), not the local toolchain version.
