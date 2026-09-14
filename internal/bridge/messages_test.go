package bridge_test

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEveryCommandNamesAStoppedSessionTheSameWay(t *testing.T) {
	said := map[string]string{}
	for _, cmd := range []string{"/cr_kill", "/cr_interrupt", "/cr_peek"} {
		h := newHarness(t)
		h.send(cmd)
		said[cmd] = h.lastMessage()
	}

	require.NotEmpty(t, said["/cr_kill"])
	assert.Equal(t, said["/cr_kill"], said["/cr_interrupt"])
	assert.Equal(t, said["/cr_kill"], said["/cr_peek"])
}

func TestKillAndRestartReportAFailedStopTheSameWay(t *testing.T) {
	stopFailure := func(t *testing.T, cmd string) string {
		cfg := testConfigFor(t)
		runner := &failingRunner{fakeRunner: newFakeRunner(), killErr: errors.New("tmux server unreachable")}
		require.NoError(t, runner.Start("main", cfg.Sessions["main"].Dir, "claude"))
		h := newHarnessWithRunner(t, cfg, runner)
		h.send(cmd)
		return h.lastMessage()
	}

	killed := stopFailure(t, "/cr_kill")

	require.NotEmpty(t, killed)
	assert.Equal(t, killed, stopFailure(t, "/cr_restart"))
}
