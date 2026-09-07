package bridge_test

import (
	"fmt"
	"os/exec"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yabanci/claude-remote/internal/bridge"
	"github.com/yabanci/claude-remote/internal/tmux"
)

func TestTheBridgeNeverTouchesASessionItDidNotCreate(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not installed")
	}

	foreign := fmt.Sprintf("work-%d", time.Now().UnixNano())
	require.NoError(t, tmux.Start(foreign, t.TempDir(), ""))
	t.Cleanup(func() {
		if tmux.Exists(foreign) {
			_ = tmux.Kill(foreign)
		}
	})
	require.NoError(t, tmux.SendKeys(foreign, "echo untouched-by-the-bridge"))

	runner := bridge.NewTmuxRunner()
	t.Cleanup(func() {
		if tmux.Exists(bridge.TmuxSessionName(foreign)) {
			_ = tmux.Kill(bridge.TmuxSessionName(foreign))
		}
	})

	assert.False(t, runner.Exists(foreign),
		"a session the user runs by hand must be invisible to the bridge, even with the same name")

	require.NoError(t, runner.Start(foreign, t.TempDir(), ""))
	require.NoError(t, runner.SendKeys(foreign, "echo typed-by-the-bridge"))

	pane, err := tmux.CapturePane(foreign, 200)
	require.NoError(t, err)
	assert.Contains(t, pane, "untouched-by-the-bridge")
	assert.NotContains(t, pane, "typed-by-the-bridge",
		"the bridge must have created its own session instead of typing into the user's")

	assert.True(t, tmux.Exists(bridge.TmuxSessionName(foreign)),
		"the bridge session lives under its own namespaced name")
}

func TestKillCannotReachAUserSession(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not installed")
	}

	foreign := fmt.Sprintf("precious-%d", time.Now().UnixNano())
	require.NoError(t, tmux.Start(foreign, t.TempDir(), ""))
	t.Cleanup(func() {
		if tmux.Exists(foreign) {
			_ = tmux.Kill(foreign)
		}
	})

	err := bridge.NewTmuxRunner().Kill(foreign)

	assert.Error(t, err, "there is no bridge session by that name, so the kill must fail")
	assert.True(t, tmux.Exists(foreign), "the user's session survives")
}
