package tmux_test

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yabanci/claude-remote/internal/tmux"
)

func TestTextStartingWithADashIsNotEatenAsAFlag(t *testing.T) {
	requireTmux(t)
	session := uniqueSession(t)
	require.NoError(t, tmux.Start(session, t.TempDir(), ""))

	require.NoError(t, tmux.SendKeys(session, "-R это должно напечататься"))

	waitForPane(t, session, "-R это должно напечататься")
}

func TestVariousLeadingFlagsSurvive(t *testing.T) {
	requireTmux(t)
	for _, text := range []string{"-v verbose", "--help меня", "-N 5 штук", "-X copy"} {
		t.Run(text, func(t *testing.T) {
			session := uniqueSession(t)
			require.NoError(t, tmux.Start(session, t.TempDir(), ""))

			require.NoError(t, tmux.SendKeys(session, text))

			waitForPane(t, session, text)
		})
	}
}

func TestAMultilineMessageArrivesAsOneInput(t *testing.T) {
	requireTmux(t)
	session := uniqueSession(t)
	require.NoError(t, tmux.Start(session, t.TempDir(), ""))

	require.NoError(t, tmux.SendKeys(session, "echo AAA\necho BBB"))

	var pane string
	require.Eventually(t, func() bool {
		var err error
		pane, err = tmux.CapturePane(session, 200)
		return err == nil && strings.Contains(pane, "AAA") && strings.Contains(pane, "BBB")
	}, 10*time.Second, 200*time.Millisecond, "both lines must reach the session, got:\n%s", pane)

	assert.NotContains(t, pane, "BBBAAA",
		"sending a literal newline lets the first line execute while the rest is still typing")
}

func TestCaptureOfAMissingSessionCarriesTmuxDiagnostics(t *testing.T) {
	requireTmux(t)

	_, err := tmux.CapturePane("cr-definitely-absent-session", 100)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "session",
		"a background service has no other channel for why tmux refused")
}
