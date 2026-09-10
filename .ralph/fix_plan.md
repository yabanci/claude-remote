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

## Explicitly out of scope for this pass

- Telegram-update-offset ordering in `bridge.go` (`offsets.save` runs before `handleUpdate`
  processes the message). This trades "may lose a message if the process is killed mid-turn"
  against "may replay/double-execute a message into the live session after a crash" — a
  deliberate choice already made once, guarded by `TestARestartedBridgeDoesNotReplayHandledMessages`.
  It is a design trade-off, not a bug. Do not touch it in this pass.

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

- [x] **Add `/cr_peek`.** Returns the current pane of the active session, formatted the
  same way as a reply, without typing anything into the session. Useful when a turn is
  still running or an answer was missed. Register it in `commandMenu` so it appears in
  Telegram's command list and in `/cr_help`. Test that it sends no keys.

- [x] **Report a session that died mid-turn.** If the tmux session disappears between
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

- [ ] **Fix `/cr_interrupt` starvation during a long-running reply.** `Run()` processes
  Telegram updates strictly sequentially — `GetUpdates` is not called again until
  `handleUpdate` fully returns, and `handleUpdate` can block inside `WaitForSettle` for up
  to `hard_cap_seconds` (default 1200s / 20 min). A user who wants to interrupt a stuck
  reply can't: `/cr_interrupt` sits unfetched on Telegram's side for the whole wait. Fix:
  dispatch each update's handling so the bridge keeps polling `GetUpdates` while a previous
  message is still in flight — run `handleUpdate` in its own goroutine, guarded by a
  per-session mutex so two messages to the *same* session never interleave, while control
  commands (`/cr_interrupt`, `/cr_kill`, `/cr_status`, `/cr_peek`) bypass that mutex
  entirely and execute immediately regardless of whether the session is currently busy.
  Test: a fake runner whose capture blocks until released, prove `/cr_interrupt` is
  delivered and acted on while the first message is still in flight.

- [x] **Reject tmux-unsafe session names in `/cr_new`.** `cmdNew` stored `parts[0]` as the
  session name with no validation and passed it straight through to every tmux `-t`
  target. A name containing `:` (tmux's session:window.pane separator) created a session
  that could never again be reached, messaged, or killed by the bridge — it silently
  orphaned. Added `validSessionName` (`^[A-Za-z0-9_-]+$`), checked before anything is
  written to config or started in tmux, so a rejected name never gets a partially-created
  broken session. `/cr_help`'s `/cr_new` description now says what's allowed. Tested with
  a `:`-containing name: rejected, no tmux session, no config file written.

- [x] **Fix path traversal in `/cr_send`.** `cmdSend` only special-cased absolute paths and
  otherwise joined onto the session dir with no `filepath.Clean` or containment check, so
  `/cr_send /etc/hosts` and `/cr_send ../../.ssh/id_rsa` both read files outside the
  session's working directory. Added `resolveSendPath`: cleans the argument, resolves it
  against the session dir (absolute args are taken as-is, relative ones joined first), then
  rejects anything whose path relative to the session dir is `..` or starts with `../`.
  Absolute paths that do resolve inside the session dir still work. Updated the
  `commandMenu`/`/cr_help` description to say the boundary is enforced. Tested an absolute
  path outside the dir, a `../` traversal, and an absolute path inside the dir.

- [x] **Add a timeout to every `internal/service` shell-out.** `execRunner.Run`/
  `CombinedOutput` called plain `exec.Command` with no context or timeout — unlike every
  tmux call, which wraps `exec.CommandContext` in a 5s/15s timeout. A hung
  `launchctl`/`systemctl` blocked `claude-remote service install|uninstall|status`
  forever. Gave `execRunner` the same `context.WithTimeout` treatment tmux uses: a
  configurable `timeout` field (defaulting to 10s via `newExecRunner`), `exec.CommandContext`,
  and `ErrCommandTimeout` returned when the context deadline is what stopped the command.
  Tested with a real `sleep 5` process and a 50ms timeout on both `Run` and
  `CombinedOutput`, proving the caller gets `ErrCommandTimeout` instead of hanging.

- [x] **Log offset-file read failures that aren't "file missing".** `offsetStore.load()`
  returned `0` with no log line when `os.ReadFile` failed for any reason other than the
  file not existing — the sibling `ParseInt` failure branch three lines below already
  logged a warning, this one didn't. A permission or I/O error on `offset.txt` silently
  reset the bridge to redeliver Telegram's entire retained update history with zero trace
  in the logs. `load()` now checks `os.IsNotExist` and only stays silent for that case;
  every other read error gets the same `s.log.Warn` treatment as the parse-failure branch.
  Tested the missing-file path stays silent and a non-missing read error (offset.txt
  replaced by a directory) logs the warning.

- [x] **Route `/cr_peek` capture failures through `reportCaptureFailure`.** `cmdPeek`
  hand-rolled its own capture-error reply instead of calling the shared
  `reportCaptureFailure` helper every other capture call site
  (`forwardToSession`, `heldBackByOpenDialog`, `sendAndAwait`, `deliverAnswer`) uses. If
  the session vanished between `Exists()` and `CapturePane()`, every other command gave
  the actionable "session vanished, run /cr_restart" message — `/cr_peek` alone gave a
  raw Go error. Now it calls `reportCaptureFailure` like the rest. Tested with a runner
  (`vanishesOnCaptureRunner`) whose session vanishes inside `CapturePane`, asserting
  `/cr_peek` gets the same "пропала … /cr_restart" message as the other commands.

- [ ] **Retry the Telegram client on transport errors and 5xx, not just 429.**
  `Client.call()`'s retry loop only backs off when `retryAfter()` returns a positive wait,
  which only happens for a parsed HTTP 429; a transport error, a body-read failure, a
  JSON-decode failure, or any non-429 API error (including 5xx) returns immediately with
  zero retries and the reply is lost for good. Extend the retry policy to cover transport
  errors and 5xx responses with the existing exponential-backoff mechanism (respect
  `max_retries`). Test that a transient 500 and a simulated network error both get retried
  and eventually succeed.

- [x] **Make `Config.Save` and the offset store's write atomic.** Both wrote in place via a
  single `os.WriteFile` with no temp-file-plus-rename. A crash or power loss mid-write could
  leave a truncated, unparseable `config.yaml` (losing the bot token and allowlist — the
  bridge won't start) or a truncated `offset.txt` (parses as garbage, resets to offset 0,
  replays already-handled updates). Added `internal/atomicfile.Write` — writes to a temp
  file in the target's directory, `fsync`s, `chmod`s to the requested permission, then
  `os.Rename`s over the target, removing the temp file if any step before the rename fails.
  Wired it into both `config.Save` and `offsetStore.save`. Tested `atomicfile.Write` directly
  (content, permissions, no leftover temp file on success, no partial file when the target
  dir is missing, original untouched when the rename itself fails) and tested both call
  sites for wholesale replacement and correct final permissions.

- [x] **Propagate the real error from `service uninstall`.** Both `launchd.disable()` and
  `systemd.disable()` discarded the error from the command that actually stops the service
  (`_ = runner.Run(...)`) and `launchd.disable` unconditionally `return nil`ed;
  `systemd.disable` only propagated the later `daemon-reload` error. `claude-remote
  service uninstall` could print "service uninstalled" while the service was still running.
  `launchd.disable` now wraps and returns the `launchctl unload` error; `systemd.disable`
  still always attempts both `disable --now` and `daemon-reload` (cleanup should run
  either way) but now collects and `errors.Join`s whichever of the two failed, instead of
  discarding the first. `Manager.Uninstall()` already returned `disable`'s error and now
  correctly stops before removing the unit file when the service failed to stop, leaving a
  file that still reflects reality. Tested both platforms with a fake runner that fails the
  unload/disable step: error is returned and names the failing tool, unit file survives.

- [ ] **Make `service status` distinguish "not installed" from "installed but failed".**
  Both `systemd.status()` and `launchd.status()` collapse *any* non-nil error from the
  underlying status command into the fixed string "not installed", discarding what the
  tool actually said (e.g. a legitimately failed/crashed unit). Parse the actual state
  where possible (`systemctl is-active` exit codes distinguish inactive/failed/unknown;
  `launchctl list` output can be inspected) and report "failed"/"stopped" distinctly from
  "not installed". Test with a fake runner returning a "failed" status specifically,
  asserting the report differs from the not-installed case.

- [ ] **Roll back `/cr_new`'s config entry when `Start` fails.** `cmdNew` writes the new
  session into `b.cfg.Sessions`, saves the config, and marks it active *before* calling
  `runner.Start`. If `Start` fails, the handler only replies with the error — it never
  removes the session from `b.cfg.Sessions`/re-saves config, leaving a dead entry that
  `/cr_status`, `/cr_use`, and plain-text routing keep referencing. Delete the entry (and
  re-save, and clear the active-session pointer if it was set) on a `Start` failure. Test
  with a runner that fails `Start`, asserting the session no longer appears in the saved
  config or in `/cr_status` afterward.

- [ ] **Cap menu rows and add a text fallback.** `replyWithMenu` puts every option from
  `ParseMenu` into a single unbounded row; with enough options Telegram's own
  buttons-per-row limits make the keyboard broken or unusable. Wrap after a reasonable
  number of buttons per row (e.g. every 3-4). Also: if `b.tg.Send` fails when sending the
  keyboard, the code only logs and returns — the user gets nothing. Add a plain-text
  fallback (numbered list of the same options) sent via `b.reply` when the keyboard send
  fails. Test both: a menu with many options wraps into multiple rows, and a forced Send
  failure falls back to text.

- [ ] **Wire `interim_notice_seconds` to something real.** Both production call sites of
  `WaitForSettle` (`ensureRunning`, `sendAndAwait`) pass a literal `nil` for the
  `onInterim` callback, even though `settle.go` fully implements and unit-tests the
  interim-notice mechanism. The config knob does nothing. Pass a real callback from both
  call sites that sends an interim "ещё работаю…" reply via `b.reply` when it fires. Test
  that a long-running fake capture triggers at least one interim message via the harness's
  fake Telegram, for both call sites.

- [x] **Fix the tool-call-detection regex false positive.** `toolCallPattern`
  (`^[A-Z][A-Za-z]*\(`) matched genuine assistant prose shaped like "Filter(x) returns…" or
  "Update(id) done", not just actual tool-call lines, causing `ExtractAnswer` to drop real
  answer text and `FormatReply` to report "no text answer" when there was one. Real
  tool-call lines are always immediately followed by a `⎿` result line; prose never is.
  Added `followedByToolResult`, which `ExtractAnswer` now requires alongside the regex
  before counting a line as a tool call. Tested with a prose line shaped like a function
  call — it now survives into the extracted answer with zero tool blocks counted.

- [ ] **Recognize commands sent with an `@botname` suffix.** `handleCommand`'s switch
  compares the first whitespace-separated field against exact literals like
  `"/cr_status"`; Telegram appends `@<botname>` to commands in group chats, so
  `/cr_status@mybot` falls through to "unknown command". Strip a trailing `@<name>` from
  the command token before the switch. Test with a command carrying the suffix.

- [x] **Escape untrusted values in the launchd/systemd templates.** `launchd.render`/
  `systemd.render` interpolated `execPath`/`logDir`/`searchPath` via raw `fmt.Sprintf` with
  no escaping: the launchd template placed them inside XML `<string>` tags (a `&`/`<`/`>`
  in a path broke the plist), and the systemd template placed `execPath` unquoted into
  `ExecStart=%s run` (a space in the path split it into two arguments). Added `xmlEscape`
  (wraps `xml.EscapeText`), applied to `execPath`/`searchPath`/`logDir` in `launchd.render`.
  Added `systemdArg`/`systemdEnv` (quote plus backslash/`"` escaping), applied to `execPath`
  in `ExecStart=` and to the `PATH` assignment in `Environment=`; the systemd template no
  longer hardcodes its own quotes since the helpers now own them. Updated the existing
  `mustContain` fixture for the now-quoted `ExecStart=` line. Tested a launchd path with an
  embedded `&` and space (escaped output plus `xml.Unmarshal` proving the rendered plist is
  well-formed), a systemd path with a space (still one argument), and a systemd path with an
  embedded `"` (escaped, not closing the quote early).

- [ ] **Make `elapsed` in `WaitForSettle` reflect real wall-clock time.** `elapsed` is
  incremented by the fixed `pollInterval` before `capture()` runs each round; `capture()`'s
  own duration (tmux's own timeout allows up to 15s) is never added, so reaching
  `hard_cap_seconds` in real time can take meaningfully longer than the configured cap.
  Track elapsed against an actual clock read each round instead of a running sum of the
  poll interval alone. Test that a slow fake capture function causes the hard cap to trip
  closer to the configured wall-clock duration than the naive poll-interval sum would
  predict.

- [ ] **Route `cmdService`'s output through the injected writer.** `cmdService`'s success
  paths call `fmt.Println` directly instead of using the `stdout` writer that `run()`
  threads through every other subcommand — its output is untestable by any caller of
  `run()`. Thread `stdout` into `cmdService` and use it. Test by asserting on the captured
  buffer the way `main_test.go`'s other `TestRun*` cases already do.

- [x] **Fix `DiffTail`'s scrollback-eviction case.** `DiffTail`'s common-prefix walk assumed
  the "before" and "after" pane captures start at the same line; once tmux's bounded
  history (`history-limit` 5000, equal to `captureHistoryLines`) evicts lines between the
  two captures — routine for any session that's been running a while — the common prefix
  collapsed to zero and the *entire* captured pane (up to 5000 lines of already-seen
  conversation) got sent back to the user as if it were the new reply. This path is only
  reached when `TailAfterPrompt` can't find the echoed prompt line. Added
  `scrollbackLikelyEvicted`: when the common prefix is zero *and* both captures are at
  least `captureHistoryLines` long (the signal that the history buffer was already near
  its cap, so eviction is plausible rather than "brand new session with nothing in
  common"), `DiffTail` now returns a "screen scrolled too far — check /cr_peek" message
  instead of the raw capture. A short pane with zero common prefix (session genuinely has
  nothing in common, buffer not yet full) still returns the whole new content as before.
  Tested both the eviction fallback (5000-line fixtures, no common prefix) and the
  not-yet-full case (10-line fixtures, no common prefix) to confirm the heuristic doesn't
  fire on a normal short session.

- [ ] **Cover `ensureRunning`'s cold-start failure branches.** No test exercises `Start()`
  failing or `WaitForSettle()` failing/timing out right after a fresh `Start()` — every
  fast test pre-seeds the session so `runner.Exists()` is always true. Add tests using the
  fake runner configured to fail `Start`, and separately to make the post-start
  `WaitForSettle` fail, asserting the user gets the expected error reply in each case.

- [ ] **Make the `/cr_use` test verify actual routing, not just the reply text.**
  `TestCrUseSwitchesActiveSession` only asserts the confirmation text contains the session
  name; it never sends a follow-up message and checks which session's fake runner
  received it. Extend it to send a message after switching and assert it reached the
  newly-active session's runner, not the default one.

- [ ] **Cover `handleUpload`'s `GetFile`/`DownloadFile` failure branches.** The fake
  Telegram test double always returns success for `getFile` and the file-download
  endpoint; no test makes either fail. Add cases where the fake returns an error/failure
  for each, asserting the user gets a sensible reply rather than a swallowed or malformed
  error.

- [ ] **Cover `offsetStore.load()`'s corrupted-file fallback.** No test exercises the
  branch where `offset.txt` exists but contains non-numeric garbage (the `strconv.ParseInt`
  failure path). Add one, asserting the warning is logged (see the offset-read-error task
  above) and the store falls back to offset 0.

- [ ] **Cover `bootstrapOwner`'s `config.Save` failure branch.** This is the fail-closed
  path that refuses to bind bridge ownership to the first sender when the binding can't be
  persisted — currently untested anywhere in the module, including `config.Save`'s own
  `MkdirAll`/`WriteFile` error branches. Make the config path unwritable in a test (or
  inject a save failure) and assert the bridge does *not* set `b.cfg` to the candidate and
  replies with the refusal message.

- [ ] **De-duplicate repeated error strings.** `"сессия %q не запущена"` is written out
  identically in `cmdKill`, `cmdInterrupt`, and `cmdPeek`; `"не удалось прочитать экран
  сессии: %v"` is duplicated between `reportCaptureFailure` and `cmdPeek` (resolved
  incidentally by the `/cr_peek` task above, but check); `"не удалось остановить: %v"` is
  duplicated between `cmdKill` and `cmdRestart`. Extract shared helpers/constants so each
  message is written once.

- [ ] **Name the `/rc` chrome filter in `clean.go`.** `isChrome()` matches the exact
  literal `"/rc"` with nothing in the source explaining what it strips or why — it's the
  tail of the Claude Code CLI's statusline. Give it a named constant with a name that says
  what it is, so a future reader doesn't have to reverse-engineer it from test fixtures.
