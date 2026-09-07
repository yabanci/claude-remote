package bridge_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yabanci/claude-remote/internal/bridge"
)

const paneWithPreviousTurnStillOnScreen = `⏺ 391

✻ Crunched for 1s · done 12:58

❯ посмотри, какие каталоги лежат в текущей папке, и назови их одной строкой
через запятую

  Listed 1 directory

⏺ claude-remote, docs, imc-rewrite, memory-service, mypaste, territory-run

✻ Cooked for 3s · done 13:06
────────────────────────────────────────────────────────────────────────────────
❯ territory-run в разработке, глянь PROGRESS.md
  [Sonnet 5] │ pet
  Context ░░░░░░ 6% (58k/1.0M)
  ⏵⏵ auto mode on (shift+tab to cycle) · ← for agents
                                                                           /rc`

func TestTailAfterPromptDropsEverythingBeforeThisTurn(t *testing.T) {
	sent := "посмотри, какие каталоги лежат в текущей папке, и назови их одной строкой через запятую"

	tail, ok := bridge.TailAfterPrompt(paneWithPreviousTurnStillOnScreen, sent)

	require.True(t, ok, "the echoed prompt must be found on screen")
	assert.NotContains(t, tail, "391", "the previous turn must not leak into this reply")
	assert.Contains(t, tail, "claude-remote, docs")
}

func TestFormattedReplyForARealScrolledPane(t *testing.T) {
	sent := "посмотри, какие каталоги лежат в текущей папке, и назови их одной строкой через запятую"

	tail, ok := bridge.TailAfterPrompt(paneWithPreviousTurnStillOnScreen, sent)
	require.True(t, ok)
	got := firstOf(bridge.FormatReply(tail))

	assert.Equal(t, "claude-remote, docs, imc-rewrite, memory-service, mypaste, territory-run", got,
		"a chat reply is the answer alone: no prior turn, no echo, no ghost input, no status bar")
}

func TestTailAfterPromptReportsWhenAnchorIsAbsent(t *testing.T) {
	_, ok := bridge.TailAfterPrompt("nothing familiar here", "какой-то другой вопрос")

	assert.False(t, ok, "caller must fall back to diffing when the echo is not on screen")
}

func TestTailAfterPromptHandlesEmptyInput(t *testing.T) {
	_, ok := bridge.TailAfterPrompt("pane", "   ")

	assert.False(t, ok)
}

func TestTailAfterPromptUsesTheLastOccurrence(t *testing.T) {
	pane := "❯ повтори\nстарый ответ\n❯ повтори\nновый ответ"

	tail, ok := bridge.TailAfterPrompt(pane, "повтори")

	require.True(t, ok)
	assert.Equal(t, "новый ответ", tail)
	assert.NotContains(t, tail, "старый")
}

func TestTailIsNotFooledByAnAnswerQuotingTheQuestion(t *testing.T) {
	pane := "❯ где лежит конфиг\n\n⏺ Ты спросил \"где лежит конфиг\" — он в ~/.config/claude-remote."

	tail, ok := bridge.TailAfterPrompt(pane, "где лежит конфиг")

	require.True(t, ok)
	assert.Contains(t, firstOf(bridge.FormatReply(tail)), "~/.config/claude-remote",
		"anchoring must use the input line, not a quote of the question inside the answer")
}

func TestTailFallsBackWhenOnlyAQuoteIsPresent(t *testing.T) {
	pane := "⏺ Ты спросил \"где лежит конфиг\" — вот ответ."

	_, ok := bridge.TailAfterPrompt(pane, "где лежит конфиг")

	assert.False(t, ok, "without an input line there is no anchor, so the caller must diff instead")
}
