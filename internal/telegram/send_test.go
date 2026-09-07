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

func capturingServer(t *testing.T, form *map[string]string, path *string) *telegram.Client {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.NoError(t, r.ParseForm())
		captured := map[string]string{}
		for k := range r.Form {
			captured[k] = r.FormValue(k)
		}
		*form = captured
		*path = r.URL.Path
		_, _ = fmt.Fprint(w, `{"ok":true,"result":{}}`)
	}))
	t.Cleanup(server.Close)
	return telegram.NewClient("test-token", telegram.WithBaseURL(server.URL))
}

func TestSendWithReplyToAndKeyboard(t *testing.T) {
	var form map[string]string
	var path string
	client := capturingServer(t, &form, &path)

	err := client.Send(context.Background(), 42, "выбери", telegram.SendOptions{
		ReplyTo: 7,
		Keyboard: &telegram.InlineKeyboard{Rows: [][]telegram.InlineButton{{
			{Text: "1. Yes", CallbackData: "1"},
			{Text: "2. No", CallbackData: "2"},
		}}},
	})

	require.NoError(t, err)
	assert.Contains(t, path, "sendMessage")
	assert.Equal(t, "42", form["chat_id"])
	assert.Equal(t, "выбери", form["text"])
	assert.Equal(t, "7", form["reply_to_message_id"])
	assert.Equal(t, "true", form["allow_sending_without_reply"])

	var kb telegram.InlineKeyboard
	require.NoError(t, json.Unmarshal([]byte(form["reply_markup"]), &kb))
	require.Len(t, kb.Rows, 1)
	assert.Equal(t, "1. Yes", kb.Rows[0][0].Text)
	assert.Equal(t, "2", kb.Rows[0][1].CallbackData)
}

func TestSendWithoutOptionsOmitsExtras(t *testing.T) {
	var form map[string]string
	var path string
	client := capturingServer(t, &form, &path)

	require.NoError(t, client.Send(context.Background(), 42, "просто текст", telegram.SendOptions{}))

	assert.NotContains(t, form, "reply_to_message_id")
	assert.NotContains(t, form, "reply_markup")
}

func TestSendChatAction(t *testing.T) {
	var form map[string]string
	var path string
	client := capturingServer(t, &form, &path)

	require.NoError(t, client.SendChatAction(context.Background(), 42, "typing"))

	assert.Contains(t, path, "sendChatAction")
	assert.Equal(t, "typing", form["action"])
}

func TestAnswerCallback(t *testing.T) {
	var form map[string]string
	var path string
	client := capturingServer(t, &form, &path)

	require.NoError(t, client.AnswerCallback(context.Background(), "cb-9", "отправил 2"))

	assert.Contains(t, path, "answerCallbackQuery")
	assert.Equal(t, "cb-9", form["callback_query_id"])
	assert.Equal(t, "отправил 2", form["text"])
}

func TestGetUpdatesDecodesCallbackQuery(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprint(w, `{"ok":true,"result":[{"update_id":9,"callback_query":{"id":"cb-1","data":"2","from":{"id":100},"message":{"message_id":5,"chat":{"id":1}}}}]}`)
	}))
	defer server.Close()
	client := telegram.NewClient("test-token", telegram.WithBaseURL(server.URL))

	updates, err := client.GetUpdates(context.Background(), 0, 1)

	require.NoError(t, err)
	require.Len(t, updates, 1)
	require.NotNil(t, updates[0].CallbackQuery)
	assert.Equal(t, "2", updates[0].CallbackQuery.Data)
	assert.Equal(t, int64(100), updates[0].CallbackQuery.From.ID)
	assert.Equal(t, int64(1), updates[0].CallbackQuery.Message.Chat.ID)
}
