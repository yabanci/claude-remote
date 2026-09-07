package bridge_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/yabanci/claude-remote/internal/bridge"
)

const realPaneWithAnswer = `⏺ 391

✻ Crunched for 1s · done 12:58




────────────────────────────────────────────────────────────────────────────────
❯
────────────────────────────────────────────────────────────────────────────────
  [Sonnet 5] │ pet                                                         /rc
  Context ░░░░░░ 6% (58k/1.0M) │ Usage Weekly ███░░░ 54% (resets in 2d 7h)
  ⏵⏵ auto mode on (shift+tab to cycle) · ← for agents`

const realPaneWithToolsAndAnswer = `⏺ Read(README.md)
  ⎿  Read 42 lines

⏺ Bash(go test ./...)
  ⎿  ok  github.com/example/pkg 0.3s

⏺ Тесты проходят, README описывает сборку через make.
  Ничего чинить не нужно.

✻ Crunched for 12s · done 13:20
────────────────────────────────────────────────────────────────────────────────
  [Sonnet 5] │ pet                                                         /rc
  Context ░░░░░░ 7% (61k/1.0M)
  ⏵⏵ auto mode on (shift+tab to cycle) · ← for agents`

func TestFormatReplyReturnsOnlyTheAnswer(t *testing.T) {
	got := bridge.FormatReply(realPaneWithAnswer)

	assert.Equal(t, "391", got, "a chat message is the answer, not a screen")
}

func TestFormatReplyDropsToolLogsButKeepsProse(t *testing.T) {
	got := bridge.FormatReply(realPaneWithToolsAndAnswer)

	assert.Equal(t, "Тесты проходят, README описывает сборку через make.\nНичего чинить не нужно.", got)
	assert.NotContains(t, got, "Read(")
	assert.NotContains(t, got, "⎿")
}

func TestExtractAnswerCountsToolBlocks(t *testing.T) {
	answer := bridge.ExtractAnswer(realPaneWithToolsAndAnswer)

	assert.Equal(t, 2, answer.ToolBlocks)
	assert.Contains(t, answer.Text, "Тесты проходят")
}

func TestFormatReplyReportsToolsWhenThereIsNoProse(t *testing.T) {
	pane := "⏺ Bash(make build)\n  ⎿  built\n\n✻ done"

	got := bridge.FormatReply(pane)

	assert.Contains(t, got, "выполнила 1 действий")
}

func TestFormatReplyFallsBackToCleanTextWithoutMarkers(t *testing.T) {
	pane := "$ echo hi\nhi\n────────────\n  Context ░░░ 6% (58k/1.0M)"

	got := bridge.FormatReply(pane)

	assert.Contains(t, got, "hi")
	assert.NotContains(t, got, "Context ")
}

func TestFormatReplyKeepsMultilineAnswerShape(t *testing.T) {
	pane := "⏺ Нашёл три места:\n\n  1. первый\n  2. второй\n  3. третий\n\n✻ done"

	got := bridge.FormatReply(pane)

	assert.Contains(t, got, "Нашёл три места:")
	assert.Contains(t, got, "1. первый")
	assert.Contains(t, got, "3. третий")
}
