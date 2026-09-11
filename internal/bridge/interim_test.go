package bridge_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yabanci/claude-remote/internal/config"
)

func neverSettlesBeforeTheHardCap(cfg *config.Config) {
	cfg.Settle.PollIntervalMS = 20
	cfg.Settle.StableRounds = 1000
	cfg.Settle.HardCapSeconds = 1
	cfg.Settle.InterimNoticeSeconds = 1
}

func TestALongTurnIsAnnouncedBeforeTheAnswerArrives(t *testing.T) {
	h := newHarness(t, neverSettlesBeforeTheHardCap)
	h.startSession("main")

	h.sendAwaiting("долгий вопрос", 2)

	msgs := h.tg.messages()
	require.Len(t, msgs, 2)
	assert.Contains(t, msgs[0], "ещё работаю над ответом")
	assert.Contains(t, msgs[1], "долгий вопрос")
}

func TestALongTurnIsAnnouncedEvenWhenItIsNotTheFirstUpdateOfTheChat(t *testing.T) {
	h := newHarness(t, neverSettlesBeforeTheHardCap)
	h.startSession("main")

	h.send("/cr_help")
	h.sendAwaiting("долгий вопрос", 2)

	msgs := h.tg.messages()
	require.Len(t, msgs, 3)
	assert.Contains(t, msgs[1], "ещё работаю над ответом")
	assert.Contains(t, msgs[2], "долгий вопрос")
}

func TestASlowColdStartIsAnnouncedWhileTheSessionComesUp(t *testing.T) {
	h := newHarness(t, neverSettlesBeforeTheHardCap)

	h.sendAwaiting("привет", 2)

	msgs := h.tg.messages()
	require.NotEmpty(t, msgs)
	assert.Contains(t, msgs[0], "ещё запускается")
	assert.Contains(t, msgs[0], `"main"`)
}

func TestAFastTurnSendsNoInterimNotice(t *testing.T) {
	h := newHarness(t)
	h.startSession("main")

	h.send("быстрый вопрос")

	for _, msg := range h.tg.messages() {
		assert.NotContains(t, msg, "ещё работаю над ответом")
		assert.NotContains(t, msg, "ещё запускается")
	}
}
