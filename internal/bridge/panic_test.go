package bridge_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/yabanci/claude-remote/internal/telegram"
)

type panickingRunner struct {
	*fakeRunner
	panicOn string
}

func (r *panickingRunner) CapturePane(session string, historyLines int) (string, error) {
	if session == r.panicOn {
		panic("simulated panic while reading the pane")
	}
	return r.fakeRunner.CapturePane(session, historyLines)
}

func TestAPanicInOneUpdateDoesNotCrashTheBridgeOrDropSiblingUpdates(t *testing.T) {
	cfg := testConfigFor(t)
	runner := &panickingRunner{fakeRunner: newFakeRunner(), panicOn: "main"}
	require.NoError(t, runner.Start("main", cfg.Sessions["main"].Dir, "claude"))
	h := newHarnessWithRunner(t, cfg, runner)
	h.tg.updates = []telegram.Update{
		textUpdate(1, "this one panics"),
		textUpdate(2, "/cr_status"),
	}

	cancel, done := h.runInBackground()
	waitUntil(t, func() bool { return len(h.tg.messages()) >= 2 })

	joined := strings.Join(h.tg.messages(), "\n")
	require.Contains(t, joined, "сломалось",
		"the panicking update must get a graceful reply, not silence")
	require.Contains(t, joined, "main",
		"the sibling update dispatched right after must still be processed normally")

	cancel()
	h.awaitStop(done)
}
