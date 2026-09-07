package bridge_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yabanci/claude-remote/internal/telegram"
)

func TestAPhotoFromThePhoneLandsInTheInbox(t *testing.T) {
	h := newHarness(t).startSession("main")

	h.deliver(telegram.Message{
		Chat: telegram.Chat{ID: 1},
		From: &telegram.User{ID: testUserID},
		Photo: []telegram.PhotoSize{
			{FileID: "small", Width: 90, Height: 60},
			{FileID: "biggest", Width: 1280, Height: 960},
		},
	})

	entries, err := os.ReadDir(filepath.Join(h.sessionDir("main"), "telegram-inbox"))
	require.NoError(t, err, "a screenshot sent the normal way must not be silently dropped")
	require.Len(t, entries, 1)
	assert.Contains(t, entries[0].Name(), "biggest", "the largest size is the useful one")
	assert.Contains(t, h.runner.lastSentKeys(), "telegram-inbox")
}

func TestACaptionReachesTheSessionWithTheFile(t *testing.T) {
	h := newHarness(t).startSession("main")

	h.deliver(telegram.Message{
		Chat:     telegram.Chat{ID: 1},
		From:     &telegram.User{ID: testUserID},
		Document: &telegram.Document{FileID: "fid", FileName: "trace.log"},
		Caption:  "почему тут таймаут?",
	})

	sent := h.runner.lastSentKeys()
	assert.Contains(t, sent, "trace.log")
	assert.Contains(t, sent, "почему тут таймаут?",
		"the question asked with the file is the whole point of sending it")
}

func TestAMessageWithNothingUsableGetsAnAnswer(t *testing.T) {
	h := newHarness(t).startSession("main")

	h.deliver(telegram.Message{
		Chat: telegram.Chat{ID: 1},
		From: &telegram.User{ID: testUserID},
	})

	assert.Contains(t, h.lastMessage(), "не понял это сообщение",
		"silence is the worst answer: the user cannot tell the bridge from a hang")
}
