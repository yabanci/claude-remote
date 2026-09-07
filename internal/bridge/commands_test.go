package bridge_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yabanci/claude-remote/internal/config"
	"github.com/yabanci/claude-remote/internal/telegram"
)

func messageFrom(text string) telegram.Message {
	return telegram.Message{
		Chat: telegram.Chat{ID: 1},
		From: &telegram.User{ID: 100},
		Text: text,
	}
}

func TestCrNewCreatesPersistsAndStartsSession(t *testing.T) {
	cfg := testConfig(t)
	projectDir := t.TempDir()
	runner := newFakeRunner()
	ft := &fakeTelegram{}
	tg := newFakeTelegramServer(t, ft)

	configPath := filepath.Join(t.TempDir(), "config.yaml")
	b := newTestBridgeAt(t, cfg, runner, tg, configPath)

	runOneUpdate(t, b, ft, messageFrom("/cr_new work "+projectDir))

	assert.Contains(t, ft.sent[len(ft.sent)-1], "создана и запущена")
	assert.True(t, runner.Exists("work"))

	saved, err := config.Load(configPath)
	require.NoError(t, err)
	assert.Equal(t, projectDir, saved.Sessions["work"].Dir)
}

func TestCrNewRejectsMissingDirectory(t *testing.T) {
	cfg := testConfig(t)
	runner := newFakeRunner()
	ft := &fakeTelegram{}
	tg := newFakeTelegramServer(t, ft)
	b := newTestBridge(t, cfg, runner, tg)

	runOneUpdate(t, b, ft, messageFrom("/cr_new work /definitely/not/here"))

	assert.Contains(t, ft.sent[len(ft.sent)-1], "не найдена")
	assert.False(t, runner.Exists("work"))
}

func TestCrNewRejectsDuplicateName(t *testing.T) {
	cfg := testConfig(t)
	runner := newFakeRunner()
	ft := &fakeTelegram{}
	tg := newFakeTelegramServer(t, ft)
	b := newTestBridge(t, cfg, runner, tg)

	runOneUpdate(t, b, ft, messageFrom("/cr_new main "+t.TempDir()))

	assert.Contains(t, ft.sent[len(ft.sent)-1], "уже существует")
}

func TestCrKillStopsRunningSession(t *testing.T) {
	cfg := testConfig(t)
	runner := newFakeRunner()
	require.NoError(t, runner.Start("main", cfg.Sessions["main"].Dir, "claude"))
	ft := &fakeTelegram{}
	tg := newFakeTelegramServer(t, ft)
	b := newTestBridge(t, cfg, runner, tg)

	runOneUpdate(t, b, ft, messageFrom("/cr_kill"))

	assert.Contains(t, ft.sent[len(ft.sent)-1], "остановлена")
	assert.False(t, runner.Exists("main"))
}

func TestCrKillOnStoppedSessionSaysSo(t *testing.T) {
	cfg := testConfig(t)
	runner := newFakeRunner()
	ft := &fakeTelegram{}
	tg := newFakeTelegramServer(t, ft)
	b := newTestBridge(t, cfg, runner, tg)

	runOneUpdate(t, b, ft, messageFrom("/cr_kill"))

	assert.Contains(t, ft.sent[len(ft.sent)-1], "не запущена")
}

func TestCrRestartRestartsSession(t *testing.T) {
	cfg := testConfig(t)
	runner := newFakeRunner()
	require.NoError(t, runner.Start("main", cfg.Sessions["main"].Dir, "claude"))
	ft := &fakeTelegram{}
	tg := newFakeTelegramServer(t, ft)
	b := newTestBridge(t, cfg, runner, tg)

	runOneUpdate(t, b, ft, messageFrom("/cr_restart"))

	assert.Contains(t, ft.sent[len(ft.sent)-1], "перезапущена")
	assert.True(t, runner.Exists("main"))
}

func TestCrStatusReportsRunningState(t *testing.T) {
	cfg := testConfig(t)
	runner := newFakeRunner()
	require.NoError(t, runner.Start("main", cfg.Sessions["main"].Dir, "claude"))
	ft := &fakeTelegram{}
	tg := newFakeTelegramServer(t, ft)
	b := newTestBridge(t, cfg, runner, tg)

	runOneUpdate(t, b, ft, messageFrom("/cr_status"))

	assert.Contains(t, ft.sent[len(ft.sent)-1], "работает")
}

func TestCrSessionsListsConfiguredSessions(t *testing.T) {
	cfg := testConfig(t)
	cfg.Sessions["work"] = config.SessionConfig{Dir: t.TempDir(), Command: "claude"}
	runner := newFakeRunner()
	ft := &fakeTelegram{}
	tg := newFakeTelegramServer(t, ft)
	b := newTestBridge(t, cfg, runner, tg)

	runOneUpdate(t, b, ft, messageFrom("/cr_sessions"))

	last := ft.sent[len(ft.sent)-1]
	assert.Contains(t, last, "main")
	assert.Contains(t, last, "work")
	assert.Contains(t, last, "[active]")
}

func TestCrHelpListsCommands(t *testing.T) {
	cfg := testConfig(t)
	runner := newFakeRunner()
	ft := &fakeTelegram{}
	tg := newFakeTelegramServer(t, ft)
	b := newTestBridge(t, cfg, runner, tg)

	runOneUpdate(t, b, ft, messageFrom("/cr_help"))

	assert.Contains(t, ft.sent[len(ft.sent)-1], "/cr_status")
}

func TestUnknownBridgeCommandIsReported(t *testing.T) {
	cfg := testConfig(t)
	runner := newFakeRunner()
	ft := &fakeTelegram{}
	tg := newFakeTelegramServer(t, ft)
	b := newTestBridge(t, cfg, runner, tg)

	runOneUpdate(t, b, ft, messageFrom("/cr_nonsense"))

	assert.Contains(t, ft.sent[len(ft.sent)-1], "неизвестная команда")
}

func TestCrSendReportsMissingFile(t *testing.T) {
	cfg := testConfig(t)
	runner := newFakeRunner()
	ft := &fakeTelegram{}
	tg := newFakeTelegramServer(t, ft)
	b := newTestBridge(t, cfg, runner, tg)

	runOneUpdate(t, b, ft, messageFrom("/cr_send nope.txt"))

	assert.Contains(t, ft.sent[len(ft.sent)-1], "файл не найден")
}

func TestCrSendResolvesPathRelativeToSessionDir(t *testing.T) {
	cfg := testConfig(t)
	sessionDir := cfg.Sessions["main"].Dir
	require.NoError(t, os.WriteFile(filepath.Join(sessionDir, "report.txt"), []byte("body"), 0o600))

	runner := newFakeRunner()
	ft := &fakeTelegram{}
	tg := newFakeTelegramServer(t, ft)
	b := newTestBridge(t, cfg, runner, tg)

	runOneUpdate(t, b, ft, messageFrom("/cr_send report.txt"))

	assert.Equal(t, []string{"report.txt"}, ft.docs)
	assert.Empty(t, ft.sent)
}
