# claude-remote — hardening backlog

Repo: Go, module `github.com/yabanci/claude-remote`. Bridge between Telegram and a live
`claude` session running in tmux.

## Ground rules for every task

- No comments in code. Only `TODO:` / `FIXME:` / `HACK:` markers are allowed.
- Every change ships with tests that would fail without it.
- Interfaces are declared at the call site (`bridge.Runner`, `service.CommandRunner`,
  `service.platform`). Anything that shells out goes behind one — a test must never touch
  the real system.
- Before marking a task done, all of these must pass:
  `gofmt -l .` (empty), `go vet ./...`, `golangci-lint run ./...`,
  `go test ./... -race`, and `deadcode ./...` must report nothing.
- Commit each finished task separately with a message that says what broke and why, not
  what the diff shows.
- Never touch `.github/workflows`, `go.mod`, or the release configuration.

## Tasks

- [x] **Raise `cmd/claude-remote` coverage above 70%.** Was 51%. `main` read `os.Args` and
  called `os.Exit` directly, so `printUsage`, the `version`/`help` branches, and the
  unknown-subcommand path had no way to be exercised without spawning the binary.
  Extracted `run(args, stdout, stderr) int` and `runBridge(ctx, args) error`; `main` is now
  a thin wrapper around both. Coverage is 81.2%.

- [x] **Warn when the reply carries no known TUI marker.** `FormatReply` falls back to
  `CleanReply` when it finds no `⏺` block. That fallback is also what happens if a future
  Claude Code release changes its markers — the bridge would quietly start relaying screen
  scrapings again. `FormatReply` now takes a `*slog.Logger` and warns, naming the expected
  markers, when the fallback text is 4+ lines; the user-visible text is unchanged either
  way. Tested both the warn and no-warn paths.

- [ ] **Add `/cr_peek`.** Returns the current pane of the active session, formatted the
  same way as a reply, without typing anything into the session. Useful when a turn is
  still running or an answer was missed. Register it in `commandMenu` so it appears in
  Telegram's command list and in `/cr_help`. Test that it sends no keys.

- [ ] **Report a session that died mid-turn.** If the tmux session disappears between
  sending the message and reading the answer, the user currently gets a capture error with
  a raw Go message. Detect that the session is gone and reply with something actionable
  naming the session and suggesting `/cr_restart`. Test with a runner whose session
  vanishes after `SendKeys`.

- [x] **Validate session directories when the config loads.** `Config.Validate` checked that
  `dir` is non-empty but not that it exists, so a typo was only discovered when a message
  arrived and `tmux.Start` failed. `Validate` now `os.Stat`s every session's dir (through
  `ExpandDir`, so `~` still works) and reports every one that is missing in a single error,
  sorted by session name, instead of stopping at the first. Tested with two bad directories
  and with a `~`-relative one that does resolve.

- [x] **Make `/cr_kill` refuse to kill a session that is not configured.** Done before the
  loop started: it killed any tmux session by name, including one the user runs by hand.
  Restricted to configured sessions, with tests.
