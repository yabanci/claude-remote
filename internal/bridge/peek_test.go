package bridge_test

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCrPeekShowsTheScreenWithoutTypingIntoIt(t *testing.T) {
	h := newHarness(t).startSession("main")
	h.runner.setPane("main", "⏺ здесь уже был ответ\n\n✻ done")

	h.send("/cr_peek")

	assert.Contains(t, h.lastMessage(), "здесь уже был ответ")
	assert.Empty(t, h.runner.lastSentKeys(), "peek must not send anything into the session")
}

func TestCrPeekOnStoppedSessionSaysSo(t *testing.T) {
	h := newHarness(t)

	h.send("/cr_peek")

	assert.Contains(t, h.lastMessage(), "не запущена")
}

func TestCrPeekIsOfferedInHelpAndMenu(t *testing.T) {
	h := newHarness(t)

	h.send("/cr_help")

	assert.Contains(t, h.lastMessage(), "/cr_peek")
}

type dyingRunner struct {
	*fakeRunner
	died bool
}

func (d *dyingRunner) SendKeys(session, text string) error {
	if err := d.fakeRunner.SendKeys(session, text); err != nil {
		return err
	}
	d.died = true
	return d.Kill(session)
}

func (d *dyingRunner) CapturePane(session string, historyLines int) (string, error) {
	if d.died {
		return "", errors.New("can't find pane")
	}
	return d.fakeRunner.CapturePane(session, historyLines)
}

func TestSessionDyingMidTurnIsReportedClearly(t *testing.T) {
	cfg := testConfigFor(t)
	runner := &dyingRunner{fakeRunner: newFakeRunner()}
	require.NoError(t, runner.Start("main", cfg.Sessions["main"].Dir, "claude"))

	h := newHarnessWithRunner(t, cfg, runner)
	h.send("посчитай что-нибудь")

	assert.Contains(t, h.lastMessage(), "пропала")
	assert.Contains(t, h.lastMessage(), "/cr_restart")
}

type vanishesOnCaptureRunner struct {
	*fakeRunner
}

func (v *vanishesOnCaptureRunner) CapturePane(session string, historyLines int) (string, error) {
	_ = v.Kill(session)
	return "", errors.New("can't find pane")
}

func TestCrPeekReportsVanishedSessionLikeOtherCaptureFailures(t *testing.T) {
	cfg := testConfigFor(t)
	runner := &vanishesOnCaptureRunner{fakeRunner: newFakeRunner()}
	require.NoError(t, runner.Start("main", cfg.Sessions["main"].Dir, "claude"))

	h := newHarnessWithRunner(t, cfg, runner)
	h.send("/cr_peek")

	assert.Contains(t, h.lastMessage(), "пропала")
	assert.Contains(t, h.lastMessage(), "/cr_restart")
}
