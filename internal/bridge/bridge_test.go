package bridge_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
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

type fakeRunner struct {
	mu       sync.Mutex
	sessions map[string]string
	panes    map[string]string
	sentKeys []string
}

func newFakeRunner() *fakeRunner {
	return &fakeRunner{sessions: map[string]string{}, panes: map[string]string{}}
}

func (f *fakeRunner) Exists(session string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	_, ok := f.sessions[session]
	return ok
}

func (f *fakeRunner) Start(session, dir, command string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sessions[session] = dir
	f.panes[session] = "$ " + command
	return nil
}

func (f *fakeRunner) Kill(session string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.sessions, session)
	delete(f.panes, session)
	return nil
}

func (f *fakeRunner) SendKeys(session, text string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sentKeys = append(f.sentKeys, text)
	f.panes[session] += "\n> " + text + "\nreply to: " + text
	return nil
}

func (f *fakeRunner) Interrupt(session string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.panes[session] += "\n^C"
	return nil
}

func (f *fakeRunner) CapturePane(session string, historyLines int) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.panes[session], nil
}

type fakeTelegram struct {
	mu       sync.Mutex
	sent     []string
	docs     []string
	updates  []telegram.Update
	nextCall int
}

func (f *fakeTelegram) replyCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.sent) + len(f.docs)
}

func newFakeTelegramServer(t *testing.T, ft *fakeTelegram) *telegram.Client {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.Contains(r.URL.Path, "getUpdates"):
			ft.mu.Lock()
			var result []telegram.Update
			if ft.nextCall < len(ft.updates) {
				result = []telegram.Update{ft.updates[ft.nextCall]}
			}
			ft.nextCall++
			ft.mu.Unlock()
			data, _ := json.Marshal(result)
			_, _ = fmt.Fprintf(w, `{"ok":true,"result":%s}`, data)
		case strings.Contains(r.URL.Path, "sendMessage"):
			require.NoError(t, r.ParseForm())
			ft.mu.Lock()
			ft.sent = append(ft.sent, r.FormValue("text"))
			ft.mu.Unlock()
			_, _ = fmt.Fprint(w, `{"ok":true,"result":{}}`)
		case strings.Contains(r.URL.Path, "sendDocument"):
			require.NoError(t, r.ParseMultipartForm(1<<20))
			_, header, err := r.FormFile("document")
			require.NoError(t, err)
			ft.mu.Lock()
			ft.docs = append(ft.docs, header.Filename)
			ft.mu.Unlock()
			_, _ = fmt.Fprint(w, `{"ok":true,"result":{}}`)
		default:
			_, _ = fmt.Fprint(w, `{"ok":true,"result":{}}`)
		}
	}))
	t.Cleanup(server.Close)
	return telegram.NewClient("test-token", telegram.WithBaseURL(server.URL))
}

func testConfig(t *testing.T) config.Config {
	cfg := config.Default()
	cfg.BotToken = "x"
	cfg.AllowedUsers = []int64{100}
	cfg.Settle.PollIntervalMS = 5
	cfg.Settle.StableRounds = 2
	cfg.Settle.HardCapSeconds = 1
	cfg.Settle.ColdStartDelayMS = 5
	cfg.Settle.PostSendDelayMS = 5
	cfg.Sessions["main"] = config.SessionConfig{Dir: t.TempDir(), Command: "claude"}
	return cfg
}

func newTestBridge(t *testing.T, cfg config.Config, runner bridge.Runner, tg *telegram.Client) *bridge.Bridge {
	t.Helper()
	return newTestBridgeAt(t, cfg, runner, tg, filepath.Join(t.TempDir(), "config.yaml"))
}

func newTestBridgeAt(t *testing.T, cfg config.Config, runner bridge.Runner, tg *telegram.Client, configPath string) *bridge.Bridge {
	t.Helper()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	return bridge.New(cfg, configPath, tg, runner, logger, t.TempDir())
}

func runOneUpdate(t *testing.T, b *bridge.Bridge, ft *fakeTelegram, msg telegram.Message) {
	t.Helper()
	ft.updates = []telegram.Update{{UpdateID: 1, Message: &msg}}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = b.Run(ctx)
	}()

	waitUntil(t, func() bool { return ft.replyCount() > 0 })
	cancel()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("bridge did not stop after cancel")
	}
}

func waitUntil(t *testing.T, cond func() bool) {
	t.Helper()
	for i := 0; i < 200; i++ {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("condition not met in time")
}

func TestForwardToSessionRepliesWithDiff(t *testing.T) {
	cfg := testConfig(t)
	runner := newFakeRunner()
	require.NoError(t, runner.Start("main", cfg.Sessions["main"].Dir, "claude"))
	ft := &fakeTelegram{}
	tg := newFakeTelegramServer(t, ft)
	b := newTestBridge(t, cfg, runner, tg)

	runOneUpdate(t, b, ft, telegram.Message{
		Chat: telegram.Chat{ID: 1},
		From: &telegram.User{ID: 100},
		Text: "what is 2+2",
	})

	require.NotEmpty(t, ft.sent)
	assert.Contains(t, ft.sent[len(ft.sent)-1], "reply to: what is 2+2")
}

func TestBootstrapsToFirstSenderWhenAllowedUsersEmpty(t *testing.T) {
	cfg := testConfig(t)
	cfg.AllowedUsers = nil
	runner := newFakeRunner()
	ft := &fakeTelegram{}
	tg := newFakeTelegramServer(t, ft)
	b := newTestBridge(t, cfg, runner, tg)

	runOneUpdate(t, b, ft, telegram.Message{
		Chat: telegram.Chat{ID: 1},
		From: &telegram.User{ID: 777},
		Text: "hello",
	})

	assert.Contains(t, ft.sent[0], "777")
}

func TestUnauthorizedSenderIsIgnored(t *testing.T) {
	cfg := testConfig(t)
	runner := newFakeRunner()
	ft := &fakeTelegram{}
	tg := newFakeTelegramServer(t, ft)
	b := newTestBridge(t, cfg, runner, tg)

	ft.updates = []telegram.Update{{UpdateID: 1, Message: &telegram.Message{
		Chat: telegram.Chat{ID: 1},
		From: &telegram.User{ID: 999},
		Text: "let me in",
	}}}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = b.Run(ctx)
	}()
	time.Sleep(50 * time.Millisecond)
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("bridge did not stop after cancel")
	}

	assert.Empty(t, ft.sent)
	assert.False(t, runner.Exists("main"))
}

func TestCrUseSwitchesActiveSession(t *testing.T) {
	cfg := testConfig(t)
	cfg.Sessions["work"] = config.SessionConfig{Dir: t.TempDir(), Command: "claude"}
	runner := newFakeRunner()
	ft := &fakeTelegram{}
	tg := newFakeTelegramServer(t, ft)
	b := newTestBridge(t, cfg, runner, tg)

	runOneUpdate(t, b, ft, telegram.Message{
		Chat: telegram.Chat{ID: 1},
		From: &telegram.User{ID: 100},
		Text: "/cr_use work",
	})

	assert.Contains(t, ft.sent[len(ft.sent)-1], "work")
}

func TestCrUseUnknownSessionReportsError(t *testing.T) {
	cfg := testConfig(t)
	runner := newFakeRunner()
	ft := &fakeTelegram{}
	tg := newFakeTelegramServer(t, ft)
	b := newTestBridge(t, cfg, runner, tg)

	runOneUpdate(t, b, ft, telegram.Message{
		Chat: telegram.Chat{ID: 1},
		From: &telegram.User{ID: 100},
		Text: "/cr_use ghost",
	})

	assert.Contains(t, ft.sent[len(ft.sent)-1], "не найдена")
}

func TestCrInterruptSendsCtrlC(t *testing.T) {
	cfg := testConfig(t)
	runner := newFakeRunner()
	require.NoError(t, runner.Start("main", cfg.Sessions["main"].Dir, "claude"))
	ft := &fakeTelegram{}
	tg := newFakeTelegramServer(t, ft)
	b := newTestBridge(t, cfg, runner, tg)

	runOneUpdate(t, b, ft, telegram.Message{
		Chat: telegram.Chat{ID: 1},
		From: &telegram.User{ID: 100},
		Text: "/cr_interrupt",
	})

	assert.Contains(t, runner.panes["main"], "^C")
}
