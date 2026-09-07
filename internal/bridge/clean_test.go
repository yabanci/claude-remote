package bridge_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/yabanci/claude-remote/internal/bridge"
)

const realReplyFromTelegram = `⏺ 391

✻ Crunched for 1s · done 12:58




────────────────────────────────────────────────────────────────────────────────
❯
────────────────────────────────────────────────────────────────────────────────
  [Sonnet 5] │ pet                                                         /rc
  Context ░░░░░░ 6% (58k/1.0M) │ Usage Weekly ███░░░ 54% (resets in 2d 7h)
  ⏵⏵ auto mode on (shift+tab to cycle) · ← for agents`

func TestCleanReplyStripsTuiChrome(t *testing.T) {
	got := bridge.CleanReply(realReplyFromTelegram)

	assert.Contains(t, got, "391", "the answer itself must survive")
	assert.NotContains(t, got, "Sonnet 5")
	assert.NotContains(t, got, "Context ")
	assert.NotContains(t, got, "auto mode on")
	assert.NotContains(t, got, "────")
	assert.NotContains(t, got, "\n❯")
}

func TestCleanReplyKeepsRealContent(t *testing.T) {
	answer := "Вот что я нашёл:\n\n1. первый пункт\n2. второй пункт\n\nfunc main() {\n\tfmt.Println(\"hi\")\n}"

	got := bridge.CleanReply(answer)

	assert.Equal(t, answer, got, "ordinary prose and code must pass through untouched")
}

func TestCleanReplyCollapsesBlankRuns(t *testing.T) {
	got := bridge.CleanReply("ответ\n\n\n\n\nхвост")

	assert.Equal(t, "ответ\n\nхвост", got)
}

func TestCleanReplyOnChromeOnlyInputIsEmpty(t *testing.T) {
	chrome := strings.Join([]string{
		"────────────────────────────",
		"❯ ",
		"  [Sonnet 5] │ pet    /rc",
		"  Context ░░░ 6% (58k/1.0M)",
		"  ⏵⏵ auto mode on (shift+tab to cycle)",
	}, "\n")

	assert.Empty(t, bridge.CleanReply(chrome))
}

func TestCleanReplyKeepsLinesThatMerelyMentionContext(t *testing.T) {
	answer := "Context switching costs about 20 minutes per interruption."

	assert.Equal(t, answer, bridge.CleanReply(answer),
		"a sentence starting with Context but carrying no meter must survive")
}
