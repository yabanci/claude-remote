package bridge_test

import (
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yabanci/claude-remote/internal/config"
	"github.com/yabanci/claude-remote/internal/telegram"
)

type callLoggingRunner struct {
	*fakeRunner
	holdText string
	entered  chan struct{}
	release  chan struct{}
	once     sync.Once

	mu    sync.Mutex
	calls []string
}

func newCallLoggingRunner(holdText string) *callLoggingRunner {
	return &callLoggingRunner{
		fakeRunner: newFakeRunner(),
		holdText:   holdText,
		entered:    make(chan struct{}),
		release:    make(chan struct{}),
	}
}

func (r *callLoggingRunner) resetLog() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = nil
}

func (r *callLoggingRunner) callLog() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.calls...)
}

func (r *callLoggingRunner) logCall(entry string) {
	r.mu.Lock()
	r.calls = append(r.calls, entry)
	r.mu.Unlock()
}

func (r *callLoggingRunner) SendKeys(session, text string) error {
	r.logCall("SendKeys:" + text)
	if text == r.holdText {
		r.once.Do(func() { close(r.entered) })
		<-r.release
	}
	return r.fakeRunner.SendKeys(session, text)
}

func (r *callLoggingRunner) Kill(session string) error {
	r.logCall("Kill:" + session)
	return r.fakeRunner.Kill(session)
}

func (r *callLoggingRunner) Start(session, dir, command string) error {
	r.logCall("Start:" + session)
	return r.fakeRunner.Start(session, dir, command)
}

func giveQueuedTurnTimeToStartWaitingOnTheLock(d time.Duration) {
	time.Sleep(d)
}

func textUpdateFromChat(id, chatID int64, text string) telegram.Update {
	return telegram.Update{UpdateID: id, Message: &telegram.Message{
		MessageID: id,
		Chat:      telegram.Chat{ID: chatID},
		From:      &telegram.User{ID: testUserID},
		Text:      text,
	}}
}

func TestCrRestartLocksTheExplicitTargetNotTheCallersActiveSession(t *testing.T) {
	const chatA, chatB int64 = 1, 2
	cfg := testConfigFor(t)
	cfg.Sessions["work"] = config.SessionConfig{Dir: t.TempDir(), Command: "claude"}
	runner := newCallLoggingRunner("long question")
	require.NoError(t, runner.Start("main", cfg.Sessions["main"].Dir, "claude"))
	require.NoError(t, runner.Start("work", cfg.Sessions["work"].Dir, "claude"))
	runner.resetLog()

	h := newHarnessWithRunner(t, cfg, runner)
	h.tg.updates = []telegram.Update{textUpdateFromChat(1, chatB, "/cr_use work")}

	cancel, done := h.runInBackground()
	waitUntil(t, func() bool {
		return strings.Contains(strings.Join(h.tg.messages(), "\n"), "активная сессия: work")
	})

	h.tg.enqueue(textUpdateFromChat(2, chatB, "long question"))
	awaitSignal(t, runner.entered, "the long question to reach session \"work\"")

	h.tg.enqueue(textUpdateFromChat(3, chatA, "/cr_restart work"))
	time.Sleep(stallGracePeriod)

	assert.NotContains(t, runner.callLog(), "Kill:work",
		"/cr_restart work from another chat must wait for chatB's in-flight turn on \"work\", "+
			"not run immediately just because chatA's own active session is \"main\"")

	close(runner.release)
	waitUntil(t, func() bool {
		return strings.Contains(strings.Join(h.tg.messages(), "\n"), "перезапущена: work")
	})

	log := runner.callLog()
	sendIdx := slices.Index(log, "SendKeys:long question")
	killIdx := slices.Index(log, "Kill:work")
	require.GreaterOrEqual(t, sendIdx, 0)
	require.GreaterOrEqual(t, killIdx, 0)
	assert.Less(t, sendIdx, killIdx,
		"the stalled turn's SendKeys must fully complete before /cr_restart's Kill runs")

	cancel()
	h.awaitStop(done)
}

func TestABareCrRestartActsOnTheTurnsFrozenTargetNotAConcurrentlySwitchedActiveSession(t *testing.T) {
	const chatID int64 = 1
	cfg := testConfigFor(t)
	cfg.Sessions["work"] = config.SessionConfig{Dir: t.TempDir(), Command: "claude"}
	runner := newCallLoggingRunner("hold main")
	require.NoError(t, runner.Start("main", cfg.Sessions["main"].Dir, "claude"))
	require.NoError(t, runner.Start("work", cfg.Sessions["work"].Dir, "claude"))
	runner.resetLog()

	h := newHarnessWithRunner(t, cfg, runner)
	h.tg.updates = []telegram.Update{textUpdateFromChat(1, chatID, "hold main")}

	cancel, done := h.runInBackground()
	awaitSignal(t, runner.entered, "the long turn to reach SendKeys on \"main\"")

	h.tg.enqueue(textUpdateFromChat(2, chatID, "/cr_use work"))
	giveQueuedTurnTimeToStartWaitingOnTheLock(stallGracePeriod)
	h.tg.enqueue(textUpdateFromChat(3, chatID, "/cr_restart"))
	giveQueuedTurnTimeToStartWaitingOnTheLock(stallGracePeriod)

	close(runner.release)
	waitUntil(t, func() bool {
		return strings.Contains(strings.Join(h.tg.messages(), "\n"), "перезапущена")
	})

	log := runner.callLog()
	assert.Contains(t, log, "Kill:main",
		"a bare /cr_restart, queued while the active session was still \"main\", must restart "+
			"\"main\" -- the session its turn actually locked -- even though /cr_use switched "+
			"the chat's active session to \"work\" while it was waiting")
	assert.NotContains(t, log, "Kill:work",
		"it must not act on \"work\" just because that became the active session by the time "+
			"the queued turn finally ran -- that is the exact re-derivation bug this test guards")

	cancel()
	h.awaitStop(done)
}
