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

	"github.com/stretchr/testify/require"

	"github.com/yabanci/claude-remote/internal/bridge"
	"github.com/yabanci/claude-remote/internal/config"
	"github.com/yabanci/claude-remote/internal/telegram"
)

const testUserID = 100

type fakeRunner struct {
	mu           sync.Mutex
	sessions     map[string]string
	panes        map[string]string
	sentKeys     []string
	replyPayload string
	captureErr   error
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
	reply := f.replyPayload
	if reply == "" {
		reply = "reply to: " + text
	}
	f.panes[session] += "\n> " + text + "\n" + reply
	return nil
}

func (f *fakeRunner) Interrupt(session string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.panes[session] += "\n^C"
	return nil
}

const fakeScreenHeight = 24

func (f *fakeRunner) CapturePane(session string, historyLines int) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.captureErr != nil {
		return "", f.captureErr
	}
	pane := f.panes[session]
	if historyLines > 0 {
		return pane, nil
	}

	lines := strings.Split(pane, "\n")
	if len(lines) > fakeScreenHeight {
		lines = lines[len(lines)-fakeScreenHeight:]
	}
	return strings.Join(lines, "\n"), nil
}

func (f *fakeRunner) lastSentKeys() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.sentKeys) == 0 {
		return ""
	}
	return f.sentKeys[len(f.sentKeys)-1]
}

func (f *fakeRunner) setPane(session, content string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.panes[session] = content
}

func (f *fakeRunner) pane(session string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.panes[session]
}

type fakeTelegram struct {
	mu             sync.Mutex
	sent           []string
	docs           []string
	keyboardsL     [][]string
	keyboardRows   [][][]string
	callbacks      []string
	typing         int
	replyTos       []int64
	updates        []telegram.Update
	nextCall       int
	rejectMarkup   bool
	rejectGetFile  bool
	rejectDownload bool
}

func (f *fakeTelegram) keyboards() [][]string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([][]string(nil), f.keyboardsL...)
}

func (f *fakeTelegram) rowsOfLastKeyboard() [][]string {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.keyboardRows) == 0 {
		return nil
	}
	return f.keyboardRows[len(f.keyboardRows)-1]
}

func (f *fakeTelegram) failEveryKeyboardSend() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.rejectMarkup = true
}

func (f *fakeTelegram) failGetFile() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.rejectGetFile = true
}

func (f *fakeTelegram) failDownload() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.rejectDownload = true
}

func (f *fakeTelegram) answeredCallbacks() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.callbacks...)
}

func (f *fakeTelegram) typingActions() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.typing
}

func (f *fakeTelegram) firstReplyTo() int64 {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.replyTos) == 0 {
		return 0
	}
	return f.replyTos[0]
}

func (f *fakeTelegram) messages() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.sent...)
}

func (f *fakeTelegram) documents() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.docs...)
}

func (f *fakeTelegram) replyCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.sent) + len(f.docs)
}

func (f *fakeTelegram) enqueue(update telegram.Update) {
	f.mu.Lock()
	defer f.mu.Unlock()
	update.UpdateID = int64(len(f.updates) + 1)
	f.updates = append(f.updates, update)
}

func (f *fakeTelegram) handle(t *testing.T, w http.ResponseWriter, r *http.Request) {
	t.Helper()
	switch {
	case strings.Contains(r.URL.Path, "getUpdates"):
		f.mu.Lock()
		var result []telegram.Update
		if f.nextCall < len(f.updates) {
			result = []telegram.Update{f.updates[f.nextCall]}
			f.nextCall++
		}
		f.mu.Unlock()
		data, _ := json.Marshal(result)
		_, _ = fmt.Fprintf(w, `{"ok":true,"result":%s}`, data)
	case strings.Contains(r.URL.Path, "getFile"):
		f.mu.Lock()
		reject := f.rejectGetFile
		f.mu.Unlock()
		if reject {
			_, _ = fmt.Fprint(w, `{"ok":false,"error_code":400,"description":"file not found"}`)
			return
		}
		_, _ = fmt.Fprint(w, `{"ok":true,"result":{"file_id":"fid","file_path":"documents/upload.txt"}}`)
	case strings.Contains(r.URL.Path, "/file/bot"):
		f.mu.Lock()
		reject := f.rejectDownload
		f.mu.Unlock()
		if reject {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_, _ = fmt.Fprint(w, "uploaded file body")
	case strings.Contains(r.URL.Path, "sendChatAction"):
		f.mu.Lock()
		f.typing++
		f.mu.Unlock()
		_, _ = fmt.Fprint(w, `{"ok":true,"result":true}`)
	case strings.Contains(r.URL.Path, "answerCallbackQuery"):
		require.NoError(t, r.ParseForm())
		f.mu.Lock()
		f.callbacks = append(f.callbacks, r.FormValue("callback_query_id"))
		f.mu.Unlock()
		_, _ = fmt.Fprint(w, `{"ok":true,"result":true}`)
	case strings.Contains(r.URL.Path, "sendMessage"):
		require.NoError(t, r.ParseForm())
		markup := r.FormValue("reply_markup")
		f.mu.Lock()
		if markup != "" && f.rejectMarkup {
			f.mu.Unlock()
			w.WriteHeader(http.StatusBadRequest)
			_, _ = fmt.Fprint(w, `{"ok":false,"description":"Bad Request: BUTTON_DATA_INVALID"}`)
			return
		}
		f.sent = append(f.sent, r.FormValue("text"))
		if markup != "" {
			var kb telegram.InlineKeyboard
			require.NoError(t, json.Unmarshal([]byte(markup), &kb))
			var labels []string
			var rows [][]string
			for _, row := range kb.Rows {
				var rowLabels []string
				for _, btn := range row {
					labels = append(labels, btn.Text)
					rowLabels = append(rowLabels, btn.Text)
				}
				rows = append(rows, rowLabels)
			}
			f.keyboardsL = append(f.keyboardsL, labels)
			f.keyboardRows = append(f.keyboardRows, rows)
		}
		if to := r.FormValue("reply_to_message_id"); to != "" {
			var id int64
			_, _ = fmt.Sscanf(to, "%d", &id)
			f.replyTos = append(f.replyTos, id)
		}
		f.mu.Unlock()
		_, _ = fmt.Fprint(w, `{"ok":true,"result":{}}`)
	case strings.Contains(r.URL.Path, "sendDocument"):
		require.NoError(t, r.ParseMultipartForm(1<<20))
		_, header, err := r.FormFile("document")
		require.NoError(t, err)
		f.mu.Lock()
		f.docs = append(f.docs, header.Filename)
		f.mu.Unlock()
		_, _ = fmt.Fprint(w, `{"ok":true,"result":{}}`)
	default:
		_, _ = fmt.Fprint(w, `{"ok":true,"result":{}}`)
	}
}

type harness struct {
	t          *testing.T
	cfg        config.Config
	configPath string
	runner     *fakeRunner
	tg         *fakeTelegram
	bridge     *bridge.Bridge
}

func testConfigFor(t *testing.T) config.Config {
	t.Helper()
	cfg := config.Default()
	cfg.BotToken = "x"
	cfg.AllowedUsers = []int64{testUserID}
	cfg.Settle.PollIntervalMS = 5
	cfg.Settle.StableRounds = 2
	cfg.Settle.HardCapSeconds = 1
	cfg.Settle.ColdStartDelayMS = 5
	cfg.Settle.PostSendDelayMS = 5
	cfg.Sessions["main"] = config.SessionConfig{Dir: t.TempDir(), Command: "claude"}
	return cfg
}

func newTestTelegramClient(baseURL string) *telegram.Client {
	return telegram.NewClient("test-token", telegram.WithBaseURL(baseURL),
		telegram.WithMaxRetries(0))
}

func newHarnessWithRunner(t *testing.T, cfg config.Config, runner bridge.Runner) *harness {
	t.Helper()

	ft := &fakeTelegram{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ft.handle(t, w, r)
	}))
	t.Cleanup(server.Close)

	configPath := filepath.Join(t.TempDir(), "config.yaml")
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	tg := newTestTelegramClient(server.URL)

	return &harness{
		t:          t,
		cfg:        cfg,
		configPath: configPath,
		runner:     newFakeRunner(),
		tg:         ft,
		bridge:     bridge.New(cfg, configPath, tg, runner, logger, t.TempDir()),
	}
}

func newHarness(t *testing.T, tweaks ...func(*config.Config)) *harness {
	t.Helper()

	cfg := config.Default()
	cfg.BotToken = "x"
	cfg.AllowedUsers = []int64{testUserID}
	cfg.Settle.PollIntervalMS = 5
	cfg.Settle.StableRounds = 2
	cfg.Settle.HardCapSeconds = 1
	cfg.Settle.ColdStartDelayMS = 5
	cfg.Settle.PostSendDelayMS = 5
	cfg.Sessions["main"] = config.SessionConfig{Dir: t.TempDir(), Command: "claude"}
	for _, tweak := range tweaks {
		tweak(&cfg)
	}

	ft := &fakeTelegram{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ft.handle(t, w, r)
	}))
	t.Cleanup(server.Close)

	configPath := filepath.Join(t.TempDir(), "config.yaml")
	runner := newFakeRunner()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	tg := newTestTelegramClient(server.URL)

	return &harness{
		t:          t,
		cfg:        cfg,
		configPath: configPath,
		runner:     runner,
		tg:         ft,
		bridge:     bridge.New(cfg, configPath, tg, runner, logger, t.TempDir()),
	}
}

func (h *harness) sessionDir(name string) string {
	return h.cfg.Sessions[name].Dir
}

func (h *harness) startSession(name string) *harness {
	h.t.Helper()
	require.NoError(h.t, h.runner.Start(name, h.sessionDir(name), "claude"))
	return h
}

func (h *harness) userMessage(text string) telegram.Message {
	return telegram.Message{
		Chat: telegram.Chat{ID: 1},
		From: &telegram.User{ID: testUserID},
		Text: text,
	}
}

func (h *harness) send(text string) {
	h.t.Helper()
	h.deliver(h.userMessage(text))
}

func (h *harness) deliver(msg telegram.Message) {
	h.t.Helper()
	h.tg.enqueue(telegram.Update{Message: &msg})
	h.runUntilReply()
}

func (h *harness) deliverCallback(data string) {
	h.t.Helper()
	h.tg.enqueue(telegram.Update{
		CallbackQuery: &telegram.CallbackQuery{
			ID:   "cb-1",
			From: &telegram.User{ID: testUserID},
			Data: data,
			Message: &telegram.Message{
				MessageID: 77,
				Chat:      telegram.Chat{ID: 1},
			},
		},
	})
	h.runUntilReplies(1)
}

func (h *harness) runUntilReply() {
	h.t.Helper()
	h.runUntilReplies(1)
}

func (h *harness) runUntilReplies(want int) {
	h.t.Helper()
	before := h.tg.replyCount()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = h.bridge.Run(ctx)
	}()

	waitUntil(h.t, func() bool { return h.tg.replyCount() >= before+want })
	cancel()
	h.awaitStop(done)
}

func (h *harness) sendAwaiting(text string, wantReplies int) {
	h.t.Helper()
	msg := h.userMessage(text)
	h.tg.enqueue(telegram.Update{Message: &msg})
	h.runUntilReplies(wantReplies)
}

func (h *harness) runBriefly() {
	h.t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = h.bridge.Run(ctx)
	}()
	time.Sleep(50 * time.Millisecond)
	cancel()
	h.awaitStop(done)
}

func (h *harness) awaitStop(done <-chan struct{}) {
	h.t.Helper()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		h.t.Fatal("bridge did not stop after cancel")
	}
}

func (h *harness) lastMessage() string {
	h.t.Helper()
	msgs := h.tg.messages()
	require.NotEmpty(h.t, msgs, "expected at least one message reply")
	return msgs[len(msgs)-1]
}

func waitUntil(t *testing.T, cond func() bool) {
	t.Helper()
	for i := 0; i < 500; i++ {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("condition not met in time")
}
