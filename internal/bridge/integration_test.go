package bridge_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yabanci/claude-remote/internal/bridge"
	"github.com/yabanci/claude-remote/internal/config"
	"github.com/yabanci/claude-remote/internal/telegram"
)

func TestEndToEndAgainstRealTmux(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not installed")
	}

	session := fmt.Sprintf("claude-remote-e2e-%d", time.Now().UnixNano())
	t.Cleanup(func() {
		if liveExists(session) {
			_ = liveKill(session)
		}
	})

	var mu sync.Mutex
	var replies []string
	delivered := false

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.Contains(r.URL.Path, "getUpdates"):
			mu.Lock()
			var updates []telegram.Update
			if !delivered {
				delivered = true
				updates = []telegram.Update{{
					UpdateID: 1,
					Message: &telegram.Message{
						Chat: telegram.Chat{ID: 55},
						From: &telegram.User{ID: 55},
						Text: "echo integration-works",
					},
				}}
			}
			mu.Unlock()
			data, _ := json.Marshal(updates)
			_, _ = fmt.Fprintf(w, `{"ok":true,"result":%s}`, data)
		case strings.Contains(r.URL.Path, "sendMessage"):
			require.NoError(t, r.ParseForm())
			mu.Lock()
			replies = append(replies, r.FormValue("text"))
			mu.Unlock()
			_, _ = fmt.Fprint(w, `{"ok":true,"result":{}}`)
		default:
			_, _ = fmt.Fprint(w, `{"ok":true,"result":{}}`)
		}
	}))
	defer server.Close()

	cfg := config.Default()
	cfg.BotToken = "test-token"
	cfg.AllowedUsers = []int64{55}
	cfg.DefaultSession = session
	cfg.Sessions = map[string]config.SessionConfig{
		session: {Dir: t.TempDir(), Command: ""},
	}
	cfg.Settle.PollIntervalMS = 200
	cfg.Settle.StableRounds = 2
	cfg.Settle.HardCapSeconds = 20
	cfg.Settle.ColdStartDelayMS = 500
	cfg.Settle.PostSendDelayMS = 500

	tg := telegram.NewClient("test-token", telegram.WithBaseURL(server.URL))
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	b := bridge.New(cfg, filepath.Join(t.TempDir(), "config.yaml"), tg, bridge.NewTmuxRunner(), logger, t.TempDir())

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = b.Run(ctx)
	}()

	require.Eventually(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(replies) > 0
	}, 30*time.Second, 200*time.Millisecond, "no reply came back through the bridge")

	cancel()
	select {
	case <-done:
	case <-time.After(30 * time.Second):
		t.Fatal("bridge did not stop after cancel")
	}

	mu.Lock()
	defer mu.Unlock()
	joined := strings.Join(replies, "\n")
	assert.Contains(t, joined, "integration-works")
}
