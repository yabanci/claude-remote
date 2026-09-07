package bridge_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/yabanci/claude-remote/internal/config"
	"github.com/yabanci/claude-remote/internal/telegram"
)

func TestForwardToSessionRepliesWithDiff(t *testing.T) {
	h := newHarness(t).startSession("main")

	h.send("what is 2+2")

	assert.Contains(t, h.lastMessage(), "reply to: what is 2+2")
}

func TestBootstrapsToFirstSenderWhenAllowedUsersEmpty(t *testing.T) {
	h := newHarness(t, func(c *config.Config) { c.AllowedUsers = nil })

	h.deliver(telegram.Message{
		Chat: telegram.Chat{ID: 1},
		From: &telegram.User{ID: 777},
		Text: "hello",
	})

	assert.Contains(t, h.tg.messages()[0], "777")
}

func TestUnauthorizedSenderIsIgnored(t *testing.T) {
	h := newHarness(t)
	h.tg.updates = []telegram.Update{{UpdateID: 1, Message: &telegram.Message{
		Chat: telegram.Chat{ID: 1},
		From: &telegram.User{ID: 999},
		Text: "let me in",
	}}}

	h.runBriefly()

	assert.Empty(t, h.tg.messages())
	assert.False(t, h.runner.Exists("main"))
}

func TestCrUseSwitchesActiveSession(t *testing.T) {
	h := newHarness(t, func(c *config.Config) {
		c.Sessions["work"] = config.SessionConfig{Dir: t.TempDir(), Command: "claude"}
	})

	h.send("/cr_use work")

	assert.Contains(t, h.lastMessage(), "work")
}

func TestCrUseUnknownSessionReportsError(t *testing.T) {
	h := newHarness(t)

	h.send("/cr_use ghost")

	assert.Contains(t, h.lastMessage(), "не найдена")
}

func TestCrInterruptSendsCtrlC(t *testing.T) {
	h := newHarness(t).startSession("main")

	h.send("/cr_interrupt")

	assert.Contains(t, h.runner.pane("main"), "^C")
}
