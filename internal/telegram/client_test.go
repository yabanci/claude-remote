package telegram_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yabanci/claude-remote/internal/telegram"
)

func newTestClient(t *testing.T, handler http.HandlerFunc) *telegram.Client {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	return telegram.NewClient("test-token", telegram.WithBaseURL(server.URL))
}

func TestGetUpdates(t *testing.T) {
	client := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		assert.Contains(t, r.URL.Path, "/getUpdates")
		_, _ = fmt.Fprint(w, `{"ok":true,"result":[{"update_id":5,"message":{"message_id":1,"chat":{"id":100},"text":"hi"}}]}`)
	})

	updates, err := client.GetUpdates(context.Background(), 0, 1)

	require.NoError(t, err)
	require.Len(t, updates, 1)
	assert.Equal(t, int64(5), updates[0].UpdateID)
	assert.Equal(t, "hi", updates[0].Message.Text)
	assert.Equal(t, int64(100), updates[0].Message.Chat.ID)
}

func TestGetUpdatesAPIError(t *testing.T) {
	client := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprint(w, `{"ok":false,"description":"Unauthorized"}`)
	})

	_, err := client.GetUpdates(context.Background(), 0, 1)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "Unauthorized")
}

func TestSendMessagePostsChatIDAndText(t *testing.T) {
	var gotChatID, gotText string
	client := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		require.NoError(t, r.ParseForm())
		gotChatID = r.FormValue("chat_id")
		gotText = r.FormValue("text")
		_, _ = fmt.Fprint(w, `{"ok":true,"result":{}}`)
	})

	err := client.SendMessage(context.Background(), 42, "hello there")

	require.NoError(t, err)
	assert.Equal(t, "42", gotChatID)
	assert.Equal(t, "hello there", gotText)
}

func TestGetFile(t *testing.T) {
	client := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprint(w, `{"ok":true,"result":{"file_id":"abc","file_path":"documents/file_1.txt"}}`)
	})

	file, err := client.GetFile(context.Background(), "abc")

	require.NoError(t, err)
	assert.Equal(t, "documents/file_1.txt", file.FilePath)
}

func TestDownloadFile(t *testing.T) {
	client := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		assert.Contains(t, r.URL.Path, "/file/bottest-token/documents/file_1.txt")
		_, _ = fmt.Fprint(w, "file contents")
	})

	dest := filepath.Join(t.TempDir(), "nested", "out.txt")
	err := client.DownloadFile(context.Background(), "documents/file_1.txt", dest)

	require.NoError(t, err)
	data, err := os.ReadFile(dest)
	require.NoError(t, err)
	assert.Equal(t, "file contents", string(data))
}

func TestSendDocumentUploadsMultipart(t *testing.T) {
	var receivedFileName string
	client := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		require.NoError(t, r.ParseMultipartForm(1<<20))
		file, header, err := r.FormFile("document")
		require.NoError(t, err)
		defer func() { _ = file.Close() }()
		receivedFileName = header.Filename
		_, _ = fmt.Fprint(w, `{"ok":true,"result":{}}`)
	})

	src := filepath.Join(t.TempDir(), "report.txt")
	require.NoError(t, os.WriteFile(src, []byte("report body"), 0o600))

	err := client.SendDocument(context.Background(), 7, src)

	require.NoError(t, err)
	assert.Equal(t, "report.txt", receivedFileName)
}
