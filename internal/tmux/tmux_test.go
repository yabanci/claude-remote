package tmux_test

import (
	"fmt"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yabanci/claude-remote/internal/tmux"
)

func requireTmux(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not installed")
	}
}

func uniqueSession(t *testing.T) string {
	t.Helper()
	name := fmt.Sprintf("claude-remote-test-%d", time.Now().UnixNano())
	t.Cleanup(func() {
		if tmux.Exists(name) {
			_ = tmux.Kill(name)
		}
	})
	return name
}

func TestSessionLifecycle(t *testing.T) {
	requireTmux(t)
	session := uniqueSession(t)

	assert.False(t, tmux.Exists(session))

	require.NoError(t, tmux.Start(session, t.TempDir(), ""))
	assert.True(t, tmux.Exists(session))

	require.NoError(t, tmux.Kill(session))
	assert.False(t, tmux.Exists(session))
}

func TestSendKeysAppearsInCapturedPane(t *testing.T) {
	requireTmux(t)
	session := uniqueSession(t)
	require.NoError(t, tmux.Start(session, t.TempDir(), ""))

	require.NoError(t, tmux.SendKeys(session, "echo hello-from-bridge"))

	var pane string
	require.Eventually(t, func() bool {
		var err error
		pane, err = tmux.CapturePane(session, 100)
		return err == nil && strings.Contains(pane, "hello-from-bridge")
	}, 10*time.Second, 200*time.Millisecond, "expected echoed output in pane, got:\n%s", pane)
}

func TestSendKeysIsLiteralNotInterpreted(t *testing.T) {
	requireTmux(t)
	session := uniqueSession(t)
	require.NoError(t, tmux.Start(session, t.TempDir(), ""))

	require.NoError(t, tmux.SendKeys(session, "echo 'C-c ; literal $TEST'"))

	var pane string
	require.Eventually(t, func() bool {
		var err error
		pane, err = tmux.CapturePane(session, 100)
		return err == nil && strings.Contains(pane, "C-c ; literal $TEST")
	}, 10*time.Second, 200*time.Millisecond, "expected literal text in pane, got:\n%s", pane)
}

func TestListSessionsIncludesStartedSession(t *testing.T) {
	requireTmux(t)
	session := uniqueSession(t)
	require.NoError(t, tmux.Start(session, t.TempDir(), ""))

	sessions, err := tmux.ListSessions()

	require.NoError(t, err)
	assert.Contains(t, sessions, session)
}
