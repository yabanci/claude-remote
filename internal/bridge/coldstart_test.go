package bridge_test

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestAMessageToAStoppedSessionReportsAFailedColdStart(t *testing.T) {
	cfg := testConfigFor(t)
	runner := &failingRunner{fakeRunner: newFakeRunner(), startErr: errors.New("no such directory")}
	h := newHarnessWithRunner(t, cfg, runner)

	h.send("привет")

	assert.Contains(t, h.lastMessage(), "не удалось запустить сессию")
	assert.Contains(t, h.lastMessage(), "no such directory")
	assert.Empty(t, runner.lastSentKeys(),
		"a session that never started must not be typed into")
}

func TestAMessageToAStoppedSessionReportsAnUnreadableScreenAfterStart(t *testing.T) {
	h := newHarness(t)
	h.runner.captureErr = errors.New("can't find pane")

	h.send("привет")

	assert.Contains(t, h.lastMessage(), "не удалось дождаться запуска сессии")
	assert.Contains(t, h.lastMessage(), "can't find pane")
	assert.Empty(t, h.runner.lastSentKeys(),
		"a session whose screen never became readable must not be typed into")
}
