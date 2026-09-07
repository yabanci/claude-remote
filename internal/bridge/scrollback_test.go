package bridge_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func paneWithDismissedTrustDialogInHistory() string {
	return realTrustDialog + "\n" +
		strings.Repeat("⏺ обычный ответ сессии\n\n", 20) +
		"❯ "
}

func TestATrustDialogLeftInScrollbackDoesNotBlockTheSessionForever(t *testing.T) {
	h := newHarness(t).startSession("main")
	h.runner.setPane("main", paneWithDismissedTrustDialogInHistory())

	h.send("посчитай два плюс два")

	assert.NotContains(t, h.lastMessage(), "ждёт подтверждения доверия",
		"the dialog was confirmed long ago and only survives in scrollback; the session is usable")
	assert.NotEmpty(t, h.runner.lastSentKeys(), "the message must actually reach the session")
}

func TestAnOldMenuInScrollbackDoesNotTurnEveryReplyIntoButtons(t *testing.T) {
	h := newHarness(t).startSession("main")
	h.runner.setPane("main", realChoiceDialog+"\n"+strings.Repeat("⏺ answered long ago\n\n", 20)+"❯ ")

	h.send("расскажи что-нибудь")

	require.Empty(t, h.tg.keyboards(),
		"a menu that scrolled out of view must not be re-offered on every later turn")
}
