package bridge

import (
	"io"
	"log/slog"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yabanci/claude-remote/internal/config"
)

func newTestBridgeForState(t *testing.T) *Bridge {
	t.Helper()
	dir := t.TempDir()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	return New(config.Default(), filepath.Join(dir, "config.yaml"), nil, nil, logger, dir)
}

func TestBindOwnerRefusesASecondBindingOnceAlreadyBound(t *testing.T) {
	b := newTestBridgeForState(t)

	require.NoError(t, b.bindOwner(111, 1001))

	err := b.bindOwner(222, 2002)

	assert.ErrorIs(t, err, errAlreadyBound,
		"a second binding attempt must be refused, not silently overwrite the first")
	assert.Equal(t, []int64{111}, b.cfg.AllowedUsers,
		"the first owner must still be the one on record")
	assert.Equal(t, []int64{1001}, b.cfg.AllowedChats)
}

func TestATurnsTargetSessionStaysFrozenEvenIfActiveSessionChangesWhileItWaits(t *testing.T) {
	b := newTestBridgeForState(t)
	const chatID = 1
	b.cfg.Sessions["main"] = config.SessionConfig{Dir: t.TempDir(), Command: "claude"}
	b.cfg.Sessions["work"] = config.SessionConfig{Dir: t.TempDir(), Command: "claude"}
	b.activeSession[chatID] = "main"

	target := b.targetSessionFor(chatID, "hello")
	require.Equal(t, "main", target, "the turn's target is fixed at dispatch time")

	b.activeSession[chatID] = "work"

	s, err := b.resolveSession(chatID, target)

	require.NoError(t, err)
	assert.Equal(t, "main", s.name,
		"a turn already locked on \"main\" must still resolve to \"main\" even if /cr_use "+
			"switched the chat's active session to \"work\" while the turn was queued — "+
			"re-deriving the active session here instead of reusing the frozen target is "+
			"exactly what let a turn holding one session's lock act on a different session")
}
