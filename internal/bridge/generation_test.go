package bridge

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fixedPaneRunner struct {
	pane string
}

func (fixedPaneRunner) Exists(string) bool                 { return true }
func (fixedPaneRunner) Start(string, string, string) error { return nil }
func (fixedPaneRunner) Kill(string) error                  { return nil }
func (fixedPaneRunner) SendKeys(string, string) error      { return nil }
func (fixedPaneRunner) Interrupt(string) error             { return nil }
func (r fixedPaneRunner) CapturePane(string, int) (string, error) {
	return r.pane, nil
}

func TestWatchVisibleReadsThePaneWhenGenerationMatches(t *testing.T) {
	b := newTestBridgeForState(t)
	b.runner = fixedPaneRunner{pane: "$ claude\nhello"}

	capture := b.watchVisible(sessionRef{name: "main", generation: b.generations.current("main")})

	pane, err := capture()

	require.NoError(t, err)
	assert.Equal(t, "$ claude\nhello", pane)
}

func TestWatchVisibleDetectsASessionReplacedMidPoll(t *testing.T) {
	b := newTestBridgeForState(t)
	b.runner = fixedPaneRunner{pane: "should never be read"}

	staleGeneration := b.generations.current("main")
	b.generations.bump("main")

	capture := b.watchVisible(sessionRef{name: "main", generation: staleGeneration})
	pane, err := capture()

	assert.Empty(t, pane)
	assert.True(t, errors.Is(err, errSessionReplaced))
}

func TestCapturePaneDetectsASessionReplacedBetweenTurnSteps(t *testing.T) {
	b := newTestBridgeForState(t)
	b.runner = fixedPaneRunner{pane: "should never be read"}

	s := sessionRef{name: "main", generation: b.generations.current("main")}
	b.generations.bump("main")

	pane, err := b.capturePane(s, visiblePaneOnly)

	assert.Empty(t, pane)
	assert.True(t, errors.Is(err, errSessionReplaced),
		"a Start() landing between two capture points of the same turn (e.g. before "+
			"heldBackByOpenDialog's dialog check or deliverAnswer's before/after reads) "+
			"must be caught the same way WaitForSettle's own poll loop already is")
}
