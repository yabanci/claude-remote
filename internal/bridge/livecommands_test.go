package bridge_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yabanci/claude-remote/internal/config"
)

func TestLiveHelpListsEveryCommand(t *testing.T) {
	lh := newLiveHarness(t)
	lh.queue("/cr_help")

	lh.runUntil(1, 30*time.Second)

	reply := lh.lastReply()
	for _, cmd := range []string{"/cr_status", "/cr_sessions", "/cr_use", "/cr_new", "/cr_kill", "/cr_restart", "/cr_interrupt", "/cr_send"} {
		assert.Contains(t, reply, cmd)
	}
}

func TestLiveStatusReflectsRealTmuxState(t *testing.T) {
	lh := newLiveHarness(t)
	lh.queue("/cr_status")

	lh.runUntil(1, 30*time.Second)
	assert.Contains(t, lh.lastReply(), "остановлена")

	liveStart(t, lh.session, lh.sessionDir)
	lh.queue("/cr_status")
	lh.runUntil(2, 30*time.Second)

	assert.Contains(t, lh.lastReply(), "работает")
}

func TestLiveNewCreatesRealSessionAndPersistsIt(t *testing.T) {
	lh := newLiveHarness(t)
	projectDir := t.TempDir()
	extra := lh.session + "-extra"
	t.Cleanup(func() {
		if liveExists(extra) {
			_ = liveKill(extra)
		}
	})

	lh.queue("/cr_new " + extra + " " + projectDir)
	lh.runUntil(1, 60*time.Second)

	assert.Contains(t, lh.lastReply(), "создана и запущена")
	assert.True(t, liveExists(extra), "a real tmux session must exist afterwards")

	saved, err := config.Load(lh.configPath)
	require.NoError(t, err)
	assert.Equal(t, projectDir, saved.Sessions[extra].Dir, "the session must survive a restart of the bridge")
}

func TestLiveUseSwitchesWhichSessionReceivesMessages(t *testing.T) {
	lh := newLiveHarness(t)
	other := lh.session + "-other"
	otherDir := t.TempDir()
	t.Cleanup(func() {
		if liveExists(other) {
			_ = liveKill(other)
		}
	})

	lh.queue("/cr_new "+other+" "+otherDir, "echo landed-in-second-session")
	lh.runUntil(2, 90*time.Second)

	pane, err := liveCapture(other, 200)
	require.NoError(t, err)
	assert.Contains(t, pane, "landed-in-second-session",
		"after /cr_new the new session becomes active and must receive the next message")
}

func TestLiveKillStopsTheRealSession(t *testing.T) {
	lh := newLiveHarness(t)
	liveStart(t, lh.session, lh.sessionDir)
	require.True(t, liveExists(lh.session))

	lh.queue("/cr_kill")
	lh.runUntil(1, 30*time.Second)

	assert.Contains(t, lh.lastReply(), "остановлена")
	assert.False(t, liveExists(lh.session), "the tmux session must actually be gone")
}

func TestLiveInterruptStopsARunningCommand(t *testing.T) {
	lh := newLiveHarness(t)
	liveStart(t, lh.session, lh.sessionDir)
	require.NoError(t, liveSendKeys(lh.session, "sleep 300"))
	time.Sleep(2 * time.Second)

	lh.queue("/cr_interrupt")
	lh.runUntil(1, 30*time.Second)

	assert.Contains(t, lh.lastReply(), "Ctrl-C")
	require.Eventually(t, func() bool {
		pane, err := liveCapture(lh.session, 100)
		return err == nil && strings.Contains(pane, "^C")
	}, 15*time.Second, 300*time.Millisecond, "the interrupt must reach the running command")
}

func TestLiveSendDeliversARealFile(t *testing.T) {
	lh := newLiveHarness(t)
	require.NoError(t, os.WriteFile(filepath.Join(lh.sessionDir, "report.txt"), []byte("тело отчёта"), 0o600))

	lh.queue("/cr_send report.txt")
	lh.runUntil(1, 30*time.Second)

	assert.Equal(t, []string{"report.txt"}, lh.sentDocuments())
}

func TestLiveUploadedFileLandsInTheRealSessionDir(t *testing.T) {
	lh := newLiveHarness(t)
	liveStart(t, lh.session, lh.sessionDir)

	lh.queueDocument("notes.txt")
	lh.runUntil(1, 60*time.Second)

	saved := filepath.Join(lh.sessionDir, "telegram-inbox", "notes.txt")
	body, err := os.ReadFile(saved)
	require.NoError(t, err, "the uploaded file must exist on disk")
	assert.Equal(t, "содержимое присланного файла", string(body))

	pane, err := liveCapture(lh.session, 200)
	require.NoError(t, err)
	assert.Contains(t, pane, "telegram-inbox/notes.txt", "the session must be told where the file went")
}

func TestLiveLongOutputArrivesAsADocument(t *testing.T) {
	lh := newLiveHarness(t)
	padding := strings.Repeat("x", 60)
	lh.queue("for i in $(seq 1 400); do echo \"line $i " + padding + "\"; done")

	lh.runUntil(1, 120*time.Second)

	assert.NotEmpty(t, lh.sentDocuments(),
		"output past the inline limit must arrive as a file instead of a wall of messages")
}
