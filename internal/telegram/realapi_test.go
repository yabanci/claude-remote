package telegram_test

import (
	"context"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yabanci/claude-remote/internal/telegram"
)

func liveClient(t *testing.T) (*telegram.Client, int64) {
	t.Helper()

	tokenFile := os.Getenv("CLAUDE_REMOTE_LIVE_TOKEN_FILE")
	chatFile := os.Getenv("CLAUDE_REMOTE_LIVE_CHAT_FILE")
	if tokenFile == "" || chatFile == "" {
		t.Skip("set CLAUDE_REMOTE_LIVE_TOKEN_FILE and CLAUDE_REMOTE_LIVE_CHAT_FILE to run against the real Bot API")
	}

	token, err := os.ReadFile(tokenFile)
	require.NoError(t, err)
	rawChat, err := os.ReadFile(chatFile)
	require.NoError(t, err)

	chatID, err := strconv.ParseInt(strings.TrimSpace(string(rawChat)), 10, 64)
	require.NoError(t, err)

	return telegram.NewClient(strings.TrimSpace(string(token))), chatID
}

func TestRealAPIAcceptsAnInlineKeyboard(t *testing.T) {
	client, chatID := liveClient(t)

	err := client.Send(context.Background(), chatID,
		"claude-remote: проверка кнопок. Нажми любую — бридж получит callback.",
		telegram.SendOptions{
			Keyboard: &telegram.InlineKeyboard{Rows: [][]telegram.InlineButton{{
				{Text: "1. Yes", CallbackData: "1"},
				{Text: "2. Not now", CallbackData: "2"},
			}}},
		})

	require.NoError(t, err, "Telegram must accept the reply_markup payload we build")
}

func TestRealAPIAcceptsChatAction(t *testing.T) {
	client, chatID := liveClient(t)

	assert.NoError(t, client.SendChatAction(context.Background(), chatID, "typing"))
}
