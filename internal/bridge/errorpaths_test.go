package bridge_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yabanci/claude-remote/internal/bridge"
	"github.com/yabanci/claude-remote/internal/config"
	"github.com/yabanci/claude-remote/internal/telegram"
)

type failingRunner struct {
	*fakeRunner
	killErr      error
	startErr     error
	interruptErr error
}

func (f *failingRunner) Kill(session string) error {
	if f.killErr != nil {
		return f.killErr
	}
	return f.fakeRunner.Kill(session)
}

func (f *failingRunner) Start(session, dir, command string) error {
	if f.startErr != nil {
		return f.startErr
	}
	return f.fakeRunner.Start(session, dir, command)
}

func (f *failingRunner) Interrupt(session string) error {
	if f.interruptErr != nil {
		return f.interruptErr
	}
	return f.fakeRunner.Interrupt(session)
}

func TestCrRestartReportsAFailedStop(t *testing.T) {
	cfg := testConfigFor(t)
	runner := &failingRunner{fakeRunner: newFakeRunner(), killErr: errors.New("tmux server unreachable")}
	require.NoError(t, runner.Start("main", cfg.Sessions["main"].Dir, "claude"))
	h := newHarnessWithRunner(t, cfg, runner)

	h.send("/cr_restart")

	assert.Contains(t, h.lastMessage(), "не удалось остановить")
	assert.Contains(t, h.lastMessage(), "tmux server unreachable")
}

func TestCrRestartReportsAFailedStart(t *testing.T) {
	cfg := testConfigFor(t)
	runner := &failingRunner{fakeRunner: newFakeRunner(), startErr: errors.New("no such directory")}
	h := newHarnessWithRunner(t, cfg, runner)

	h.send("/cr_restart")

	assert.Contains(t, h.lastMessage(), "не удалось запустить")
}

func TestCrInterruptReportsFailure(t *testing.T) {
	cfg := testConfigFor(t)
	runner := &failingRunner{fakeRunner: newFakeRunner(), interruptErr: errors.New("pane is gone")}
	require.NoError(t, runner.Start("main", cfg.Sessions["main"].Dir, "claude"))
	h := newHarnessWithRunner(t, cfg, runner)

	h.send("/cr_interrupt")

	assert.Contains(t, h.lastMessage(), "не удалось прервать")
}

func TestCrKillReportsFailure(t *testing.T) {
	cfg := testConfigFor(t)
	runner := &failingRunner{fakeRunner: newFakeRunner(), killErr: errors.New("permission denied")}
	require.NoError(t, runner.Start("main", cfg.Sessions["main"].Dir, "claude"))
	h := newHarnessWithRunner(t, cfg, runner)

	h.send("/cr_kill")

	assert.Contains(t, h.lastMessage(), "не удалось остановить")
}

func TestCrNewRollsBackConfigEntryWhenStartFails(t *testing.T) {
	cfg := testConfigFor(t)
	runner := &failingRunner{fakeRunner: newFakeRunner(), startErr: errors.New("no such directory")}
	h := newHarnessWithRunner(t, cfg, runner)
	projectDir := t.TempDir()

	h.send("/cr_new work " + projectDir)

	assert.Contains(t, h.lastMessage(), "не запустилась")
	assert.False(t, runner.Exists("work"))

	saved, err := config.Load(h.configPath)
	require.NoError(t, err)
	_, exists := saved.Sessions["work"]
	assert.False(t, exists, "a session whose Start failed must not stay in the saved config")

	h.send("/cr_status")
	assert.NotContains(t, h.lastMessage(), "work", "a rolled-back session must not still be the active one")
}

type offsetAwareTelegram struct {
	mu             sync.Mutex
	deliveries     int
	replies        int
	requestedFirst string
}

func (o *offsetAwareTelegram) serve(w http.ResponseWriter, r *http.Request) {
	_ = r.ParseForm()
	switch {
	case r.URL.Path[len(r.URL.Path)-10:] == "getUpdates":
		offset := r.FormValue("offset")
		o.mu.Lock()
		if o.requestedFirst == "" {
			o.requestedFirst = offset
		}
		var batch []telegram.Update
		if offset == "" || offset == "0" {
			o.deliveries++
			batch = []telegram.Update{{
				UpdateID: 41,
				Message: &telegram.Message{
					Chat: telegram.Chat{ID: 1},
					From: &telegram.User{ID: testUserID},
					Text: "привет",
				},
			}}
		}
		o.mu.Unlock()
		data, _ := json.Marshal(batch)
		_, _ = fmt.Fprintf(w, `{"ok":true,"result":%s}`, data)
	default:
		o.mu.Lock()
		o.replies++
		o.mu.Unlock()
		_, _ = fmt.Fprint(w, `{"ok":true,"result":{}}`)
	}
}

func TestARestartedBridgeDoesNotReplayHandledMessages(t *testing.T) {
	fake := &offsetAwareTelegram{}
	server := httptest.NewServer(http.HandlerFunc(fake.serve))
	defer server.Close()

	stateDir := t.TempDir()
	cfg := testConfigFor(t)
	runner := newFakeRunner()
	require.NoError(t, runner.Start("main", cfg.Sessions["main"].Dir, "claude"))
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	configPath := filepath.Join(t.TempDir(), "config.yaml")

	runOnce := func() {
		tg := telegram.NewClient("test-token", telegram.WithBaseURL(server.URL))
		b := bridge.New(cfg, configPath, tg, runner, logger, stateDir)
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan struct{})
		go func() {
			defer close(done)
			_ = b.Run(ctx)
		}()
		time.Sleep(700 * time.Millisecond)
		cancel()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Fatal("bridge did not stop")
		}
	}

	runOnce()
	fake.mu.Lock()
	afterFirst := fake.deliveries
	fake.mu.Unlock()
	require.Equal(t, 1, afterFirst, "the message must be handled once")

	fake.mu.Lock()
	fake.requestedFirst = ""
	fake.mu.Unlock()

	runOnce()

	fake.mu.Lock()
	defer fake.mu.Unlock()
	assert.Equal(t, "42", fake.requestedFirst,
		"a restarted bridge must resume past the handled message, not from zero")
	assert.Equal(t, 1, fake.deliveries,
		"without a persisted offset the same message would be executed again after every restart")
}

func TestAnUnwritableStateDirDoesNotStopTheBridgeAnswering(t *testing.T) {
	blocked := filepath.Join(t.TempDir(), "state")
	require.NoError(t, os.WriteFile(blocked, []byte("this is a file, not a directory"), 0o600))

	cfg := testConfigFor(t)
	runner := newFakeRunner()
	require.NoError(t, runner.Start("main", cfg.Sessions["main"].Dir, "claude"))

	ft := &fakeTelegram{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ft.handle(t, w, r)
	}))
	defer server.Close()

	tg := telegram.NewClient("test-token", telegram.WithBaseURL(server.URL))
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	b := bridge.New(cfg, filepath.Join(t.TempDir(), "config.yaml"), tg, runner, logger, blocked)

	ft.updates = []telegram.Update{{UpdateID: 1, Message: &telegram.Message{
		Chat: telegram.Chat{ID: 1},
		From: &telegram.User{ID: testUserID},
		Text: "привет",
	}}}

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
		t.Fatal("bridge did not stop")
	}

	assert.NotEmpty(t, ft.messages(),
		"a state dir it cannot write must cost replay after a restart, not the ability to answer now")
}
