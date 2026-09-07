package bridge_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yabanci/claude-remote/internal/bridge"
)

func TestParseMenuFromRealAutoModeDialog(t *testing.T) {
	menu, ok := bridge.ParseMenu(realChoiceDialog)

	require.True(t, ok)
	assert.Contains(t, menu.Question, "Teach auto mode about your environment?")
	require.Len(t, menu.Options, 3)
	assert.Equal(t, "1", menu.Options[0].Key)
	assert.Equal(t, "Yes", menu.Options[0].Label)
	assert.Equal(t, "2", menu.Options[1].Key)
	assert.Equal(t, "Not now", menu.Options[1].Label)
}

func TestParseMenuFromRealTrustDialog(t *testing.T) {
	_, ok := bridge.ParseMenu(realTrustDialog)

	assert.False(t, ok, "the trust dialog has no numbered options, it must not become buttons")
}

func TestParseMenuFromPermissionPrompt(t *testing.T) {
	pane := `Do you want to allow this command?

  ❯ 1. Yes
    2. No, tell Claude what to do differently

  Enter to confirm · Esc to cancel`

	menu, ok := bridge.ParseMenu(pane)

	require.True(t, ok)
	assert.Contains(t, menu.Question, "allow this command")
	require.Len(t, menu.Options, 2)
	assert.Equal(t, "No, tell Claude what to do differently", menu.Options[1].Label)
}

func TestParseMenuIgnoresOrdinaryNumberedLists(t *testing.T) {
	pane := "⏺ Нашёл три места:\n\n1. первый\n2. второй\n3. третий"

	_, ok := bridge.ParseMenu(pane)

	assert.False(t, ok, "a numbered list in an answer is not a menu without a confirm line")
}
