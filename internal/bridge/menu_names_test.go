package bridge_test

import (
	"regexp"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var telegramCommandName = regexp.MustCompile(`^/[a-z0-9_]{1,32}$`)

func TestEveryRegisteredCommandNameIsValidForTelegram(t *testing.T) {
	h := newHarness(t)

	h.send("/cr_help")

	names := regexp.MustCompile(`/[A-Za-z0-9_]+`).FindAllString(h.lastMessage(), -1)
	require.NotEmpty(t, names, "help must list the commands it registers")
	for _, name := range names {
		assert.Regexp(t, telegramCommandName, name,
			"Telegram rejects the whole setMyCommands call on one bad name, leaving no menu at all")
	}
}
