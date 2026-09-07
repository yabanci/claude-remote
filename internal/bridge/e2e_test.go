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
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yabanci/claude-remote/internal/bridge"
	"github.com/yabanci/claude-remote/internal/config"
	"github.com/yabanci/claude-remote/internal/telegram"
	"github.com/yabanci/claude-remote/internal/tmux"
)

type liveHarness struct {
	t       *testing.T
	session string
	bridge  *bridge.Bridge

	mu      sync.Mutex
	replies []string
	queued  []telegram.Update
	served  int
}

func newLiveHarness(t *testing.T) *liveHarness {
	t.Helper()
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not installed")
	}

	lh := &liveHarness{
		t:       t,
		session: fmt.Sprintf("cr-live-%d", time.Now().UnixNano()),
	}
	t.Cleanup(func() {
		if tmux.Exists(lh.session) {
			_ = tmux.Kill(lh.session)
		}
	})

	server := httptest.NewServer(http.HandlerFunc(lh.serve))
	t.Cleanup(server.Close)

	cfg := config.Default()
	cfg.BotToken = "test-token"
	cfg.AllowedUsers = []int64{55}
	cfg.DefaultSession = lh.session
	cfg.Sessions = map[string]config.SessionConfig{
		lh.session: {Dir: t.TempDir(), Command: ""},
	}
	cfg.Settle.PollIntervalMS = 200
	cfg.Settle.StableRounds = 3
	cfg.Settle.HardCapSeconds = 30
	cfg.Settle.ColdStartDelayMS = 500
	cfg.Settle.PostSendDelayMS = 500

	tg := telegram.NewClient("test-token", telegram.WithBaseURL(server.URL))
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	lh.bridge = bridge.New(cfg, filepath.Join(t.TempDir(), "config.yaml"), tg,
		bridge.NewTmuxRunner(), logger, t.TempDir())
	return lh
}

func (lh *liveHarness) serve(w http.ResponseWriter, r *http.Request) {
	switch {
	case strings.Contains(r.URL.Path, "getUpdates"):
		lh.mu.Lock()
		var batch []telegram.Update
		if lh.served < len(lh.queued) {
			batch = []telegram.Update{lh.queued[lh.served]}
			lh.served++
		}
		lh.mu.Unlock()
		data, _ := json.Marshal(batch)
		_, _ = fmt.Fprintf(w, `{"ok":true,"result":%s}`, data)
	case strings.Contains(r.URL.Path, "sendMessage"):
		_ = r.ParseForm()
		lh.mu.Lock()
		lh.replies = append(lh.replies, r.FormValue("text"))
		lh.mu.Unlock()
		_, _ = fmt.Fprint(w, `{"ok":true,"result":{}}`)
	default:
		_, _ = fmt.Fprint(w, `{"ok":true,"result":{}}`)
	}
}

func (lh *liveHarness) queue(texts ...string) {
	lh.mu.Lock()
	defer lh.mu.Unlock()
	for i, text := range texts {
		lh.queued = append(lh.queued, telegram.Update{
			UpdateID: int64(i + 1),
			Message: &telegram.Message{
				Chat: telegram.Chat{ID: 55},
				From: &telegram.User{ID: 55},
				Text: text,
			},
		})
	}
}

func (lh *liveHarness) replyCount() int {
	lh.mu.Lock()
	defer lh.mu.Unlock()
	return len(lh.replies)
}

func (lh *liveHarness) allReplies() string {
	lh.mu.Lock()
	defer lh.mu.Unlock()
	return strings.Join(lh.replies, "\n")
}

func (lh *liveHarness) runUntil(wantReplies int, timeout time.Duration) {
	lh.t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = lh.bridge.Run(ctx)
	}()

	require.Eventually(lh.t, func() bool { return lh.replyCount() >= wantReplies },
		timeout, 200*time.Millisecond, "expected %d replies, got %d", wantReplies, lh.replyCount())
	cancel()

	select {
	case <-done:
	case <-time.After(30 * time.Second):
		lh.t.Fatal("bridge did not stop")
	}
}

func TestLiveOutputLongerThanVisiblePaneStillReachesTheUser(t *testing.T) {
	lh := newLiveHarness(t)
	lh.queue("for i in $(seq 1 200); do echo \"scrolled-line-$i\"; done")

	lh.runUntil(1, 60*time.Second)

	reply := lh.allReplies()
	assert.Contains(t, reply, "scrolled-line-200", "the tail of a long reply must arrive")
	assert.Contains(t, reply, "scrolled-line-150",
		"lines that scrolled out of the visible pane must still be diffed from scrollback")
}

func TestLiveSecondTurnDoesNotRepeatTheFirst(t *testing.T) {
	lh := newLiveHarness(t)
	lh.queue("echo first-turn-marker", "echo second-turn-marker")

	lh.runUntil(2, 60*time.Second)

	lh.mu.Lock()
	defer lh.mu.Unlock()
	last := lh.replies[len(lh.replies)-1]
	assert.Contains(t, last, "second-turn-marker")
	assert.NotContains(t, last, "first-turn-marker",
		"each reply must be the diff for its own turn, not the whole screen")
}

func TestLiveNoGoroutineLeakAcrossTurns(t *testing.T) {
	lh := newLiveHarness(t)
	before := runtime.NumGoroutine()
	lh.queue("echo turn-a", "echo turn-b", "echo turn-c")

	lh.runUntil(3, 90*time.Second)

	time.Sleep(500 * time.Millisecond)
	after := runtime.NumGoroutine()
	assert.LessOrEqual(t, after, before+2,
		"goroutines should not accumulate per handled message (before=%d after=%d)", before, after)
}
