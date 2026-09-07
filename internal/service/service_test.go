package service

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type call struct {
	name string
	args []string
}

type fakeRunner struct {
	calls  []call
	failOn string
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
	return []byte("running"), nil
}

func (f *fakeRunner) ran(name string) bool {
	for _, c := range f.calls {
		if c.name == name {
			return true
		}
	}
	return false
}

type platformCase struct {
	name        string
	platform    platform
	unitRelPath []string
	tool        string
	mustContain string
}

func platformCases() []platformCase {
	return []platformCase{
		{
			name:        "launchd",
			platform:    launchd{},
			unitRelPath: []string{"Library", "LaunchAgents", "dev.claude-remote.bridge.plist"},
			tool:        "launchctl",
			mustContain: "<string>/usr/local/bin/claude-remote</string>",
		},
		{
			name:        "systemd",
			platform:    systemd{},
			unitRelPath: []string{".config", "systemd", "user", "claude-remote.service"},
			tool:        "systemctl",
			mustContain: "ExecStart=/usr/local/bin/claude-remote run",
		},
	}
}

func TestInstallWritesUnitFileAndEnablesIt(t *testing.T) {
	for _, tc := range platformCases() {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			runner := &fakeRunner{}
			mgr := newManagerForPlatform("/usr/local/bin/claude-remote", runner, tc.platform)

			require.NoError(t, mgr.Install())

			unitPath := filepath.Join(append([]string{home}, tc.unitRelPath...)...)
			data, err := os.ReadFile(unitPath)
			require.NoError(t, err)
			assert.Contains(t, string(data), tc.mustContain)
			assert.True(t, runner.ran(tc.tool), "should have invoked %s", tc.tool)
		})
	}
}

func TestUninstallRemovesUnitFile(t *testing.T) {
	for _, tc := range platformCases() {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			runner := &fakeRunner{}
			mgr := newManagerForPlatform("/usr/local/bin/claude-remote", runner, tc.platform)
			require.NoError(t, mgr.Install())

			require.NoError(t, mgr.Uninstall())

			unitPath := filepath.Join(append([]string{home}, tc.unitRelPath...)...)
			assert.NoFileExists(t, unitPath)
		})
	}
}

func TestUninstallIsFineWhenNothingInstalled(t *testing.T) {
	for _, tc := range platformCases() {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			mgr := newManagerForPlatform("/usr/local/bin/claude-remote", &fakeRunner{}, tc.platform)

			assert.NoError(t, mgr.Uninstall())
		})
	}
}

func TestStatusReportsNotInstalledWhenToolFails(t *testing.T) {
	for _, tc := range platformCases() {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			runner := &fakeRunner{failOn: tc.tool}
			mgr := newManagerForPlatform("/usr/local/bin/claude-remote", runner, tc.platform)

			status, err := mgr.Status()

			require.NoError(t, err)
			assert.Equal(t, statusNotInstalled, status)
		})
	}
}

func TestStatusReturnsToolOutput(t *testing.T) {
	for _, tc := range platformCases() {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			mgr := newManagerForPlatform("/usr/local/bin/claude-remote", &fakeRunner{}, tc.platform)

			status, err := mgr.Status()

			require.NoError(t, err)
			assert.Equal(t, "running", status)
		})
	}
}

func TestInstallPropagatesEnableFailure(t *testing.T) {
	for _, tc := range platformCases() {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			runner := &fakeRunner{failOn: tc.tool}
			mgr := newManagerForPlatform("/usr/local/bin/claude-remote", runner, tc.platform)

			err := mgr.Install()

			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.tool)
		})
	}
}

func TestInstallIsIdempotent(t *testing.T) {
	for _, tc := range platformCases() {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			mgr := newManagerForPlatform("/usr/local/bin/claude-remote", &fakeRunner{}, tc.platform)

			require.NoError(t, mgr.Install())
			require.NoError(t, mgr.Install())

			unitPath := filepath.Join(append([]string{home}, tc.unitRelPath...)...)
			data, err := os.ReadFile(unitPath)
			require.NoError(t, err)
			assert.Equal(t, 1, strings.Count(string(data), tc.mustContain), "reinstall must overwrite, not append")
		})
	}
}

func TestUnsupportedPlatformGivesActionableError(t *testing.T) {
	mgr := &Manager{execPath: "/usr/local/bin/claude-remote", platErr: fmt.Errorf("not supported on plan9")}

	installErr := mgr.Install()
	uninstallErr := mgr.Uninstall()
	_, statusErr := mgr.Status()

	require.Error(t, installErr)
	assert.Contains(t, installErr.Error(), "manually")
	require.Error(t, uninstallErr)
	require.Error(t, statusErr)
}

func TestPlatformForKnownOSes(t *testing.T) {
	darwinPlatform, err := platformFor("darwin")
	require.NoError(t, err)
	assert.IsType(t, launchd{}, darwinPlatform)

	linuxPlatform, err := platformFor("linux")
	require.NoError(t, err)
	assert.IsType(t, systemd{}, linuxPlatform)

	_, err = platformFor("windows")
	require.Error(t, err)
}

func TestNewManagerUsesRealExecRunner(t *testing.T) {
	mgr := NewManager("/usr/local/bin/claude-remote")
	assert.IsType(t, execRunner{}, mgr.runner)
}

func TestExecRunnerRunsRealCommands(t *testing.T) {
	r := execRunner{}

	require.NoError(t, r.Run("true"))
	require.Error(t, r.Run("false"))

	out, err := r.CombinedOutput("echo", "hello")
	require.NoError(t, err)
	assert.Equal(t, "hello", strings.TrimSpace(string(out)))
}
