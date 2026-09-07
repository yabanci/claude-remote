package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yabanci/claude-remote/internal/config"
)

func withStdin(t *testing.T, input string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "stdin")
	require.NoError(t, os.WriteFile(path, []byte(input), 0o600))

	f, err := os.Open(path)
	require.NoError(t, err)

	original := os.Stdin
	os.Stdin = f
	t.Cleanup(func() {
		os.Stdin = original
		_ = f.Close()
	})
}

func TestResolveConfigPathUsesFlag(t *testing.T) {
	got, err := resolveConfigPath([]string{"-config", "/tmp/custom.yaml"})

	require.NoError(t, err)
	assert.Equal(t, "/tmp/custom.yaml", got)
}

func TestResolveConfigPathFallsBackToDefault(t *testing.T) {
	got, err := resolveConfigPath(nil)

	require.NoError(t, err)
	expected, err := config.DefaultPath()
	require.NoError(t, err)
	assert.Equal(t, expected, got)
}

func TestCmdInitWritesConfigWithSecurePermissions(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.yaml")
	projectDir := t.TempDir()
	withStdin(t, "123456:test-token\n777\n"+projectDir+"\n")

	require.NoError(t, cmdInit([]string{"-config", configPath}))

	info, err := os.Stat(configPath)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), info.Mode().Perm(), "config holds a bot token, must not be world-readable")

	cfg, err := config.Load(configPath)
	require.NoError(t, err)
	assert.Equal(t, "123456:test-token", cfg.BotToken)
	assert.Equal(t, []int64{777}, cfg.AllowedUsers)
	assert.Equal(t, projectDir, cfg.Sessions[cfg.DefaultSession].Dir)
	require.NoError(t, cfg.Validate())
}

func TestCmdInitLeavesAllowlistEmptyWhenUserIDSkipped(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.yaml")
	withStdin(t, "123456:test-token\n\n\n")

	require.NoError(t, cmdInit([]string{"-config", configPath}))

	cfg, err := config.Load(configPath)
	require.NoError(t, err)
	assert.True(t, cfg.NeedsBootstrap())
}

func TestCmdInitRejectsEmptyToken(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.yaml")
	withStdin(t, "\n")

	err := cmdInit([]string{"-config", configPath})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "token")
	assert.NoFileExists(t, configPath)
}

func TestCmdInitRejectsNonNumericUserID(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.yaml")
	withStdin(t, "123456:test-token\nnot-a-number\n\n")

	err := cmdInit([]string{"-config", configPath})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid user id")
}

func TestCmdRunReportsMissingConfig(t *testing.T) {
	err := cmdRun([]string{"-config", filepath.Join(t.TempDir(), "absent.yaml")})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "load config")
}

func TestCmdRunRejectsInvalidConfig(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.yaml")
	cfg := config.Default()
	cfg.BotToken = ""
	require.NoError(t, config.Save(configPath, cfg))
	t.Setenv(config.EnvBotToken, "")

	err := cmdRun([]string{"-config", configPath})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid config")
}

func TestCmdServiceRequiresSubcommand(t *testing.T) {
	err := cmdService(nil)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "usage")
}

func TestCmdServiceRejectsUnknownSubcommand(t *testing.T) {
	err := cmdService([]string{"frobnicate"})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "unknown service subcommand")
}
