package telegram_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yabanci/claude-remote/internal/telegram"
)

func TestSetMyCommandsSendsTheListAsJSON(t *testing.T) {
	var raw string
	var path string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.NoError(t, r.ParseForm())
		raw = r.FormValue("commands")
		path = r.URL.Path
		_, _ = fmt.Fprint(w, `{"ok":true,"result":true}`)
	}))
	defer server.Close()
	client := telegram.NewClient("test-token", telegram.WithBaseURL(server.URL))

	err := client.SetMyCommands(context.Background(), []telegram.BotCommand{
		{Command: "cr_status", Description: "статус"},
		{Command: "cr_peek", Description: "показать экран"},
	})

	require.NoError(t, err)
	assert.Contains(t, path, "setMyCommands")

	var decoded []telegram.BotCommand
	require.NoError(t, json.Unmarshal([]byte(raw), &decoded))
	require.Len(t, decoded, 2)
	assert.Equal(t, "cr_status", decoded[0].Command)
	assert.Equal(t, "показать экран", decoded[1].Description)
}

func TestSetMyCommandsPropagatesRejection(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprint(w, `{"ok":false,"error_code":400,"description":"Bad Request: command name is invalid"}`)
	}))
	defer server.Close()
	client := telegram.NewClient("test-token", telegram.WithBaseURL(server.URL))

	err := client.SetMyCommands(context.Background(), []telegram.BotCommand{{Command: "Bad Name", Description: "x"}})

	require.Error(t, err, "a rejected command list means the user gets no menu at all")
	assert.Contains(t, err.Error(), "command name is invalid")
}
