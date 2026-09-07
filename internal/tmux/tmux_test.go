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

func waitForPane(t *testing.T, session, want string) {
	t.Helper()
	var pane string
	require.Eventually(t, func() bool {
		var err error
		pane, err = tmux.CapturePane(session, 100)
		return err == nil && strings.Contains(pane, want)
	}, 10*time.Second, 200*time.Millisecond, "expected %q in pane, got:\n%s", want, pane)
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

func TestStartRunsTheGivenCommand(t *testing.T) {
	requireTmux(t)
	session := uniqueSession(t)

	require.NoError(t, tmux.Start(session, t.TempDir(), "echo started-with-command"))

	waitForPane(t, session, "started-with-command")
}

func TestSendKeysAppearsInCapturedPane(t *testing.T) {
	requireTmux(t)
	session := uniqueSession(t)
	require.NoError(t, tmux.Start(session, t.TempDir(), ""))

	require.NoError(t, tmux.SendKeys(session, "echo hello-from-bridge"))

	waitForPane(t, session, "hello-from-bridge")
}

func TestSendKeysIsLiteralNotInterpreted(t *testing.T) {
	requireTmux(t)
	session := uniqueSession(t)
	require.NoError(t, tmux.Start(session, t.TempDir(), ""))

	require.NoError(t, tmux.SendKeys(session, "echo 'C-c ; literal $TEST'"))

	waitForPane(t, session, "C-c ; literal $TEST")
}

func TestInterruptReachesThePane(t *testing.T) {
	requireTmux(t)
	session := uniqueSession(t)
	require.NoError(t, tmux.Start(session, t.TempDir(), ""))
	require.NoError(t, tmux.SendKeys(session, "sleep 30"))

	require.NoError(t, tmux.Interrupt(session))

	waitForPane(t, session, "^C")
}

func TestCaptureVisibleOnlyStillReturnsContent(t *testing.T) {
	requireTmux(t)
	session := uniqueSession(t)
	require.NoError(t, tmux.Start(session, t.TempDir(), ""))
	require.NoError(t, tmux.SendKeys(session, "echo visible-pane-marker"))
	waitForPane(t, session, "visible-pane-marker")

	visible, err := tmux.CapturePane(session, 0)

	require.NoError(t, err)
	assert.Contains(t, visible, "visible-pane-marker")
}

func TestOperationsOnMissingSessionReturnErrors(t *testing.T) {
	requireTmux(t)
	absent := fmt.Sprintf("cr-absent-%d", time.Now().UnixNano())

	assert.False(t, tmux.Exists(absent))
	assert.Error(t, tmux.Kill(absent))
	assert.Error(t, tmux.SendKeys(absent, "hello"))
	assert.Error(t, tmux.Interrupt(absent))

	_, err := tmux.CapturePane(absent, 100)
	assert.Error(t, err)
}

func TestStartFailsOnMissingDirectory(t *testing.T) {
	requireTmux(t)
	session := uniqueSession(t)

	assert.Error(t, tmux.Start(session, "/definitely/not/a/directory", ""))
}
