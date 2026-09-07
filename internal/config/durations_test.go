package config_test

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yabanci/claude-remote/internal/config"
)

func TestSettleDurationsUseTheDeclaredUnits(t *testing.T) {
	s := config.SettleConfig{
		PollIntervalMS:       1500,
		HardCapSeconds:       1200,
		InterimNoticeSeconds: 120,
		ColdStartDelayMS:     3000,
		PostSendDelayMS:      2000,
	}

	assert.Equal(t, 1500*time.Millisecond, s.PollInterval())
	assert.Equal(t, 20*time.Minute, s.HardCapDuration())
	assert.Equal(t, 2*time.Minute, s.InterimNoticeDuration())
	assert.Equal(t, 3*time.Second, s.ColdStartDelay())
	assert.Equal(t, 2*time.Second, s.PostSendDelay())
}

func TestDefaultPathLivesUnderTheOSConfigDir(t *testing.T) {
	path, err := config.DefaultPath()

	require.NoError(t, err)
	assert.Equal(t, "config.yaml", filepath.Base(path))
	assert.Equal(t, "claude-remote", filepath.Base(filepath.Dir(path)))
	assert.True(t, filepath.IsAbs(path), "the daemon resolves this without a working directory")
}
