package bridge_test

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yabanci/claude-remote/internal/bridge"
	"github.com/yabanci/claude-remote/internal/config"
)

func TestDiffTail(t *testing.T) {
	tests := []struct {
		name   string
		before string
		after  string
		want   string
	}{
		{"no change", "hello\nworld", "hello\nworld", ""},
		{"appended lines", "hello\nworld", "hello\nworld\nnew line", "new line"},
		{"fully different", "old", "totally new", "totally new"},
		{"empty before", "", "reply text", "reply text"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, bridge.DiffTail(tc.before, tc.after))
		})
	}
}

func TestDiffTailFallsBackWhenScrollbackWasEvicted(t *testing.T) {
	before := fullHistoryPane("before line", 5000)
	after := fullHistoryPane("after line", 5000)

	got := bridge.DiffTail(before, after)

	assert.Contains(t, got, "/cr_peek")
	assert.NotContains(t, got, "after line")
}

func TestDiffTailReturnsWholeCaptureWhenHistoryIsNotYetFull(t *testing.T) {
	before := fullHistoryPane("before line", 10)
	after := fullHistoryPane("after line", 10)

	got := bridge.DiffTail(before, after)

	assert.Equal(t, after, got)
}

func fullHistoryPane(linePrefix string, lines int) string {
	rows := make([]string, lines)
	for i := range rows {
		rows[i] = fmt.Sprintf("%s %d", linePrefix, i)
	}
	return strings.Join(rows, "\n")
}

func sequenceCapture(values []string) bridge.CaptureFunc {
	i := 0
	return func() (string, error) {
		if i >= len(values) {
			i = len(values) - 1
		}
		v := values[i]
		i++
		return v, nil
	}
}

func TestWaitForSettleStopsOnceStable(t *testing.T) {
	cfg := config.SettleConfig{PollIntervalMS: 5, StableRounds: 2, HardCapSeconds: 5}
	capture := sequenceCapture([]string{"a", "b", "b", "b", "b"})

	got, err := bridge.WaitForSettle(capture, cfg, nil)

	require.NoError(t, err)
	assert.Equal(t, "b", got)
}

func TestWaitForSettleHitsHardCapWhenNeverStable(t *testing.T) {
	cfg := config.SettleConfig{PollIntervalMS: 5, StableRounds: 3, HardCapSeconds: 1}
	call := 0
	capture := func() (string, error) {
		call++
		return fmt.Sprintf("frame-%d", call), nil
	}

	start := time.Now()
	_, err := bridge.WaitForSettle(capture, cfg, nil)
	elapsed := time.Since(start)

	require.NoError(t, err)
	assert.GreaterOrEqual(t, elapsed, cfg.HardCapDuration()-50*time.Millisecond)
}

func TestWaitForSettlePropagatesCaptureError(t *testing.T) {
	cfg := config.SettleConfig{PollIntervalMS: 5, StableRounds: 2, HardCapSeconds: 5}
	boom := errors.New("tmux gone")
	capture := func() (string, error) { return "", boom }

	_, err := bridge.WaitForSettle(capture, cfg, nil)

	require.ErrorIs(t, err, boom)
}

func TestWaitForSettleInvokesInterimCallback(t *testing.T) {
	cfg := config.SettleConfig{PollIntervalMS: 5, StableRounds: 100, HardCapSeconds: 1, InterimNoticeSeconds: 1}
	call := 0
	capture := func() (string, error) {
		call++
		return fmt.Sprintf("frame-%d", call), nil
	}

	var notices []time.Duration
	_, err := bridge.WaitForSettle(capture, cfg, func(elapsed time.Duration) {
		notices = append(notices, elapsed)
	})

	require.NoError(t, err)
	assert.NotEmpty(t, notices)
}

func TestWaitForSettleCountsCaptureDurationTowardTheHardCap(t *testing.T) {
	const captureDuration = 30 * time.Millisecond
	cfg := config.SettleConfig{PollIntervalMS: 2, StableRounds: 1000, HardCapSeconds: 1}
	frame := 0
	capture := func() (string, error) {
		time.Sleep(captureDuration)
		frame++
		return fmt.Sprintf("frame-%d", frame), nil
	}

	start := time.Now()
	_, err := bridge.WaitForSettle(capture, cfg, nil)
	elapsed := time.Since(start)

	require.NoError(t, err)
	naiveRounds := int64(cfg.HardCapDuration() / cfg.PollInterval())
	naiveWallClock := time.Duration(naiveRounds) * (cfg.PollInterval() + captureDuration)
	assert.Less(t, elapsed, naiveWallClock/4)
	assert.GreaterOrEqual(t, elapsed, cfg.HardCapDuration()-cfg.PollInterval()-captureDuration)
}
