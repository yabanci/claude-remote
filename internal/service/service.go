package service

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
)

const (
	launchdLabel = "dev.claude-remote.bridge"
	systemdUnit  = "claude-remote.service"
	launchdPlist = launchdLabel + ".plist"
)

type CommandRunner interface {
	Run(name string, args ...string) error
	CombinedOutput(name string, args ...string) ([]byte, error)
}

type execRunner struct{}

func (execRunner) Run(name string, args ...string) error {
	return exec.Command(name, args...).Run()
}

func (execRunner) CombinedOutput(name string, args ...string) ([]byte, error) {
	return exec.Command(name, args...).CombinedOutput()
}

type Manager struct {
	execPath string
	runner   CommandRunner
}

func NewManager(execPath string) *Manager {
	return &Manager{execPath: execPath, runner: execRunner{}}
}

func NewManagerWithRunner(execPath string, runner CommandRunner) *Manager {
	return &Manager{execPath: execPath, runner: runner}
}

func (m *Manager) Install() error {
	switch runtime.GOOS {
	case "darwin":
		return m.installLaunchd()
	case "linux":
		return m.installSystemd()
	default:
		return fmt.Errorf("service install is not supported on %s — run %q manually", runtime.GOOS, m.execPath+" run")
	}
}

func (m *Manager) Uninstall() error {
	switch runtime.GOOS {
	case "darwin":
		return m.uninstallLaunchd()
	case "linux":
		return m.uninstallSystemd()
	default:
		return fmt.Errorf("service uninstall is not supported on %s", runtime.GOOS)
	}
}

func (m *Manager) Status() (string, error) {
	switch runtime.GOOS {
	case "darwin":
		return m.statusLaunchd()
	case "linux":
		return m.statusSystemd()
	default:
		return "", fmt.Errorf("service status is not supported on %s", runtime.GOOS)
	}
}

func (m *Manager) launchdPlistPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home dir: %w", err)
	}
	return filepath.Join(home, "Library", "LaunchAgents", launchdPlist), nil
}

func (m *Manager) installLaunchd() error {
	plistPath, err := m.launchdPlistPath()
	if err != nil {
		return err
	}
	logDir := filepath.Dir(plistPath)

	content := fmt.Sprintf(launchdTemplate, launchdLabel, m.execPath, logDir, logDir)
	if err := os.MkdirAll(filepath.Dir(plistPath), 0o755); err != nil {
		return fmt.Errorf("create LaunchAgents dir: %w", err)
	}
	if err := os.WriteFile(plistPath, []byte(content), 0o644); err != nil {
		return fmt.Errorf("write plist: %w", err)
	}

	if err := m.runner.Run("launchctl", "load", "-w", plistPath); err != nil {
		return fmt.Errorf("launchctl load: %w", err)
	}
	return nil
}

func (m *Manager) uninstallLaunchd() error {
	plistPath, err := m.launchdPlistPath()
	if err != nil {
		return err
	}
	_ = m.runner.Run("launchctl", "unload", plistPath)
	if err := os.Remove(plistPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove plist: %w", err)
	}
	return nil
}

func (m *Manager) statusLaunchd() (string, error) {
	out, err := m.runner.CombinedOutput("launchctl", "list", launchdLabel)
	if err != nil {
		return "not installed", nil
	}
	return string(out), nil
}

func (m *Manager) systemdUnitPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home dir: %w", err)
	}
	return filepath.Join(home, ".config", "systemd", "user", systemdUnit), nil
}

func (m *Manager) installSystemd() error {
	unitPath, err := m.systemdUnitPath()
	if err != nil {
		return err
	}

	content := fmt.Sprintf(systemdTemplate, m.execPath)
	if err := os.MkdirAll(filepath.Dir(unitPath), 0o755); err != nil {
		return fmt.Errorf("create systemd user dir: %w", err)
	}
	if err := os.WriteFile(unitPath, []byte(content), 0o644); err != nil {
		return fmt.Errorf("write unit file: %w", err)
	}

	if err := m.runner.Run("systemctl", "--user", "daemon-reload"); err != nil {
		return fmt.Errorf("systemctl daemon-reload: %w", err)
	}
	if err := m.runner.Run("systemctl", "--user", "enable", "--now", systemdUnit); err != nil {
		return fmt.Errorf("systemctl enable --now: %w", err)
	}
	return nil
}

func (m *Manager) uninstallSystemd() error {
	_ = m.runner.Run("systemctl", "--user", "disable", "--now", systemdUnit)

	unitPath, err := m.systemdUnitPath()
	if err != nil {
		return err
	}
	if err := os.Remove(unitPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove unit file: %w", err)
	}
	return m.runner.Run("systemctl", "--user", "daemon-reload")
}

func (m *Manager) statusSystemd() (string, error) {
	out, err := m.runner.CombinedOutput("systemctl", "--user", "is-active", systemdUnit)
	if err != nil {
		return "not installed", nil
	}
	return string(out), nil
}

const launchdTemplate = `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key>
  <string>%s</string>
  <key>ProgramArguments</key>
  <array>
    <string>%s</string>
    <string>run</string>
  </array>
  <key>RunAtLoad</key>
  <true/>
  <key>KeepAlive</key>
  <true/>
  <key>StandardOutPath</key>
  <string>%s/claude-remote.out.log</string>
  <key>StandardErrorPath</key>
  <string>%s/claude-remote.err.log</string>
</dict>
</plist>
`

const systemdTemplate = `[Unit]
Description=claude-remote Telegram bridge

[Service]
ExecStart=%s run
Restart=on-failure
RestartSec=5

[Install]
WantedBy=default.target
`
