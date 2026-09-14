package bridge_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yabanci/claude-remote/internal/telegram"
)

func TestMenuArrivesAsButtonsNotAsInstructions(t *testing.T) {
	h := newHarness(t).startSession("main")
	h.runner.replyPayload = realChoiceDialog

	h.send("включи что-нибудь")

	require.NotEmpty(t, h.tg.keyboards(), "a menu must arrive as tappable buttons")
	buttons := h.tg.keyboards()[0]
	assert.Equal(t, []string{"1. Yes", "2. Not now", "3. Don't show again"}, buttons)
	assert.NotContains(t, h.lastMessage(), "Ответь коротко",
		"with buttons there is no need to instruct the user to type a number")
}

func TestButtonPressIsSentIntoTheSession(t *testing.T) {
	h := newHarness(t).startSession("main")

	h.deliverCallback("2")

	assert.Equal(t, "2", h.runner.lastSentKeys())
	assert.NotEmpty(t, h.tg.answeredCallbacks(), "Telegram requires the callback to be acknowledged")
}

func TestRepliesAreThreadedToTheAskingMessage(t *testing.T) {
	h := newHarness(t).startSession("main")

	h.deliver(telegram.Message{
		MessageID: 4242,
		Chat:      telegram.Chat{ID: 1},
		From:      &telegram.User{ID: testUserID},
		Text:      "привет",
	})

	assert.Equal(t, int64(4242), h.tg.firstReplyTo(),
		"a chat answer should hang off the message that asked it")
}

func TestTypingIndicatorIsShownWhileWaiting(t *testing.T) {
	h := newHarness(t).startSession("main")

	h.send("посчитай что-нибудь")

	assert.Positive(t, h.tg.typingActions(), "the chat should show that something is happening")
}

const longChoiceDialog = `> смени модель

  Which model should this session use?

  > 1. Opus 5
    2. Sonnet 5
    3. Haiku 4.5
    4. Opus 4.8
    5. Sonnet 4.5
    6. Haiku 3.5
    7. Ask me every time

  Enter to confirm - Esc to cancel`

func TestALongMenuIsSplitIntoSeveralButtonRows(t *testing.T) {
	h := newHarness(t).startSession("main")
	h.runner.replyPayload = longChoiceDialog

	h.send("смени модель")

	rows := h.tg.rowsOfLastKeyboard()
	require.Len(t, rows, 3, "seven options must not be crammed into one unbounded row")
	assert.Equal(t, []string{"1. Opus 5", "2. Sonnet 5", "3. Haiku 4.5"}, rows[0])
	assert.Equal(t, []string{"4. Opus 4.8", "5. Sonnet 4.5", "6. Haiku 3.5"}, rows[1])
	assert.Equal(t, []string{"7. Ask me every time"}, rows[2])
}

func TestAMenuArrivesAsTextWhenTelegramRejectsTheKeyboard(t *testing.T) {
	h := newHarness(t).startSession("main")
	h.runner.replyPayload = realChoiceDialog
	h.tg.failEveryKeyboardSend()

	h.send("включи что-нибудь")

	require.Empty(t, h.tg.keyboards(), "the keyboard send was rejected")
	assert.Contains(t, h.lastMessage(), "1. Yes")
	assert.Contains(t, h.lastMessage(), "2. Not now")
	assert.Contains(t, h.lastMessage(), "3. Don't show again")
	assert.Contains(t, h.lastMessage(), "Ответь номером",
		"without buttons the user has to be told how to answer")
}
