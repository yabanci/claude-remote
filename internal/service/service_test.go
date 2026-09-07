package service_test

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yabanci/claude-remote/internal/service"
)

type call struct {
	name string
	args []string
}

type fakeRunner struct {
	calls   []call
	failOn  string
	outputs map[string]string
}

func (f *fakeRunner) Run(name string, args ...string) error {
	f.calls = append(f.calls, call{name, args})
	if name == f.failOn {
		return fmt.Errorf("simulated failure for %s", name)
	}
	return nil
}

func (f *fakeRunner) CombinedOutput(name string, args ...string) ([]byte, error) {
	f.calls = append(f.calls, call{name, args})
	if name == f.failOn {
		return nil, fmt.Errorf("simulated failure for %s", name)
	}
	key := name + " " + fmt.Sprint(args)
	return []byte(f.outputs[key]), nil
}

func skipUnlessOS(t *testing.T, goos string) {
	t.Helper()
	if runtime.GOOS != goos {
		t.Skipf("test targets GOOS=%s, running on %s", goos, runtime.GOOS)
	}
}

func TestInstallLaunchdWritesPlistAndLoadsIt(t *testing.T) {
	skipUnlessOS(t, "darwin")
	home := t.TempDir()
	t.Setenv("HOME", home)

	runner := &fakeRunner{}
	mgr := service.NewManagerWithRunner("/usr/local/bin/claude-remote", runner)

	require.NoError(t, mgr.Install())

	plistPath := filepath.Join(home, "Library", "LaunchAgents", "dev.claude-remote.bridge.plist")
	data, err := os.ReadFile(plistPath)
	require.NoError(t, err)
	assert.Contains(t, string(data), "/usr/local/bin/claude-remote")
	assert.Contains(t, string(data), "dev.claude-remote.bridge")

	require.Len(t, runner.calls, 1)
	assert.Equal(t, "launchctl", runner.calls[0].name)
	assert.Equal(t, []string{"load", "-w", plistPath}, runner.calls[0].args)
}

func TestUninstallLaunchdRemovesPlist(t *testing.T) {
	skipUnlessOS(t, "darwin")
	home := t.TempDir()
	t.Setenv("HOME", home)

	runner := &fakeRunner{}
	mgr := service.NewManagerWithRunner("/usr/local/bin/claude-remote", runner)
	require.NoError(t, mgr.Install())

	require.NoError(t, mgr.Uninstall())

	plistPath := filepath.Join(home, "Library", "LaunchAgents", "dev.claude-remote.bridge.plist")
	_, err := os.Stat(plistPath)
	assert.True(t, os.IsNotExist(err))
}

func TestStatusLaunchdNotInstalled(t *testing.T) {
	skipUnlessOS(t, "darwin")
	t.Setenv("HOME", t.TempDir())

	runner := &fakeRunner{failOn: "launchctl"}
	mgr := service.NewManagerWithRunner("/usr/local/bin/claude-remote", runner)

	status, err := mgr.Status()

	require.NoError(t, err)
	assert.Equal(t, "not installed", status)
}

func TestInstallSystemdWritesUnitAndEnables(t *testing.T) {
	skipUnlessOS(t, "linux")
	home := t.TempDir()
	t.Setenv("HOME", home)

	runner := &fakeRunner{}
	mgr := service.NewManagerWithRunner("/usr/local/bin/claude-remote", runner)

	require.NoError(t, mgr.Install())

	unitPath := filepath.Join(home, ".config", "systemd", "user", "claude-remote.service")
	data, err := os.ReadFile(unitPath)
	require.NoError(t, err)
	assert.Contains(t, string(data), "ExecStart=/usr/local/bin/claude-remote run")

	require.Len(t, runner.calls, 2)
	assert.Equal(t, []string{"--user", "daemon-reload"}, runner.calls[0].args)
	assert.Equal(t, []string{"--user", "enable", "--now", "claude-remote.service"}, runner.calls[1].args)
}

func TestNewManagerStoresExecPath(t *testing.T) {
	mgr := service.NewManager(filepath.Join("usr", "local", "bin", "claude-remote"))
	assert.NotNil(t, mgr)
}
