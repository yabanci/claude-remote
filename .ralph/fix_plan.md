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

- `/cr_interrupt` starvation during a long-running reply (`Run()` processes updates strictly
  sequentially; `handleUpdate` can block inside `WaitForSettle` for up to `hard_cap_seconds`,
  so `/cr_interrupt` sits unfetched for the whole wait). The user is fixing this by hand
  (WIP: goroutine-per-update dispatch, per-session mutex, `internal/bridge/state.go`). DO NOT
  touch `internal/bridge/bridge.go`'s `Run`/`dispatch`, `internal/bridge/commands.go`'s
  dispatch wiring, `internal/bridge/state.go`, or `internal/bridge/harness_test.go`'s
  `awaitStop`/shutdown helpers in this pass — those are being edited live outside this loop.

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

- [x] **Retry the Telegram client on transport errors and 5xx, not just 429.**
  `Client.call()`'s retry loop only backed off when `retryAfter()` returned a positive wait,
  which only happened for a parsed HTTP 429; a transport error, a body-read failure, a
  JSON-decode failure, or any non-429 API error (including 5xx) returned immediately with
  zero retries and the reply was lost for good. Added a `retryWithBackoff` sentinel that
  `doWithRetryHint` returns for a transport error, a body-read failure, a 5xx with an
  unparseable body, and a 5xx API error without a `retry_after`; `call()` turns that
  sentinel into an exponential backoff (`backoffForAttempt`, 500ms doubling per attempt,
  capped at `maxRetryAfter`) while still respecting `max_retries` and a real 429's
  `retry_after`. A non-5xx API error (e.g. 400) still returns zero retries immediately.
  Tested a transient 500 and a simulated dropped-connection network error both get retried
  and eventually succeed, and that persistent 500s still give up after `max_retries`.

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

- [x] **Make `service status` distinguish "not installed" from "installed but failed".**
  Both `systemd.status()` and `launchd.status()` collapsed *any* non-nil error from the
  underlying status command into the fixed string "not installed", discarding what the
  tool actually said (e.g. a legitimately failed/crashed unit). `systemd.status` now reads
  `systemctl is-active`'s own output: "failed" and "inactive" get their own `statusFailed`/
  `statusStopped` results instead of collapsing into `statusNotInstalled`; only a genuinely
  unrecognized state (or `is-active` erroring with no usable text) still means "not
  installed". `launchd.status` now inspects `launchctl list`'s plist-ish output: a `"PID"`
  key means running (returns the raw text as before), otherwise a non-zero
  `"LastExitStatus"` means `statusFailed` and a zero one means `statusStopped`. Fixing this
  broke `TestStatusReturnsToolOutput`, which asserted launchd passed arbitrary tool output
  straight through — that assumption no longer holds now that the output is parsed;
  replaced it with six platform-specific tests (`TestLaunchdStatusReportsRunningWithPID`,
  `...StoppedWhenLastExitStatusIsZero`, `...FailedWhenLastExitStatusIsNonZero`,
  `TestSystemdStatusReportsActiveState`, `...FailedDistinctFromNotInstalled`,
  `...StoppedWhenInactive`) covering both platforms' running/stopped/failed/not-installed
  outcomes.

- [x] **Roll back `/cr_new`'s config entry when `Start` fails.** `cmdNew` wrote the new
  session into `b.cfg.Sessions`, saved the config, and marked it active *before* calling
  `runner.Start`. If `Start` failed, the handler only replied with the error — it never
  removed the session from `b.cfg.Sessions`/re-saved config, leaving a dead entry that
  `/cr_status`, `/cr_use`, and plain-text routing kept referencing. Rolled the config entry
  (and the active-session pointer, if set) back on a `Start` failure. Tested with a runner
  that fails `Start`: the session no longer appears in the saved config or as active
  afterward. (Commit `d03d9f1` — landed manually after ralph's own session left this
  verified-clean state uncommitted for 8+ stalled iterations; see tooling_ralph_loop.md.)

- [x] **Cap menu rows and add a text fallback.** `replyWithMenu` built one unbounded row
  holding every option `ParseMenu` found, so a menu with more options than Telegram allows
  per row came back rejected — and the rejection only reached `b.log.Error`, leaving the
  user staring at nothing while the session sat blocked on a choice. Moved the presentation
  out of `bridge.go` into `Menu` methods: `keyboardRows` chunks the options
  `menuButtonsPerRow` (3) at a time, `heading` owns the empty-question default, and
  `asPlainText` renders a numbered list closing with "Ответь номером варианта." so the user
  knows how to answer without buttons. `replyWithMenu` now warns and falls back to
  `b.reply(ctx, …)` with that text when the keyboard send fails; passing `ctx` rather than
  the spent `sendCtx` is deliberate, since `b.reply` derives its own full delivery timeout.
  The fake Telegram now records per-row button labels (`rowsOfLastKeyboard`) and can reject
  any send carrying `reply_markup` (`failEveryKeyboardSend`). Tested a seven-option model
  picker splitting 3/3/1 and a rejected keyboard arriving as text with every option in it.
  Also carries the two remaining `telegram.NewClient` callsites from `64b996b`, which
  overlapped this work.

- [x] **Wire `interim_notice_seconds` to something real.** Both production call sites of
  `WaitForSettle` (`ensureRunning`, `sendAndAwait`) passed a literal `nil` for the
  `onInterim` callback, even though `settle.go` fully implements and unit-tests the
  interim-notice mechanism — so the config knob did nothing and a user who asked a slow
  question saw only a typing indicator until `hard_cap_seconds` expired. Added
  `internal/bridge/interim.go` with two `InterimFunc` constructors, `noticeAnswerStillComing`
  (sendAndAwait) and `noticeSessionStillStarting` (ensureRunning); each replies through
  `b.reply` naming the elapsed time rounded to the second, and the cold-start one also names
  the session, since the two waits are indistinguishable to the user otherwise. The message
  text lives in package-level constants rather than inline, so the tests assert against the
  same strings the bridge sends. Tested both call sites with a harness config that cannot
  settle before the hard cap (`stable_rounds` 1000, `hard_cap_seconds` 1,
  `interim_notice_seconds` 1): each produces an interim message ahead of the real reply. A
  third test pins the negative case — a fast turn under the default config sends neither
  notice — so the callbacks can't start firing on every ordinary message. Both positive
  tests fail with `nil` restored at the call sites.

- [x] **Fix the tool-call-detection regex false positive.** `toolCallPattern`
  (`^[A-Z][A-Za-z]*\(`) matched genuine assistant prose shaped like "Filter(x) returns…" or
  "Update(id) done", not just actual tool-call lines, causing `ExtractAnswer` to drop real
  answer text and `FormatReply` to report "no text answer" when there was one. Real
  tool-call lines are always immediately followed by a `⎿` result line; prose never is.
  Added `followedByToolResult`, which `ExtractAnswer` now requires alongside the regex
  before counting a line as a tool call. Tested with a prose line shaped like a function
  call — it now survives into the extracted answer with zero tool blocks counted.

- [x] **Recognize commands sent with an `@botname` suffix.** `handleCommand`'s switch
  compares the first whitespace-separated field against exact literals like
  `"/cr_status"`; Telegram appends `@<botname>` to commands in group chats, so
  `/cr_status@mybot` fell through to "unknown command". Added `stripBotSuffix`, applied to
  the command token before the switch, cutting on the first `@`. Tested `/cr_status@somebot`
  against a running session: recognized, not reported as unknown.

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

- [x] **Make `elapsed` in `WaitForSettle` reflect real wall-clock time.** `elapsed` was
  incremented by the fixed `pollInterval` before `capture()` ran each round; `capture()`'s
  own duration (tmux's own timeout allows up to 15s) was never added, so `elapsed` drifted
  arbitrarily far behind the clock and both things derived from it — the `hard_cap_seconds`
  exit and the elapsed figure `interim_notice_seconds` reports to the user — were wrong by
  the same factor. With the shipped defaults (1.5s poll) and a tmux capture near its 15s
  timeout, the loop advanced `elapsed` by 1.5s per 16.5s of real time, so a 1200s hard cap
  would not trip for roughly 3.7 hours, and an interim notice claiming "2 minutes" would be
  sent 22 minutes in. `WaitForSettle` now stamps `startedAt` before the first capture and
  re-reads `time.Since(startedAt)` after each round's capture, so capture time counts
  against the cap and the notice reports true elapsed time. Tested with a capture that
  sleeps 30ms against a 2ms poll interval and a 1s hard cap: the call now returns in ~1s
  where the poll-interval sum predicted (and the old code took) 16.7s.

- [x] **Route `cmdService`'s output through the injected writer.** `cmdService`'s success
  paths called `fmt.Println` directly instead of using the `stdout` writer that `run()`
  threads through every other subcommand, so nothing a caller of `run()` could see proved
  the confirmation lines were ever printed — or, worse, that they were *not* printed after
  a failure. Threading `stdout` alone was not enough to test it: `cmdService` calls
  `os.Executable()` and `service.NewManager` before reaching the switch, so any test of the
  success paths would install a real launchd/systemd service. Split the resolution from the
  dispatch — `cmdService` still resolves the executable and builds the real manager, then
  delegates to `runService(args, mgr, stdout)`, which takes the manager through a new
  `serviceManager` interface declared at the call site like `bridge.Runner` and
  `service.CommandRunner`. All three success paths now write to the injected writer. Tested
  with a `fakeServiceManager` that records its calls: install, uninstall and status each
  write their line to the buffer (status still trimmed), a manager error on any of the three
  surfaces the error and writes *nothing* — the regression that would have let `uninstall`
  print "service uninstalled" over a still-running service — and a missing `-config` on
  install is rejected before `Install()` is ever reached. Coverage of
  `cmd/claude-remote` is 92.9%, up from 81.2%.

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
