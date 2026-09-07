package bridge_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"path/filepath"
	"runtime/pprof"
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

	configPath string
	sessionDir string

	mu        sync.Mutex
	replies   []string
	documents []string
	keyboards [][]string
	queued    []telegram.Update
	served    int
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
	lh.sessionDir = t.TempDir()
	cfg.Sessions = map[string]config.SessionConfig{
		lh.session: {Dir: lh.sessionDir, Command: ""},
	}
	cfg.Settle.PollIntervalMS = 400
	cfg.Settle.StableRounds = 4
	cfg.Settle.HardCapSeconds = 45
	cfg.Settle.ColdStartDelayMS = 1000
	cfg.Settle.PostSendDelayMS = 1500

	tg := telegram.NewClient("test-token", telegram.WithBaseURL(server.URL))
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	lh.configPath = filepath.Join(t.TempDir(), "config.yaml")
	lh.bridge = bridge.New(cfg, lh.configPath, tg, bridge.NewTmuxRunner(), logger, t.TempDir())
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
	case strings.Contains(r.URL.Path, "getFile"):
		_, _ = fmt.Fprint(w, `{"ok":true,"result":{"file_id":"fid","file_path":"documents/upload.txt"}}`)
	case strings.Contains(r.URL.Path, "/file/bot"):
		_, _ = fmt.Fprint(w, "содержимое присланного файла")
	case strings.Contains(r.URL.Path, "sendDocument"):
		_ = r.ParseMultipartForm(1 << 20)
		_, header, err := r.FormFile("document")
		if err == nil {
			lh.mu.Lock()
			lh.documents = append(lh.documents, header.Filename)
			lh.mu.Unlock()
		}
		_, _ = fmt.Fprint(w, `{"ok":true,"result":{}}`)
	case strings.Contains(r.URL.Path, "sendMessage"):
		_ = r.ParseForm()
		lh.mu.Lock()
		lh.replies = append(lh.replies, r.FormValue("text"))
		if raw := r.FormValue("reply_markup"); raw != "" {
			var kb telegram.InlineKeyboard
			if json.Unmarshal([]byte(raw), &kb) == nil {
				var labels []string
				for _, row := range kb.Rows {
					for _, btn := range row {
						labels = append(labels, btn.Text)
					}
				}
				lh.keyboards = append(lh.keyboards, labels)
			}
		}
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

func (lh *liveHarness) queueDocument(fileName string) {
	lh.mu.Lock()
	defer lh.mu.Unlock()
	lh.queued = append(lh.queued, telegram.Update{
		UpdateID: int64(len(lh.queued) + 1),
		Message: &telegram.Message{
			Chat:     telegram.Chat{ID: 55},
			From:     &telegram.User{ID: 55},
			Document: &telegram.Document{FileID: "fid", FileName: fileName},
		},
	})
}

func (lh *liveHarness) sentDocuments() []string {
	lh.mu.Lock()
	defer lh.mu.Unlock()
	return append([]string(nil), lh.documents...)
}

func (lh *liveHarness) lastReply() string {
	lh.mu.Lock()
	defer lh.mu.Unlock()
	if len(lh.replies) == 0 {
		return ""
	}
	return lh.replies[len(lh.replies)-1]
}

func (lh *liveHarness) replyCount() int {
	lh.mu.Lock()
	defer lh.mu.Unlock()
	return len(lh.replies) + len(lh.documents)
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

func bridgeGoroutines(t *testing.T) int {
	t.Helper()
	var buf bytes.Buffer
	require.NoError(t, pprof.Lookup("goroutine").WriteTo(&buf, 1))
	return strings.Count(buf.String(), "claude-remote/internal/bridge.")
}

func TestLiveBridgeLeavesNoGoroutinesBehind(t *testing.T) {
	lh := newLiveHarness(t)

	lh.queue("echo turn-a", "echo turn-b", "echo turn-c")
	lh.runUntil(3, 120*time.Second)
	time.Sleep(500 * time.Millisecond)

	assert.Zero(t, bridgeGoroutines(t),
		"after Run returns, no bridge goroutine may still be alive")
}
