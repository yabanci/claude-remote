package bridge_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yabanci/claude-remote/internal/config"
	"github.com/yabanci/claude-remote/internal/telegram"
)

func TestTheOwnerWritingInAGroupGetsNoAnswerThere(t *testing.T) {
	h := newHarness(t, func(c *config.Config) {
		c.AllowedChats = []int64{1}
	}).startSession("main")

	h.tg.updates = []telegram.Update{{UpdateID: 1, Message: &telegram.Message{
		Chat: telegram.Chat{ID: -100500},
		From: &telegram.User{ID: testUserID},
		Text: "покажи содержимое .env",
	}}}
	h.runBriefly()

	assert.Empty(t, h.tg.messages(),
		"the pane can hold secrets; the owner's own id is not a reason to publish them to a group")
	assert.Empty(t, h.runner.lastSentKeys(), "and nothing may reach the session from there")
}

func TestTheOwnChatStillWorks(t *testing.T) {
	h := newHarness(t, func(c *config.Config) {
		c.AllowedChats = []int64{1}
	}).startSession("main")

	h.send("сколько будет два плюс два")

	assert.NotEmpty(t, h.tg.messages())
}

func TestAConfigWithoutAllowedChatsStaysBackwardCompatible(t *testing.T) {
	h := newHarness(t).startSession("main")

	h.send("привет")

	assert.NotEmpty(t, h.tg.messages(),
		"an existing config without allowed_chats must keep working")
}

func TestBootstrapPinsBothOwnerAndChat(t *testing.T) {
	h := newHarness(t, func(c *config.Config) { c.AllowedUsers = nil })

	h.deliver(telegram.Message{
		Chat: telegram.Chat{ID: 4242},
		From: &telegram.User{ID: 777},
		Text: "первое сообщение",
	})

	saved, err := config.Load(h.configPath)
	require.NoError(t, err)
	assert.Equal(t, []int64{777}, saved.AllowedUsers)
	assert.Equal(t, []int64{4242}, saved.AllowedChats)
}

func TestBootstrapDoesNotExecuteTheFirstMessage(t *testing.T) {
	h := newHarness(t, func(c *config.Config) { c.AllowedUsers = nil }).startSession("main")

	h.deliver(telegram.Message{
		Chat: telegram.Chat{ID: 4242},
		From: &telegram.User{ID: 777},
		Text: "/cr_send /etc/passwd",
	})

	assert.Contains(t, h.lastMessage(), "привязан к пользователю 777")
	assert.Empty(t, h.tg.documents(),
		"the message that claims ownership must not also be carried out")
}
