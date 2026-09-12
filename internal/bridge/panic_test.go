package bridge_test

import (
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/yabanci/claude-remote/internal/telegram"
)

type panickingRunner struct {
	*fakeRunner
	panicOn string

	mu       sync.Mutex
	panicked bool
}

func (r *panickingRunner) CapturePane(session string, historyLines int) (string, error) {
	r.mu.Lock()
	if session == r.panicOn && !r.panicked {
		r.panicked = true
		r.mu.Unlock()
		panic("simulated panic while reading the pane")
	}
	r.mu.Unlock()
	return r.fakeRunner.CapturePane(session, historyLines)
}

func TestAPanicInOneUpdateDoesNotCrashTheBridgeOrDropSiblingUpdates(t *testing.T) {
	cfg := testConfigFor(t)
	runner := &panickingRunner{fakeRunner: newFakeRunner(), panicOn: "main"}
	require.NoError(t, runner.Start("main", cfg.Sessions["main"].Dir, "claude"))
	h := newHarnessWithRunner(t, cfg, runner)
	h.tg.updates = []telegram.Update{
		textUpdate(1, "this one panics"),
		textUpdate(2, "this one must still land on main"),
	}

	cancel, done := h.runInBackground()
	waitUntil(t, func() bool { return len(h.tg.messages()) >= 2 })

	joined := strings.Join(h.tg.messages(), "\n")
	require.Contains(t, joined, "сломалось",
		"the panicking update must get a graceful reply, not silence")
	require.Contains(t, joined, "reply to: this one must still land on main",
		"the sibling update targeting the SAME session must still be processed -- if "+
			"inSessionTurn's lock on \"main\" were left held by the panic, this would hang "+
			"until the test's own timeout instead of getting a real reply")

	cancel()
	h.awaitStop(done)
}
