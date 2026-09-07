package telegram_test

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yabanci/claude-remote/internal/telegram"
)

const secretToken = "7712345678:AAFAKE-TOKEN-abcdefghijklmnop"

func TestTransportErrorNeverCarriesTheToken(t *testing.T) {
	client := telegram.NewClient(secretToken,
		telegram.WithBaseURL("https://api.telegram.org.invalid"),
		telegram.WithRetryPolicy(0, func(time.Duration) {}))

	err := client.Send(context.Background(), 1, "текст", telegram.SendOptions{})

	require.Error(t, err)
	assert.NotContains(t, err.Error(), secretToken,
		"this error is formatted into a Telegram reply and into the log; the token is a key to the machine")
	assert.Contains(t, err.Error(), "/bot***")
}

func TestDownloadErrorNeverCarriesTheToken(t *testing.T) {
	client := telegram.NewClient(secretToken, telegram.WithBaseURL("https://api.telegram.org.invalid"))

	err := client.DownloadFile(context.Background(), "documents/x.txt", t.TempDir()+"/x.txt")

	require.Error(t, err)
	assert.NotContains(t, err.Error(), secretToken)
}

func TestSendDocumentErrorNeverCarriesTheToken(t *testing.T) {
	path := t.TempDir() + "/report.txt"
	require.NoError(t, os.WriteFile(path, []byte("body"), 0o600))

	client := telegram.NewClient(secretToken,
		telegram.WithBaseURL("https://api.telegram.org.invalid"),
		telegram.WithRetryPolicy(0, func(time.Duration) {}))

	err := client.SendDocument(context.Background(), 1, path)

	require.Error(t, err)
	assert.NotContains(t, err.Error(), secretToken)
	assert.False(t, strings.Contains(err.Error(), "AAFAKE"), "no fragment of the token may survive")
}
