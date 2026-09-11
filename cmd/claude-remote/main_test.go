package main

import (
	"bytes"
	"context"
	"errors"
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
	var stdout bytes.Buffer

	err := cmdService(nil, &stdout)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "usage")
	assert.Empty(t, stdout.String())
}

func TestCmdServiceRejectsUnknownSubcommand(t *testing.T) {
	var stdout bytes.Buffer

	err := cmdService([]string{"frobnicate"}, &stdout)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "unknown service subcommand")
	assert.Empty(t, stdout.String())
}

func TestServiceInstallRefusesWithoutConfig(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "absent.yaml")

	err := verifyConfigBeforeInstall([]string{"-config", missing})

	require.Error(t, err, "installing a KeepAlive service with no config would crash-loop forever")
	assert.Contains(t, err.Error(), "claude-remote init")
}

func TestServiceInstallRefusesWithInvalidConfig(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.yaml")
	cfg := config.Default()
	cfg.BotToken = ""
	require.NoError(t, config.Save(configPath, cfg))
	t.Setenv(config.EnvBotToken, "")

	err := verifyConfigBeforeInstall([]string{"-config", configPath})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid")
}

func TestRunWithNoArgsPrintsUsageAndFails(t *testing.T) {
	var stdout, stderr bytes.Buffer

	code := run(nil, &stdout, &stderr)

	assert.Equal(t, 1, code)
	assert.Contains(t, stdout.String(), "Usage:")
	assert.Empty(t, stderr.String())
}

func TestRunUnknownCommandPrintsUsageAndFails(t *testing.T) {
	var stdout, stderr bytes.Buffer

	code := run([]string{"frobnicate"}, &stdout, &stderr)

	assert.Equal(t, 1, code)
	assert.Contains(t, stdout.String(), "Usage:")
}

func TestRunHelpVariantsPrintUsageAndSucceed(t *testing.T) {
	for _, alias := range []string{"help", "-h", "--help"} {
		var stdout, stderr bytes.Buffer

		code := run([]string{alias}, &stdout, &stderr)

		assert.Equal(t, 0, code, "alias %q should exit cleanly", alias)
		assert.Contains(t, stdout.String(), "Usage:")
		assert.Empty(t, stderr.String())
	}
}

func TestRunVersionPrintsVersionAndSucceeds(t *testing.T) {
	var stdout, stderr bytes.Buffer

	code := run([]string{"version"}, &stdout, &stderr)

	assert.Equal(t, 0, code)
	assert.Contains(t, stdout.String(), version)
	assert.Empty(t, stderr.String())
}

func TestRunPropagatesSubcommandErrorToStderr(t *testing.T) {
	var stdout, stderr bytes.Buffer

	code := run([]string{"service"}, &stdout, &stderr)

	assert.Equal(t, 1, code)
	assert.Contains(t, stderr.String(), "error:")
	assert.Contains(t, stderr.String(), "usage")
}

func TestRunDelegatesRunCommand(t *testing.T) {
	var stdout, stderr bytes.Buffer

	code := run([]string{"run", "-config", filepath.Join(t.TempDir(), "absent.yaml")}, &stdout, &stderr)

	assert.Equal(t, 1, code)
	assert.Contains(t, stderr.String(), "load config")
}

func TestRunBridgeStopsCleanlyWhenContextIsAlreadyCanceled(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.yaml")
	cfg := config.Default()
	cfg.BotToken = "123:abc"
	cfg.AllowedUsers = []int64{1}
	cfg.APIBase = "http://127.0.0.1:0"
	cfg.MaxRetries = 3
	require.NoError(t, config.Save(configPath, cfg))

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := runBridge(ctx, []string{"-config", configPath})

	assert.NoError(t, err)
}

func TestRunBridgeWarnsWhenAllowlistIsEmpty(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.yaml")
	cfg := config.Default()
	cfg.BotToken = "123:abc"
	require.NoError(t, config.Save(configPath, cfg))

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := runBridge(ctx, []string{"-config", configPath})

	assert.NoError(t, err, "an empty allowlist bootstraps to the first sender, it does not fail the run")
}

func TestServiceInstallAcceptsAWorkingConfig(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.yaml")
	cfg := config.Default()
	cfg.BotToken = "123:abc"
	cfg.AllowedUsers = []int64{1}
	require.NoError(t, config.Save(configPath, cfg))

	assert.NoError(t, verifyConfigBeforeInstall([]string{"-config", configPath}))
}

type fakeServiceManager struct {
	status    string
	statusErr error
	failWith  error
	calls     []string
}

func (f *fakeServiceManager) Install() error {
	f.calls = append(f.calls, "install")
	return f.failWith
}

func (f *fakeServiceManager) Uninstall() error {
	f.calls = append(f.calls, "uninstall")
	return f.failWith
}

func (f *fakeServiceManager) Status() (string, error) {
	f.calls = append(f.calls, "status")
	return f.status, f.statusErr
}

func writableConfigPath(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	cfg := config.Default()
	cfg.BotToken = "123:abc"
	cfg.AllowedUsers = []int64{1}
	require.NoError(t, config.Save(path, cfg))
	return path
}

func TestRunServiceInstallWritesConfirmationToTheInjectedWriter(t *testing.T) {
	var stdout bytes.Buffer
	mgr := &fakeServiceManager{}

	err := runService([]string{"install", "-config", writableConfigPath(t)}, mgr, &stdout)

	require.NoError(t, err)
	assert.Equal(t, []string{"install"}, mgr.calls)
	assert.Contains(t, stdout.String(), "service installed and started")
}

func TestRunServiceUninstallWritesConfirmationToTheInjectedWriter(t *testing.T) {
	var stdout bytes.Buffer
	mgr := &fakeServiceManager{}

	err := runService([]string{"uninstall"}, mgr, &stdout)

	require.NoError(t, err)
	assert.Equal(t, []string{"uninstall"}, mgr.calls)
	assert.Contains(t, stdout.String(), "service uninstalled")
}

func TestRunServiceStatusWritesTrimmedStatusToTheInjectedWriter(t *testing.T) {
	var stdout bytes.Buffer
	mgr := &fakeServiceManager{status: "\n  running (pid 4242)  \n\n"}

	err := runService([]string{"status"}, mgr, &stdout)

	require.NoError(t, err)
	assert.Equal(t, "running (pid 4242)\n", stdout.String())
}

func TestRunServiceWritesNothingWhenTheManagerFails(t *testing.T) {
	for _, subcommand := range []string{"install", "uninstall"} {
		var stdout bytes.Buffer
		mgr := &fakeServiceManager{failWith: errors.New("launchctl exploded")}

		err := runService([]string{subcommand, "-config", writableConfigPath(t)}, mgr, &stdout)

		require.Error(t, err, "%s should surface the manager error", subcommand)
		assert.Contains(t, err.Error(), "launchctl exploded")
		assert.Empty(t, stdout.String(), "%s must not claim success after a failure", subcommand)
	}
}

func TestRunServiceStatusReportsTheManagerError(t *testing.T) {
	var stdout bytes.Buffer
	mgr := &fakeServiceManager{statusErr: errors.New("launchctl exploded")}

	err := runService([]string{"status"}, mgr, &stdout)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "service status")
	assert.Empty(t, stdout.String())
}

func TestRunServiceInstallRefusesBeforeTouchingTheManager(t *testing.T) {
	var stdout bytes.Buffer
	mgr := &fakeServiceManager{}
	missing := filepath.Join(t.TempDir(), "absent.yaml")

	err := runService([]string{"install", "-config", missing}, mgr, &stdout)

	require.Error(t, err)
	assert.Empty(t, mgr.calls, "a service that cannot start must never reach Install")
	assert.Empty(t, stdout.String())
}
