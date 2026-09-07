package bridge_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/yabanci/claude-remote/internal/bridge"
)

const realTrustDialog = `Accessing workspace:

 /Users/someone/projects/scratch

 Quick safety check: Is this a project you created or one you trust?

 Claude Code'll be able to read, edit, and execute files here.

 > No, exit
   Yes, I trust this folder

 Enter to confirm - Esc to cancel`

func TestAwaitsTrustConfirmation(t *testing.T) {
	tests := []struct {
		name string
		pane string
		want bool
	}{
		{"real first-run dialog", realTrustDialog, true},
		{"ordinary session", "$ claude\n> what is 2+2\n4", false},
		{"empty pane", "", false},
		{"reply that merely mentions trust", "I trust this analysis is correct", false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, bridge.AwaitsTrustConfirmation(tc.pane))
		})
	}
}

func TestBridgeRefusesToTypeIntoTrustDialog(t *testing.T) {
	h := newHarness(t).startSession("main")
	h.runner.setPane("main", realTrustDialog)

	h.send("сколько будет 2+2")

	assert.Contains(t, h.lastMessage(), "ждёт подтверждения доверия")
	assert.Contains(t, h.lastMessage(), "tmux attach -t "+bridge.TmuxSessionName("main"),
		"the hint must name the session as tmux knows it, not as the config calls it")
	assert.Empty(t, h.runner.lastSentKeys(),
		"a security prompt must never be answered on the user behalf")
}

const realChoiceDialog = `> сколько будет 17*23?

  Teach auto mode about your environment?

  Auto mode works better when it knows your environment. Takes about a minute.

  > 1. Yes
    2. Not now
    3. Don't show again

  Enter to confirm - Esc to cancel`

func TestAwaitsInteractiveChoice(t *testing.T) {
	assert.True(t, bridge.AwaitsInteractiveChoice(realChoiceDialog))
	assert.True(t, bridge.AwaitsInteractiveChoice(realTrustDialog))
	assert.False(t, bridge.AwaitsInteractiveChoice("$ claude\n> 2+2\n4"))
}

func TestIsDeliberateChoice(t *testing.T) {
	for _, short := range []string{"1", "2", " 3 ", "yes", "no", "да", "нет"} {
		assert.True(t, bridge.IsDeliberateChoice(short), "%q should count as answering a menu", short)
	}
	for _, prose := range []string{"сколько будет 17*23?", "run the tests please"} {
		assert.False(t, bridge.IsDeliberateChoice(prose), "%q is prose, not a menu answer", prose)
	}
}

func TestProseIsNotTypedIntoAnOpenDialog(t *testing.T) {
	h := newHarness(t).startSession("main")
	h.runner.setPane("main", realChoiceDialog)

	h.send("перечитай README и расскажи что там")

	assert.Contains(t, h.lastMessage(), "открыт диалог")
	assert.Contains(t, h.lastMessage(), "Teach auto mode")
	assert.Empty(t, h.runner.lastSentKeys(), "prose must never land in a menu")
}

func TestShortAnswerReachesAnOpenDialog(t *testing.T) {
	h := newHarness(t).startSession("main")
	h.runner.setPane("main", realChoiceDialog)

	h.send("2")

	assert.Equal(t, "2", h.runner.lastSentKeys(), "a deliberate menu answer must be passed through")
}
