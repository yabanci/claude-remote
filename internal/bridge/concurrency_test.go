package bridge_test

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yabanci/claude-remote/internal/telegram"
)

const stallGracePeriod = 500 * time.Millisecond

type captureStallingRunner struct {
	*fakeRunner
	sent    chan struct{}
	release chan struct{}
	once    sync.Once
}

func newCaptureStallingRunner() *captureStallingRunner {
	return &captureStallingRunner{
		fakeRunner: newFakeRunner(),
		sent:       make(chan struct{}),
		release:    make(chan struct{}),
	}
}

func (r *captureStallingRunner) SendKeys(session, text string) error {
	err := r.fakeRunner.SendKeys(session, text)
	r.once.Do(func() { close(r.sent) })
	return err
}

func (r *captureStallingRunner) CapturePane(session string, historyLines int) (string, error) {
	select {
	case <-r.sent:
		<-r.release
	default:
	}
	return r.fakeRunner.CapturePane(session, historyLines)
}

type sendStallingRunner struct {
	*fakeRunner
	holdText string
	entered  chan struct{}
	release  chan struct{}
	once     sync.Once

	attemptsMu sync.Mutex
	attempts   []string
}

func newSendStallingRunner(holdText string) *sendStallingRunner {
	return &sendStallingRunner{
		fakeRunner: newFakeRunner(),
		holdText:   holdText,
		entered:    make(chan struct{}),
		release:    make(chan struct{}),
	}
}

func (r *sendStallingRunner) SendKeys(session, text string) error {
	r.attemptsMu.Lock()
	r.attempts = append(r.attempts, text)
	r.attemptsMu.Unlock()

	if text == r.holdText {
		r.once.Do(func() { close(r.entered) })
		<-r.release
	}
	return r.fakeRunner.SendKeys(session, text)
}

func (r *sendStallingRunner) sendAttempts() []string {
	r.attemptsMu.Lock()
	defer r.attemptsMu.Unlock()
	return append([]string(nil), r.attempts...)
}

func awaitSignal(t *testing.T, signal <-chan struct{}, what string) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(5 * time.Second):
		t.Fatalf("timed out waiting for %s", what)
	}
}

func textUpdate(id int64, text string) telegram.Update {
	return telegram.Update{UpdateID: id, Message: &telegram.Message{
		MessageID: id,
		Chat:      telegram.Chat{ID: 1},
		From:      &telegram.User{ID: testUserID},
		Text:      text,
	}}
}

func (h *harness) runInBackground() (stop func(), done chan struct{}) {
	h.t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done = make(chan struct{})
	go func() {
		defer close(done)
		_ = h.bridge.Run(ctx)
	}()
	return cancel, done
}

func TestCrInterruptIsActedOnWhileAReplyIsStillInFlight(t *testing.T) {
	cfg := testConfigFor(t)
	runner := newCaptureStallingRunner()
	require.NoError(t, runner.Start("main", cfg.Sessions["main"].Dir, "claude"))
	h := newHarnessWithRunner(t, cfg, runner)
	h.tg.updates = []telegram.Update{
		textUpdate(1, "take as long as you need"),
		textUpdate(2, "/cr_interrupt"),
	}

	cancel, done := h.runInBackground()
	awaitSignal(t, runner.sent, "the first message to reach the session")

	waitUntil(t, func() bool { return strings.Contains(runner.pane("main"), "^C") })
	waitUntil(t, func() bool {
		return strings.Contains(strings.Join(h.tg.messages(), "\n"), "Ctrl-C отправлен")
	})

	close(runner.release)
	cancel()
	h.awaitStop(done)
}

func TestTwoMessagesToTheSameSessionDoNotInterleave(t *testing.T) {
	cfg := testConfigFor(t)
	runner := newSendStallingRunner("first question")
	require.NoError(t, runner.Start("main", cfg.Sessions["main"].Dir, "claude"))
	h := newHarnessWithRunner(t, cfg, runner)
	h.tg.updates = []telegram.Update{
		textUpdate(1, "first question"),
		textUpdate(2, "second question"),
	}

	cancel, done := h.runInBackground()
	awaitSignal(t, runner.entered, "the first message to reach the session")

	time.Sleep(stallGracePeriod)
	assert.Equal(t, []string{"first question"}, runner.sendAttempts(),
		"the second message must not be typed into the session while the first turn holds it")

	close(runner.release)
	waitUntil(t, func() bool { return len(runner.sendAttempts()) == 2 })
	assert.Equal(t, []string{"first question", "second question"}, runner.sendAttempts())

	cancel()
	h.awaitStop(done)
}
