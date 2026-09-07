package bridge_test

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yabanci/claude-remote/internal/bridge"
)

func TestSplitForTelegramKeepsCyrillicValid(t *testing.T) {
	text := strings.Repeat("проверка кириллицы в ответе бриджа\n", 300)

	chunks := bridge.SplitForTelegram(text, 3500)

	require.Greater(t, len(chunks), 1, "text must actually be split for this test to mean anything")
	for i, c := range chunks {
		assert.True(t, utf8.ValidString(c), "chunk %d contains a broken rune", i)
		assert.LessOrEqual(t, len(c), 3500, "chunk %d exceeds the limit", i)
	}
	assert.Equal(t, text, strings.Join(chunks, ""), "rejoined chunks must equal the original")
}

func TestSplitForTelegramShortTextIsOneChunk(t *testing.T) {
	assert.Equal(t, []string{"короткий ответ"}, bridge.SplitForTelegram("короткий ответ", 3500))
}

func TestSplitForTelegramSplitsOnLineBoundariesWhenPossible(t *testing.T) {
	text := strings.Repeat("abcdefghij\n", 10)

	chunks := bridge.SplitForTelegram(text, 33)

	require.Greater(t, len(chunks), 1)
	for _, c := range chunks {
		assert.True(t, strings.HasSuffix(c, "\n"), "chunk should end at a line boundary: %q", c)
	}
	assert.Equal(t, text, strings.Join(chunks, ""))
}

func TestSplitForTelegramHandlesSingleOverlongLine(t *testing.T) {
	text := strings.Repeat("я", 5000)

	chunks := bridge.SplitForTelegram(text, 3500)

	require.Greater(t, len(chunks), 1)
	for _, c := range chunks {
		assert.True(t, utf8.ValidString(c))
		assert.LessOrEqual(t, len(c), 3500)
	}
	assert.Equal(t, text, strings.Join(chunks, ""))
}

func TestSplitForTelegramPreservesContentForMixedText(t *testing.T) {
	text := strings.Repeat("mixed текст 混合 🚀\n", 400)

	chunks := bridge.SplitForTelegram(text, 1000)

	for _, c := range chunks {
		assert.True(t, utf8.ValidString(c))
	}
	assert.Equal(t, text, strings.Join(chunks, ""))
}

func TestSplitForTelegramCutLandingMidRuneBacksOff(t *testing.T) {
	text := strings.Repeat("混", 2000)
	const limit = 3500

	require.NotZero(t, limit%3, "meaningful only when the limit does not divide the rune width")

	chunks := bridge.SplitForTelegram(text, limit)

	require.Greater(t, len(chunks), 1)
	for i, c := range chunks {
		assert.True(t, utf8.ValidString(c), "chunk %d was cut mid-rune: %q", i, c)
	}
	assert.Equal(t, text, strings.Join(chunks, ""))
}

func TestSplitForTelegramEmitsRuneWiderThanLimitWhole(t *testing.T) {
	chunks := bridge.SplitForTelegram("混混混", 2)

	for _, c := range chunks {
		assert.True(t, utf8.ValidString(c), "a rune wider than the limit must not be split: %q", c)
	}
	assert.Equal(t, "混混混", strings.Join(chunks, ""))
}
