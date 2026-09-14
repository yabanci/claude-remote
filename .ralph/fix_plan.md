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

## WIP audit findings (11.09.2026) — RESOLVED after 5 review rounds (14.09.2026)

A fresh audit of the concurrent-dispatch design (`bridge.go`'s `dispatch`, `state.go`'s
`sessionLocks`/`bindOwner`) found four real bugs the design introduced, none of which existed
before it — the old sequential `Run()` had neither. All four were *logical* races the Go race
detector cannot see (every individual field access was already correctly mutex-protected; the
bug was which lock was taken, or that nothing recovered a panic).

1. **Critical, security — bootstrap owner-binding TOCTOU.** Fixed: `bindOwner`
   (`state.go`) now re-checks `NeedsBootstrap()` inside the same lock that performs the write,
   so a second `bindOwner` call once already bound returns `errAlreadyBound` instead of silently
   overwriting the first owner. `bootstrapOwner` (`bridge.go`) distinguishes that case from a real
   save failure. Test: `TestBindOwnerRefusesASecondBindingOnceAlreadyBound` (`state_test.go`).

2. **Blocking — `inSessionTurn` locked the wrong session.** Fixed in two passes. First pass:
   `inSessionTurn` takes the lock name explicitly; `targetSessionFor` (`state.go`) resolves
   `/cr_restart`'s explicit argument (if any) before locking, instead of always locking the
   caller's own active session. Test: `TestCrRestartLocksTheExplicitTargetNotTheCallersActiveSession`
   (`sessionlock_test.go`), mutation-verified. **Correction (independent review, 12.09.2026):**
   that first pass was still incomplete — the resolved lock *name* and the session actually acted
   on could still diverge, because `forwardToSession`/`handleUpload` independently re-resolved
   `activeSessionName(chatID)` a second time *inside* the lock. If `/cr_use` changed the active
   session while a turn was queued waiting for that same lock, the turn would acquire the lock for
   the *old* session but act on the *new* one once it finally ran — reproduced by the reviewer.
   Second pass: the target session name is now resolved exactly once per turn, at
   `handleUpdate`/`handleCallback`, and threaded as a plain value through `handleMessage` →
   `forwardToSession`/`handleUpload` → `resolveSession`, which never re-derives it from
   `activeSessionName` when a name is already given. Test:
   `TestATurnsTargetSessionStaysFrozenEvenIfActiveSessionChangesWhileItWaits` (`state_test.go`).
   **Correction (independent review round 2, 12.09.2026):** that second pass was itself
   incomplete on two counts, both caught by a fresh independent reviewer, not by re-reading my
   own work.
   - The bug site: `handleCommand` (`commands.go`) was never updated to receive the frozen
     `target` — it still took the raw `arg` from the command text and passed it straight to
     `cmdRestart`/`cmdSend`, which called `resolveSession(chatID, arg)` or
     `resolveSession(chatID, "")`. A bare `/cr_restart` (no explicit session name) queued while
     the turn's lock was held for the then-active session could still act on a *different*
     session by the time it actually ran, if `/cr_use` switched the active session while it
     waited — the exact class of bug fix #2 exists to close, just reachable through a path fix
     #2's first cut didn't touch. Fixed by adding `target` as `handleCommand`'s second parameter,
     threaded from `handleUpdate`, and used by `cmdRestart`/`cmdSend` in place of `arg`/`""`.
     Test: `TestABareCrRestartActsOnTheTurnsFrozenTargetNotAConcurrentlySwitchedActiveSession`
     (`sessionlock_test.go`), mutation-verified against the actual `commands.go` fix (reverting
     `target` back to `arg` at the `/cr_restart` case reproduces `Kill:work`/`Start:work` instead
     of `main`, exactly as predicted).
   - The regression test: `TestATurnsTargetSessionStaysFrozenEvenIfActiveSessionChangesWhileItWaits`
     was proven inadequate — it called `targetSessionFor` then `resolveSession` directly and
     checked the result, which tests `resolveSession`'s own contract (holds even with the
     production bug fully present) rather than the actual bug site in
     `forwardToSession`/`handleUpload`/`handleCommand`. Reverting the real fix to `handleCommand`
     left the entire suite, this test included, green — "mutation-verified" was claimed for it
     without actually reverting the production change it was meant to guard. It stays in
     `state_test.go` as documentation of `resolveSession`'s contract but is not the regression
     guard; the black-box test above (through the real dispatch path, using only exported API)
     is. Lesson: mutation-verifying a fix means reverting the actual production code change and
     re-running the new test against that — not reverting a line in the test file itself.

3. **Blocking — `/cr_kill` + same-name auto-restart could race a still-polling `WaitForSettle`.**
   Fixed: `sessionGenerations` (`state.go`) — every successful `Start()` bumps a per-name counter;
   `watchVisible`'s `CaptureFunc` checks the generation on every poll and returns
   `errSessionReplaced` if it changed mid-turn, instead of transparently reading whatever is now
   running under that name. Tests: `TestWatchVisibleDetectsASessionReplacedMidPoll`,
   `TestWatchVisibleReadsThePaneWhenGenerationMatches` (`generation_test.go`), both
   mutation-verified. **Correction (independent review, 12.09.2026):** an earlier version of this
   note claimed fix #2 alone already made this scenario unreachable — that was wrong. Fix #2's
   *first* pass (locking on the target name) still let a turn's lock key and its actual resolved
   session diverge (see fix #2's "Correction" note below); the reviewer built a reproducer proving
   the generation check was catching a *real*, currently-live case, not a hypothetical one. Fix
   #2 was then completed to close that divergence too. Do not re-derive "is this still needed"
   from first principles without re-reading that history — this note has been wrong once already.

4. **High — unrecovered panic in a dispatched goroutine killed the whole process and the whole
   in-flight batch.** Fixed: `dispatch()` (`bridge.go`) recovers per goroutine, logs the panic,
   and replies with a generic error instead of crashing. Test:
   `TestAPanicInOneUpdateDoesNotCrashTheBridgeOrDropSiblingUpdates` (`panic_test.go`),
   mutation-verified (removing the `recover()` reproduces the crash the test is written against).

The harness-regression note in the previous version of this section (this branch's
`harness_test.go` lacking `ralph-batch2`'s three-part fix) was resolved during the merge that
folded `ralph-batch2` into this branch — the merged `harness_test.go` carries that fix.

Round 2 review's remaining (non-blocking) nitpicks and how each was resolved:
- `watchVisible` duplicated `capturePane`'s generation guard verbatim, even though every call
  site already has a `sessionRef` in hand. Fixed: `watchVisible(s sessionRef) CaptureFunc` now
  just delegates to `capturePane(s, visiblePaneOnly)`.
- `awaitStop`/`awaitSignal` were hardcoded to a 5s timeout while `waitUntil` had separately grown
  to a 10s budget (observed full-suite `-race` flakiness, not a logic bug — isolated runs always
  <0.1s). Fixed: both now share `testAwaitTimeout`/`testPollInterval` (`concurrency_test.go`) with
  `waitUntil`, so there is one budget to tune instead of three that can silently drift apart.
- `sessionRef`'s `generation` field zero-value ("loaded gun": a ref built before `ensureRunning`
  runs carries `generation: 0`, indistinguishable from a real generation-0 session). Traced every
  caller: `capturePane`/`heldBackByOpenDialog`/`sendAndAwait`/`deliverAnswer` are only ever
  reached from `forwardToSession`, which always overwrites `s` with `ensureRunning`'s return value
  first — `ensureRunning` unconditionally sets `s.generation` (via `generations.current` or
  `generations.bump`) before returning. No live bug today. Left as-is rather than restructuring
  `sessionRef` into a capture-only type: the current call graph makes it safe, and the
  restructuring cost outweighs a currently-unreachable risk. Documented here instead of as a code
  comment (house rule) so the next person touching this call graph knows the invariant they must
  not break: never call the capture helpers with a `sessionRef` that skipped `ensureRunning`.
- `deliverAnswer`'s four arguments and `panic_test.go`'s implicit goroutine-ordering assumption
  were both judged acceptable as-is by the reviewer (pattern matches pre-existing code; ordering
  survived stress testing) and left untouched.

Verified together (after round 2's fixes): `go build ./...`, `go vet ./...`, `gofmt -l .`,
`golangci-lint run ./...` (0 issues), `deadcode ./...` (clean), `go test ./... -race -count=2`
all green.

### Round 3 (independent review, 14.09.2026)

Round 2's own fix (`cmdRestart`/`handleCommand`) was confirmed genuinely solid this time — the
reviewer mutation-verified it independently rather than trusting the commit message. But it found
one new blocking bug of the *same class* the first two rounds found, plus a real test-coverage gap
in round 1's own fix, and several should-fix items.

**Blocking — `cmdNew` started and bumped a session without holding that session's turn lock.**
`targetSessionFor` only special-cased `/cr_restart`; a `/cr_new work <dir>` command's turn locked
on the caller's *then-active* session (say `main`), not on `work` — the session `cmdNew` was about
to create and start. A message dispatched moments later that also resolved to `work` (e.g. because
`addSession` had already flipped the chat's active pointer to `work` before `runner.Start("work")`
even ran) could race `cmdNew`'s own `Start`/`generations.bump`, risking a duplicate
`tmux new-session`, a spurious rollback that deletes a session another goroutine just typed into,
or a bogus "session was replaced" error. Fixed the same way `/cr_restart` was fixed: extended
`targetSessionFor` to special-case `/cr_new <name> ...` too, returning the new session's own name
as the lock target — so `cmdNew` now runs under `work`'s own lock, and any concurrently-dispatched
message that also resolves to `work` correctly queues behind it instead of racing it. This also
removes an incidental, unrelated serialization: `/cr_new` no longer waits for an unrelated session's
in-flight turn to finish before it can even start creating the new one — which is exactly the kind
of stuck-session problem this whole redesign exists to fix. Test:
`TestABareCrNewCreatesItsOwnSessionWithoutWaitingOnAnUnrelatedActiveSessionsTurn`
(`sessionlock_test.go`), mutation-verified (reverting the `/cr_new` case reproduces the exact
symptom: the command hangs behind the unrelated session's lock and times out).

**Should-fix, now fixed — round 1's `forwardToSession`/`handleUpload`/`cmdSend` frozen-target fix
had zero regression coverage.** Only `cmdRestart` had a black-box guard (from round 2). Mutation-
verified this was a real gap: reverting each of the three original `resolveSession(chatID, target)`
call sites back to `resolveSession(chatID, "")` left the *entire* suite green. Added three black-box
tests through the real dispatch path — `TestAQueuedPlainMessageStaysOnItsFrozenTargetEvenIf...`,
`TestAQueuedCrSendStaysOnItsFrozenTargetEvenIf...`, `TestAQueuedUploadStaysOnItsFrozenTargetEvenIf...`
(all in `sessionlock_test.go`) — each mutation-verified individually against its own call site.

**Should-fix, now fixed — command text was parsed twice, independently, which is structurally why
this bug class kept recurring.** `targetSessionFor` (lock-target resolution) and `handleCommand`
(dispatch) each re-implemented "split off the command name, strip the bot `@suffix`, trim the arg"
with their own `strings.Cut`/`SplitN` calls. Two independent parsers agreeing on every case forever
is not a property anything enforced. Extracted one `parseCommand(text) (name, arg string, ok bool)`
used by `runsWhileSessionIsBusy`, `targetSessionFor`, and `handleCommand` alike; also fixed a related
nitpick this surfaced (`handleMessage`'s own `/cr_` routing check used an *untrimmed* prefix check,
so a leading-space command like `" /cr_restart"` would be sent into the Claude session as raw text
instead of executing — now routed through the same shared, trimming `isCrCommand` helper).

**Should-fix, now fixed — commands that touch no session still serialized behind the busy session's
turn lock.** `/cr_help`, `/cr_sessions`, and `/cr_use` never touch the Runner at all (the first two
are read-only, `cmdUse` only flips an in-memory pointer under `b.state`) but were routed through
`inSessionTurn`, so a user could not switch away from a stuck session without waiting for it —
undermining the point of this whole redesign for one of its most common cases. Added all three to
`lockFreeCommands`. This changed the interleaving `TestABareCrRestartActsOnTheTurnsFrozenTarget...`
relies on to engineer its race (that test depended on `/cr_use` itself being lock-bound); reordered
the test's message sequence (`/cr_restart` enqueued before `/cr_use` instead of after) to keep
exercising the same frozen-target invariant now that `/cr_use` no longer queues.

**Should-fix, deliberately deferred — updates from one `getUpdates` batch dispatch into unordered
goroutines, so two messages to the same session can theoretically reach it out of order.** The old
serial `Run()` could not do this; `sync.Mutex` is not FIFO for goroutines that have not yet started
waiting. The reviewer could not reproduce it (the test harness hands out one update per poll,
which inserts a real HTTP round-trip between siblings and always gives the first a head start) and
a correct fix (a ticket/FIFO lock keyed by session name, with target resolution moved earlier so
tickets are drawn in true arrival order) is an architectural change, not a mechanical one — exactly
the kind of change this pass's own ground rules reserve for an explicit decision rather than a
drive-by fix in an area that has already had three rounds of bugs found. Left for a follow-up.

Nitpicks not addressed (reviewer judged pre-existing/harmless, or a real fix independently found
during this pass): `sessionLocks.of` never evicts a name (owner-only, unbounded but slow-growing);
`cmdKill` not bumping the generation (asymmetric with `Start` but already handled correctly via the
`Exists` check); `showTyping`'s goroutine not tracked by `inflight` (pre-existing, harmless).

Also found, unrelated to this audit and NOT fixed here (documented instead, see
[[claude_remote_gotchas]] #36): `TestCrSendFollowsSymlinksThatStayInsideSessionDir` flakes on
"context canceled" roughly 1 in 10-15 isolated runs, reproduced on the already-committed round-2
HEAD too. Root cause is a test-harness race in `fakeTelegram`'s `sendDocument` fake (it records the
document server-side before the client-side call returns, so the test's "wait for reply, then
cancel the bridge" pattern can cancel a call the fake already considered successful) — not a bridge
dispatch/locking bug, out of scope for this pass.

Verified together (after round 3's fixes): `go build ./...`, `go vet ./...`, `gofmt -l .`,
`golangci-lint run ./...` (0 issues), `deadcode ./...` (clean), `go test ./... -race -count=2`
all green (aside from gotcha #36's known pre-existing flake, unrelated to this round).

### Round 4 (independent review, 14.09.2026) — no new bug of the recurring class

For the first time, the reviewer independently enumerated every caller of `targetSessionFor`,
`resolveSession`, `activeSessionName`, and `inSessionTurn` (fifteen call sites) and confirmed the
"lock key == acted-on session" invariant now holds everywhere a lock is actually taken; the
lock-free commands correctly hold no lock by design. Ship verdict: ready, no blocking bug. It did
find two mutation-proven should-fix items and one plausible-but-unproven one, all now fixed:

**Fixed — `cmdSend`'s `SendDocument` call used the raw cancellable handler `ctx` instead of the
`WithoutCancel`+`replyDeliveryTimeout` wrapper every other reply path uses.** This was not just a
test-only concern: a real shutdown during a `/cr_send` upload would abort it while every other
reply survives for `replyDeliveryTimeout` — `cmdSend` had silently opted out of protection added
deliberately in an earlier commit. It also turned out to be the actual root cause of gotcha #36's
flake (see below), not merely triggered by it. Extracted `deliveryContext(ctx)` (previously
`reply`/`replyWithMenu` each inlined the same two-line wrap) and used it in all three places.
Mutation-verified: unpatched, 3 failures in 80 isolated runs of the affected test; patched, 0
failures in 80 runs (60 by the reviewer, 20 confirming independently).

**Fixed — the `cmdNew` regression test added in round 3 didn't test what its assertion message
claimed.** It proved `/cr_new` doesn't act on the caller's unrelated active session, but not that
it holds the *new* session's own lock throughout — the reviewer mutation-proved this by removing
the `/cr_new` case from `targetSessionFor` **and** simultaneously adding `/cr_new` to
`lockFreeCommands` (i.e. reintroducing round 3's exact blocking bug in its "no lock taken at all"
form): the round-3 test still passed, 3/3, whole suite green. Added
`TestCrNewHoldsTheNewSessionsOwnLockWhileStartingIt`, which stalls a runner inside `Start("work")`
and asserts a plain message dispatched meanwhile cannot reach `work`'s pane until the stall is
released. First draft of this test was itself subtly wrong — the queued plain message resolved to
the not-yet-existing "work" session and called `Start("work")` too via `ensureRunning`'s
not-exists branch, coincidentally colliding with the same stall point regardless of locking, so it
passed even under the exact mutation it was meant to catch. Fixed by pre-seeding "work" as already
running in the fake runner before the test starts, so only `cmdNew`'s own `Start` call is stalled
and the queued message's `ensureRunning` takes its already-exists fast path instead. Re-verified
against the same combined mutation: now fails with the intended message; passes on the real fix.

**Fixed (defense in depth, not proven live) — `cmdUse`'s `hasSession`+`setActiveSession` were two
separate `b.state` critical sections.** The reviewer could not construct the interleaving through
the public API (no injectable seam between the two calls) and reported it as a hypothesis, not a
demonstrated failure. Collapsed into one atomic `useSession(chatID, name) bool` regardless, since
the fix is three lines and removes the question entirely rather than leaving it as a standing "is
this actually fine" doubt for the next person to re-derive.

**Correction to the deferred message-ordering item (not a new finding — fixing the description of
the one from round 3):** the "test harness gives the first goroutine a head start" explanation is
right but incomplete — `Run()` never waits for a dispatched goroutine before fetching the next
batch, so the same reordering risk spans consecutive `getUpdates` polls, not just one batch; only
the httptest round-trip latency (absent in real production dispatch) is what currently prevents
reproduction. The blast radius is also wider than "two messages to the same session out of order":
`/cr_use` followed by `/cr_kill` are both lock-free with nothing serializing them, and `cmdKill`
resolves at execution time, so a fast-enough `/cr_kill` could kill whatever was active *before* a
concurrently-processed `/cr_use` took effect — destructive, and something the old serial `Run()`
could not do. Still deferred (architectural, not mechanical — a FIFO lock with target resolution
moved earlier), but a cheap partial mitigation was named for a future pass: apply `/cr_use`'s
active-pointer write inline on the `Run` goroutine before dispatching (no I/O involved), so it is
strictly ordered relative to every later update's own resolution.

**Correction to gotcha #36 (`claude_remote_gotchas.md`):** round 3's diagnosis ("a test-harness
race, not a bridge logic bug") was half right. The harness recording the document server-side
before the client call returns is real and is the *trigger*, but `cmdSend`'s use of the raw
cancellable `ctx` is why that race was ever observable, and it is a genuine (if narrow) production
defect independent of any test. Fixed above; gotcha entry updated to reflect the corrected,
complete root cause and mark it resolved.

Nitpicks fixed: `handleCommand` discarded `parseCommand`'s `ok` (an unreachable-today "неизвестная
команда , см. /cr_help" with an empty name if that branch were ever reached — now replies plainly
and returns instead); `targetSessionFor`'s redundant `if ok { switch … }` simplified (when `!ok`,
`name` is `""` and matches no case, so the wrapper decided nothing); one bare `time.Sleep` inconsistent
with the file's own house-style `giveQueuedTurnTimeToStartWaitingOnTheLock` helper renamed to match;
the `/cr_send` regression test's assertion improved so a regression fails at an informative
message instead of the generic `waitUntil` timeout (it was asserting on the wrong signal — the
fix required distinguishing `/cr_use`'s own legitimate reply from `cmdSend`'s error text, not just
checking "any message appeared").

Nitpicks not fixed, judged acceptable or out of scope by the reviewer: `sessionLocks.of`'s
already-documented unbounded growth (further widened by `/cr_new`, still owner-only and
slow-growing); `bootstrapOwner`'s silent message drop on a lost bootstrap race (fail-closed and
therefore acceptable, a reply would just be kinder); `deliverAnswer`'s 5-argument count (accepted
in round 2, still true); `/cr_send` still not lock-free like `/cr_help`/`/cr_sessions`/`/cr_use`
(cannot be done safely until `handleCommand`'s lock-free path threads a real `target` instead of
hardcoding `""`, itself only a latent risk today since no lock-free command reads `target`) —
left for a future pass, not blocking.

Verified together (after round 4's fixes): `go build ./...`, `go vet ./...`, `gofmt -l .`,
`golangci-lint run ./...` (0 issues), `deadcode ./...` (clean), `go test ./... -race -count=2`
all green, including 80/80 isolated runs of the previously-flaky `/cr_send` symlink test (gotcha
#36 is now resolved, not just documented).

### Round 5 (scoped confirmation review, 14.09.2026) — clean

Round 4 was a full re-audit and came back clean of the recurring bug class; its own fix (commit
`c7b055d`, closing round 4's should-fix items) had not itself been independently checked, so round
5 reviewed *that specific diff* rather than re-auditing the whole design again — proportionate to
what had actually changed since the last clean full audit. Verdict: ready, no blocking, no
should-fix. It mutation-verified all three of round 4's fix claims independently (including
reproducing the `cmdSend` context bug under CPU contention when an idle-machine run had shown
nothing, and correctly not reporting a false negative) and found three cosmetic nitpicks, fixed in
`1aec8fc`: `deliveryContext` had an unused receiver (made a package-level function); the new
`cmdNew` test's pre-seed step silently bypassed its own stall via a raw embedded-field selector
(named `seedAlreadyRunning` instead); the same test's closing wait polled a value the failure path
itself resets, occasionally burning a 10s generic timeout instead of failing at the intended
assertion (switched to polling `lastSentKeys()`, which the reset never touches). Re-verified the
round-4 combined mutation against the fixed test: 15/15 runs now fail in ~0.5s, none hit the 10s
timeout.

### Summary — five rounds, three real bugs, now clean

1. Fix (`42257d6`): the four originally-audited bugs (bootstrap TOCTOU, wrong lock key, `/cr_kill`
   restart generation race, unrecovered panic).
2. Round 1 review → fix (`6799890`): `forwardToSession`/`handleUpload` re-derived the active
   session a second time after locking.
3. Round 2 review → fix (`57a55b8`): `handleCommand` (feeding `cmdRestart`/`cmdSend`) had the same
   bug, missed by round 1.
4. Round 3 review → fix (`bf594b4`): `cmdNew` started/bumped a session without holding *that*
   session's own lock — same bug class, different command; plus closed a real test-coverage gap at
   the three sites round 1 fixed.
5. Round 4 review → fix (`c7b055d`): first clean verdict on the recurring class (independently
   re-enumerated all 15 relevant call sites); found and fixed a real `cmdSend` production defect
   (missing shutdown-safe delivery context — also the true root cause of gotcha #36's flake, not
   merely its trigger) plus a round-3 test that didn't test its own claim.
6. Round 5 review → fix (`1aec8fc`): confirmed round 4's fixes are correct; three cosmetic
   nitpicks, no behavior change.

The "lock key == acted-on session" invariant now holds at every call site that takes a lock, by
construction (`targetSessionFor` has exactly one caller, `inSessionTurn` exactly two, both closing
over the same resolved value used for the actual action). Deliberately NOT fixed, documented as an
explicit open architectural question rather than a bug: message-ordering across dispatch goroutines
from consecutive `getUpdates` polls (not just one batch) is not FIFO, with a demonstrated-plausible
blast radius including `/cr_use` racing `/cr_kill` — see round 3's and round 4's notes above for
the corrected description and a named cheap partial mitigation. This needs an explicit decision on
priority/approach (a FIFO lock is an architectural change, not a mechanical one), not a drive-by
fix, before it's touched.

## Tasks

- [ ] **Diagnose and fix `TestLiveSecondTurnDoesNotRepeatTheFirst` (skipped 14.09.2026, not
  deleted — revisit).** Fails identically on both `ubuntu-latest` and `macos-latest`: the
  second turn's reply contains the whole pane (both turns) instead of just its own diff. Five
  fix attempts, each ruling something out — full history in
  `references/projects/claude_remote_gotchas.md` #37 and PR #18's commits `77126a5`..`d99deae`.
  Two real bugs WERE found and fixed along the way (kept, verified, benefit real usage too):
  `DiffTail` didn't tolerate differing trailing-blank-line padding between two `capture-pane`
  snapshots (`settle.go`'s `trimTrailingBlankLines`), and `TailAfterPrompt` couldn't recover an
  echo wrapped across two pane lines with no `>`/`❯` marker (`tail.go`'s `tailAfterWrappedEcho`).
  What's still unexplained: even after making the e2e harness's shell prompt short,
  deterministic, and confirmed NOT wrapping (`e2e_test.go`'s `deterministicShellCommand`), the
  SAME symptom persists — the cold-start command's own echo appears duplicated (once bare, once
  after the ambient prompt) in the captured pane, on both platforms, regardless of command
  length. This rules out wrapping as the cause of this specific remaining symptom. Leading
  hypothesis, not yet verified: `WaitForSettle`'s very first stability-check capture (right
  after `ColdStartDelay`) may be landing in the narrow window after the cold-start command was
  typed but before its Enter keypress was actually processed by the shell — a state that looks
  "stable" (nothing changing yet) but isn't the real settle point. Bumping `ColdStartDelayMS`
  from 1000 to 2500 did NOT fix it, which weakens but doesn't rule out this hypothesis (the
  extra delay is before the exec even runs if Enter itself is what's slow to register, not the
  bash startup after it). Next step if picked back up: needs actual tmux access to observe the
  cold-start sequence capture-by-capture (this session's sandbox has none — see gotcha #32) —
  add temporary diagnostic logging to `ensureRunning`/`WaitForSettle` dumping each poll's raw
  capture, run locally with real tmux, read what actually happens frame-by-frame instead of
  inferring from CI's final-state-only failure output.

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

- [x] **Cover `ensureRunning`'s cold-start failure branches.** No test exercised `Start()`
  failing or the post-start `WaitForSettle()` failing — every fast test pre-seeds the
  session via `startSession`, so `runner.Exists()` was always true and the whole cold-start
  path was dead in the suite. The existing `TestCrRestartReportsAFailedStart` only covers
  `cmdRestart`'s own `Start` call, not this one. Added `coldstart_test.go` with two tests
  that send ordinary text (not a command) to a session that is not running:
  `TestAMessageToAStoppedSessionReportsAFailedColdStart` uses `failingRunner` with a
  `startErr`, and `TestAMessageToAStoppedSessionReportsAnUnreadableScreenAfterStart` lets
  `Start` succeed but sets `captureErr`, which is the only way `WaitForSettle` returns an
  error (the hard cap returns the last capture with a nil error, so there is no timeout
  branch to cover). Each asserts the specific reply — `не удалось запустить сессию` vs
  `не удалось дождаться запуска сессии`, which is distinct from `reportCaptureFailure`'s
  text, so the two branches can't be confused — and that nothing was typed into the
  session. Verified by mutation: stubbing out both error branches in `ensureRunning` makes
  both tests fail, the first because the message is forwarded into a session that never
  started.

- [x] **Make the `/cr_use` test verify actual routing, not just the reply text.**
  `TestCrUseSwitchesActiveSession` only asserts the confirmation text contains the session
  name; it never sends a follow-up message and checks which session's fake runner
  received it. Extend it to send a message after switching and assert it reached the
  newly-active session's runner, not the default one.

- [x] **Cover `handleUpload`'s `GetFile`/`DownloadFile` failure branches.** The fake
  Telegram test double always returns success for `getFile` and the file-download
  endpoint; no test makes either fail. Add cases where the fake returns an error/failure
  for each, asserting the user gets a sensible reply rather than a swallowed or malformed
  error.

- [x] **Cover `offsetStore.load()`'s corrupted-file fallback.** No test exercises the
  branch where `offset.txt` exists but contains non-numeric garbage (the `strconv.ParseInt`
  failure path). Add one, asserting the warning is logged (see the offset-read-error task
  above) and the store falls back to offset 0.

- [x] **Cover `bootstrapOwner`'s `config.Save` failure branch.** This is the fail-closed
  path that refuses to bind bridge ownership to the first sender when the binding can't be
  persisted, and nothing in the module exercised it — nor `config.Save`'s own `MkdirAll`/
  write error branches. The branch is the whole of the bridge's trust-on-first-use
  security: an unconfigured bridge binds to whoever writes first, and if that binding
  cannot be persisted it must refuse rather than serve an unverified stranger for the rest
  of the process's life. Added `internal/bridge/bootstrap_test.go` with
  `TestAnUnsavableConfigLeavesTheBridgeUnbound`: an empty `AllowedUsers`, a config path
  occupied by a non-empty directory (so `atomicfile.Write`'s rename cannot succeed), and
  *two* messages from the same stranger — asserting the refusal both times, because the
  bug worth pinning is not the first reply but the second, where a bridge that bound itself
  in memory despite the failed save would treat the sender as the owner. Also asserts
  nothing was typed into the session. Added two `config.Save` tests pinning that the two
  failure modes stay distinguishable to the caller: a parent path that is a file yields
  `create config dir`, a target path that is a non-empty directory yields `write config`
  naming the file. Verified by mutation: dropping the `return false` (keeping only the log
  line) fails the test three ways, the damning one being `покажи содержимое .env` reaching
  the live session's keystrokes.

- [x] **De-duplicate repeated error strings.** `"сессия %q не запущена"` was written out
  identically in `cmdKill`, `cmdInterrupt` and `cmdPeek`, and `"не удалось остановить: %v"`
  in `cmdKill` and `cmdRestart` — five hand-copied literals that nothing kept in sync, so
  rewording one command's message silently left the others saying something different for
  the same condition. Extracted them as package-level constants next to `validSessionName`
  in `commands.go` (`sessionNotRunningNotice`, `stopFailedNotice`), following the format-
  string-constant convention `interim.go` already uses. `"не удалось прочитать экран
  сессии: %v"` was checked as the task asked: it is down to a single occurrence in
  `reportCaptureFailure`, the `/cr_peek` task having removed the copy, so it stays inline.
  Tests are in a new `messages_test.go` and assert the property the constants buy rather
  than the text: `/cr_kill`, `/cr_interrupt` and `/cr_peek` must produce a byte-identical
  message for a stopped session, and `/cr_kill` and `/cr_restart` the same for a failed
  stop. Both fail if any one call site is reworded on its own (verified by diverging
  `cmdPeek`'s and `cmdRestart`'s text and watching each test fail).

- [x] **Name the `/rc` chrome filter in `clean.go`.** `isChrome()` matches the exact
  literal `"/rc"` with nothing in the source explaining what it strips or why — it's the
  tail of the Claude Code CLI's statusline. Give it a named constant with a name that says
  what it is, so a future reader doesn't have to reverse-engineer it from test fixtures.

## Round 3 — findings from the 11.09.2026 full re-audit

- [x] **Fix `DiffTail`'s scrollback-eviction detector — it false-positives once the pane
  history fills up, not just on genuine eviction.** `internal/bridge/tail.go`
  (`scrollbackLikelyEvicted`) compares `before`/`after` by common *prefix* length. Once a
  session's tmux history hits its 5000-line cap (`tmux.HistoryLimit`), every single new line
  of output shifts the whole buffer by one — `after[0]` becomes `before[1]`, etc. — so
  `beforeLines[0] != afterLines[0]` on the very first comparison and `common` reads 0 even
  though 4999/5000 lines are identical. The detector then reports "evicted" and returns the
  static "ответ недоступен, экран прокрутился слишком далеко" fallback instead of the real
  reply — for essentially every turn that reaches `DiffTail` (i.e. whenever
  `TailAfterPrompt` can't find the echoed prompt line) once any long-lived session's
  scrollback has filled once. The existing test (`settle_test.go`) only exercises two
  buffers with completely different content, which never exercises the single-line-shift
  case. Fix: compare content, not raw prefix position — e.g. find `before`'s tail as a
  substring/suffix-overlap of `after` rather than requiring index-0 alignment, so a
  one-line (or N-line) shift is recognized as "mostly the same" instead of "fully evicted."
  Test with two captures that are the same 5000-line buffer shifted by exactly one line.

- [x] **Make `toolCallPattern` recognize MCP-qualified tool-call lines.**
  `internal/bridge/answer.go`'s `toolCallPattern` (`^[A-Z][A-Za-z]*\(`) requires an
  uppercase first letter and only letters before the paren. MCP tool names are formatted
  `mcp__<server>__<tool>(...)` — lowercase, with underscores — so a line invoking an MCP
  tool never matches, and instead of being stripped the way `Bash(...)`/`Read(...)` are, it
  leaks into the extracted answer as if it were prose. Broaden the pattern (or add a second
  one) to also match the `mcp__` prefix shape. Test with a fixture line shaped like a real
  MCP tool call, asserting it's excluded from the extracted answer the same way built-in
  tool calls are.

- [x] **Make `systemd.status()` handle transitional unit states, not just
  active/failed/inactive.** `internal/service/platform.go`'s `systemd.status()` (rewritten
  in `037ae0d` specifically to stop collapsing real states into "not installed") only
  special-cases `"failed"` and `"inactive"`; any other real `systemctl is-active` output
  (`"activating"`, `"deactivating"`, `"reloading"` — all reachable mid-restart, which the
  unit's own `Restart=on-failure` triggers routinely) falls through to `statusNotInstalled`.
  During a crash loop, `claude-remote service status` reports "not installed" for a service
  that is very much installed — reproducing, for the states this exact fix was meant to
  cover, the bug it was written to close. Test with `statusOutput` set to `"activating"`
  and `"deactivating"`, asserting neither reports not-installed.

- [x] **Tell the user honestly when `/cr_new`'s rollback itself fails to save.**
  `internal/bridge/commands.go`'s `rollbackNewSession` calls `config.Save` a second time (to
  remove the just-added entry) and only logs if that second save fails — but `cmdNew`
  unconditionally replies "откатываю конфиг" regardless of whether the rollback's own save
  actually succeeded. A transient disk/permission failure between the two saves leaves
  `config.yaml` on disk with a dangling session entry pointing at a session that was never
  started, while the user is told it was cleaned up; a restart reloads the stale entry —
  exactly the bug class `d03d9f1` (the original rollback fix) was written to eliminate,
  reintroduced whenever the rollback's own persistence fails. Test by making the *second*
  `config.Save` call fail while the first (creating the entry) succeeds, asserting the
  user's reply reflects the failure and doesn't claim a clean rollback.

- [x] **Give `WaitForSettle` a `context.Context` so shutdown isn't blocked on it for up to
  `hard_cap_seconds`.** Neither `internal/bridge/settle.go`'s `WaitForSettle` nor
  `internal/telegram/client.go`'s retry loop in `call()` checked `ctx.Err()` — the retry
  loop's `c.sleep(wait)` ran to completion regardless of cancellation, and `WaitForSettle`'s
  only exit conditions were `stableRounds` reached or the hard cap elapsing (default 1200s).
  Combined with `main.go`'s `signal.NotifyContext` on SIGTERM, a shutdown signal arriving
  during a busy turn could not be honored until the turn settled or 20 minutes passed, and
  the process manager's stop-timeout would SIGKILL first, losing the in-flight reply with no
  log trail. `WaitForSettle` now takes a `ctx` as its first parameter and its poll sleep is a
  `select` on `ctx.Done()` vs a timer, returning the last capture plus `ctx.Err()`. The
  client's retry wait moved behind a `Sleeper` (`func(context.Context, time.Duration) error`)
  defaulting to the same ctx-aware sleep; on cancellation it returns
  `errors.Join(lastErr, ctx.Err())` so the 429 that triggered the retry stays readable next
  to the abort reason, and no further HTTP request is sent. `WithRetryPolicy` was replaced by
  `WithMaxRetries` — production code never had a reason to inject a sleep function, so the
  test-only seam lives in `internal/telegram/export_test.go` as `WithSleeper` and is not part
  of the package's public API. Tested both sides: a context cancelled after 50ms aborts a
  1000-round settle well inside half the hard cap, and aborts a `retry_after: 60` wait in
  under 5s with exactly one attempt made.

- [x] **Fix `/cr_send`'s containment check to survive a symlink inside the session
  directory.** `internal/bridge/commands.go`'s `resolveSendPath` (from `330a8b7`, the
  original path-traversal fix) was lexical-only — `filepath.Clean`/`filepath.Rel`, no
  `filepath.EvalSymlinks`. A symlink placed inside the session directory that pointed
  outside it (`<session dir>/data -> ~/.ssh`) produced a relative path containing no `..`,
  passed the check, and `SendDocument` followed it when opening the file, uploading whatever
  it pointed at. Owner-only reachable, but the command's description promises the session
  directory is a hard boundary. `resolveSendPath` now keeps the lexical `filepath.Rel` test
  as a cheap first gate (extracted into `isInside`, now used twice) and then re-runs it on
  the `filepath.EvalSymlinks`-resolved base and target, returning the resolved target so the
  file that gets opened is the one that was checked. A target that does not exist
  (`fs.ErrNotExist` from `EvalSymlinks`) still returns the lexical path, so a typo keeps
  producing the plain "file not found" reply instead of a misleading containment error.
  Tested three ways: a directory symlink pointing outside (`data -> outside`), a file
  symlink pointing outside (`key.txt -> outside/id_rsa`), and a symlink that stays inside
  (`latest -> out`), which must still be followed and delivered.

- [x] **Escape `%` in the generated systemd unit, not just `\` and `"`.**
  `internal/service/platform.go`'s `systemdEscaper` (from `ea6cb4e`) escaped backslash and
  double-quote but not `%`, and systemd expands `%h`/`%n`/`%i`-style specifiers in
  `Environment=`/`ExecStart=` lines. An exec path or `$PATH` value holding a literal
  `%`-specifier sequence was silently mis-expanded by systemd at unit-start time: a binary
  installed under `/opt/%h/` got an `ExecStart` pointing at the user's home directory
  instead, and the service failed to start for a reason the unit file does not show.
  Added `%` -> `%%` to the existing `strings.NewReplacer`; the replacements do not cascade,
  so the added pair cannot double-escape the other two, and the escaped text is a
  `fmt.Sprintf` *argument* rather than part of the format string, so `%%` reaches the unit
  file literally. Tested with `%h` in the exec path and `%n` inside `PATH`.

- [x] **Make `sendAwaiting` append to `h.tg.updates` like `deliver`/`deliverCallback` do,
  not replace it.** `internal/bridge/harness_test.go`'s `sendAwaiting` still does
  `h.tg.updates = []telegram.Update{...}` — the exact pattern `deliver`/`deliverCallback`
  were fixed away from in `207cb0d`. It doesn't fire today only because every current call
  site uses it as the sole/first delivery on a fresh harness, but it's a live landmine for
  the next test that calls `send()`/`deliver()` before `sendAwaiting()` on the same harness:
  the second call's queued update would silently never be delivered, the same failure mode
  `207cb0d` fixed everywhere else. Fix it the same way: append with a computed `UpdateID`
  instead of replacing the slice.
  Extracted the queueing all three helpers shared into `fakeTelegram.enqueue`, which assigns
  the `UpdateID` and appends under `f.mu` — the same mutex `handle`'s `getUpdates` branch
  already takes to read `updates`/`nextCall`, so the append no longer races the httptest
  server goroutine either. `send` now builds its message through a new `userMessage` helper
  that `sendAwaiting` reuses. Guarded by
  `TestALongTurnIsAnnouncedEvenWhenItIsNotTheFirstUpdateOfTheChat`: it sends `/cr_help`
  first, so `nextCall` is already 1 when `sendAwaiting` runs; with the old slice
  replacement the queued update is never handed out and the test times out.
