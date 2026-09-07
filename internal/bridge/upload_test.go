package bridge_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yabanci/claude-remote/internal/telegram"
)

var errPaneGone = errors.New("tmux pane is gone")

func documentMessage(fileName string) telegram.Message {
	return telegram.Message{
		Chat:     telegram.Chat{ID: 1},
		From:     &telegram.User{ID: testUserID},
		Document: &telegram.Document{FileID: "fid", FileName: fileName},
	}
}

func TestUploadedFileLandsInInboxAndSessionIsTold(t *testing.T) {
	h := newHarness(t).startSession("main")

	h.deliver(documentMessage("notes.txt"))

	saved := filepath.Join(h.sessionDir("main"), "telegram-inbox", "notes.txt")
	body, err := os.ReadFile(saved)
	require.NoError(t, err, "uploaded file must land in the session inbox")
	assert.Equal(t, "uploaded file body", string(body))
	assert.Contains(t, h.runner.lastSentKeys(), filepath.Join("telegram-inbox", "notes.txt"),
		"the session should be told where the file went")
}

func TestUploadedFileNameCannotEscapeInbox(t *testing.T) {
	h := newHarness(t).startSession("main")
	sessionDir := h.sessionDir("main")

	h.deliver(documentMessage("../../escaped.txt"))

	assert.FileExists(t, filepath.Join(sessionDir, "telegram-inbox", "escaped.txt"))
	assert.NoFileExists(t, filepath.Join(filepath.Dir(filepath.Dir(sessionDir)), "escaped.txt"))
}

func TestVeryLongReplyIsSentAsDocument(t *testing.T) {
	h := newHarness(t).startSession("main")
	h.runner.replyPayload = strings.Repeat("длинный ответ от клода ", 1200)

	h.send("расскажи подробно")

	assert.Len(t, h.tg.documents(), 1, "a reply past the inline limit should arrive as a file")
	assert.Empty(t, h.tg.messages())
}

func TestModeratelyLongReplyIsChunkedIntoMessages(t *testing.T) {
	h := newHarness(t).startSession("main")
	h.runner.replyPayload = strings.Repeat("строка ответа\n", 300)

	h.send("покажи")

	messages := h.tg.messages()
	require.Greater(t, len(messages), 1, "should be split across messages")
	assert.Empty(t, h.tg.documents())
	for _, chunk := range messages {
		assert.LessOrEqual(t, len(chunk), 3500)
	}
}

func TestCaptureFailureIsReportedToUser(t *testing.T) {
	h := newHarness(t).startSession("main")
	h.runner.captureErr = errPaneGone

	h.send("привет")

	assert.Contains(t, h.lastMessage(), "не удалось прочитать экран")
}
