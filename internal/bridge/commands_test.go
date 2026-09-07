package bridge_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yabanci/claude-remote/internal/config"
)

func TestCrNewCreatesPersistsAndStartsSession(t *testing.T) {
	h := newHarness(t)
	projectDir := t.TempDir()

	h.send("/cr_new work " + projectDir)

	assert.Contains(t, h.lastMessage(), "создана и запущена")
	assert.True(t, h.runner.Exists("work"))

	saved, err := config.Load(h.configPath)
	require.NoError(t, err)
	assert.Equal(t, projectDir, saved.Sessions["work"].Dir)
}

func TestCrNewRejectsMissingDirectory(t *testing.T) {
	h := newHarness(t)

	h.send("/cr_new work /definitely/not/here")

	assert.Contains(t, h.lastMessage(), "не найдена")
	assert.False(t, h.runner.Exists("work"))
}

func TestCrNewRejectsDuplicateName(t *testing.T) {
	h := newHarness(t)

	h.send("/cr_new main " + t.TempDir())

	assert.Contains(t, h.lastMessage(), "уже существует")
}

func TestCrNewRequiresBothArguments(t *testing.T) {
	h := newHarness(t)

	h.send("/cr_new onlyname")

	assert.Contains(t, h.lastMessage(), "формат")
}

func TestCrKillStopsRunningSession(t *testing.T) {
	h := newHarness(t).startSession("main")

	h.send("/cr_kill")

	assert.Contains(t, h.lastMessage(), "остановлена")
	assert.False(t, h.runner.Exists("main"))
}

func TestCrKillOnStoppedSessionSaysSo(t *testing.T) {
	h := newHarness(t)

	h.send("/cr_kill")

	assert.Contains(t, h.lastMessage(), "не запущена")
}

func TestCrRestartRestartsSession(t *testing.T) {
	h := newHarness(t).startSession("main")

	h.send("/cr_restart")

	assert.Contains(t, h.lastMessage(), "перезапущена")
	assert.True(t, h.runner.Exists("main"))
}

func TestCrRestartStartsStoppedSession(t *testing.T) {
	h := newHarness(t)

	h.send("/cr_restart")

	assert.Contains(t, h.lastMessage(), "перезапущена")
	assert.True(t, h.runner.Exists("main"))
}

func TestCrStatusReportsRunningState(t *testing.T) {
	h := newHarness(t).startSession("main")

	h.send("/cr_status")

	assert.Contains(t, h.lastMessage(), "работает")
}

func TestCrStatusReportsStoppedState(t *testing.T) {
	h := newHarness(t)

	h.send("/cr_status")

	assert.Contains(t, h.lastMessage(), "остановлена")
}

func TestCrSessionsListsConfiguredSessions(t *testing.T) {
	h := newHarness(t, func(c *config.Config) {
		c.Sessions["work"] = config.SessionConfig{Dir: t.TempDir(), Command: "claude"}
	})

	h.send("/cr_sessions")

	last := h.lastMessage()
	assert.Contains(t, last, "main")
	assert.Contains(t, last, "work")
	assert.Contains(t, last, "[active]")
}

func TestCrHelpListsCommands(t *testing.T) {
	h := newHarness(t)

	h.send("/cr_help")

	assert.Contains(t, h.lastMessage(), "/cr_status")
}

func TestUnknownBridgeCommandIsReported(t *testing.T) {
	h := newHarness(t)

	h.send("/cr_nonsense")

	assert.Contains(t, h.lastMessage(), "неизвестная команда")
}

func TestCrSendReportsMissingFile(t *testing.T) {
	h := newHarness(t)

	h.send("/cr_send nope.txt")

	assert.Contains(t, h.lastMessage(), "файл не найден")
}

func TestCrSendRequiresPath(t *testing.T) {
	h := newHarness(t)

	h.send("/cr_send")

	assert.Contains(t, h.lastMessage(), "формат")
}

func TestCrSendResolvesPathRelativeToSessionDir(t *testing.T) {
	h := newHarness(t)
	require.NoError(t, os.WriteFile(filepath.Join(h.sessionDir("main"), "report.txt"), []byte("body"), 0o600))

	h.send("/cr_send report.txt")

	assert.Equal(t, []string{"report.txt"}, h.tg.documents())
	assert.Empty(t, h.tg.messages())
}

func TestCrKillRefusesSessionsTheBridgeDoesNotOwn(t *testing.T) {
	h := newHarness(t)
	require.NoError(t, h.runner.Start("personal-work", t.TempDir(), "vim"))

	h.send("/cr_kill personal-work")

	assert.Contains(t, h.lastMessage(), "не настроена")
	assert.True(t, h.runner.Exists("personal-work"),
		"a tmux session the user runs by hand must survive a stray kill command")
}

func TestCrKillStillStopsAConfiguredSession(t *testing.T) {
	h := newHarness(t).startSession("main")

	h.send("/cr_kill main")

	assert.Contains(t, h.lastMessage(), "остановлена")
	assert.False(t, h.runner.Exists("main"))
}
