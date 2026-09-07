package service

import (
	"fmt"
	"os"
	"path/filepath"
)

const (
	launchdLabel = "dev.claude-remote.bridge"
	launchdPlist = launchdLabel + ".plist"
	systemdUnit  = "claude-remote.service"
)

type platform interface {
	unitPath() (string, error)
	logDir() (string, error)
	render(execPath, logDir, searchPath string) string
	enable(runner CommandRunner, unitPath string) error
	disable(runner CommandRunner, unitPath string) error
	status(runner CommandRunner) (string, error)
}

func platformFor(goos string) (platform, error) {
	switch goos {
	case "darwin":
		return launchd{}, nil
	case "linux":
		return systemd{}, nil
	default:
		return nil, fmt.Errorf("not supported on %s", goos)
	}
}

type launchd struct{}

func (launchd) unitPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home dir: %w", err)
	}
	return filepath.Join(home, "Library", "LaunchAgents", launchdPlist), nil
}

func (launchd) logDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home dir: %w", err)
	}
	return filepath.Join(home, "Library", "Logs", "claude-remote"), nil
}

func (launchd) render(execPath, logDir, searchPath string) string {
	return fmt.Sprintf(launchdTemplate, launchdLabel, execPath, searchPath, logDir, logDir)
}

func (launchd) enable(runner CommandRunner, unitPath string) error {
	if err := runner.Run("launchctl", "load", "-w", unitPath); err != nil {
		return fmt.Errorf("launchctl load: %w", err)
	}
	return nil
}

func (launchd) disable(runner CommandRunner, unitPath string) error {
	_ = runner.Run("launchctl", "unload", unitPath)
	return nil
}

func (launchd) status(runner CommandRunner) (string, error) {
	out, err := runner.CombinedOutput("launchctl", "list", launchdLabel)
	if err != nil {
		return statusNotInstalled, nil
	}
	return string(out), nil
}

type systemd struct{}

func (systemd) unitPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home dir: %w", err)
	}
	return filepath.Join(home, ".config", "systemd", "user", systemdUnit), nil
}

func (systemd) logDir() (string, error) {
	return "", nil
}

func (systemd) render(execPath, _, searchPath string) string {
	return fmt.Sprintf(systemdTemplate, searchPath, execPath)
}

func (systemd) enable(runner CommandRunner, _ string) error {
	if err := runner.Run("systemctl", "--user", "daemon-reload"); err != nil {
		return fmt.Errorf("systemctl daemon-reload: %w", err)
	}
	if err := runner.Run("systemctl", "--user", "enable", "--now", systemdUnit); err != nil {
		return fmt.Errorf("systemctl enable --now: %w", err)
	}
	return nil
}

func (systemd) disable(runner CommandRunner, _ string) error {
	_ = runner.Run("systemctl", "--user", "disable", "--now", systemdUnit)
	return runner.Run("systemctl", "--user", "daemon-reload")
}

func (systemd) status(runner CommandRunner) (string, error) {
	out, err := runner.CombinedOutput("systemctl", "--user", "is-active", systemdUnit)
	if err != nil {
		return statusNotInstalled, nil
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
  <key>EnvironmentVariables</key>
  <dict>
    <key>PATH</key>
    <string>%s</string>
  </dict>
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
Environment="PATH=%s"
ExecStart=%s run
Restart=on-failure
RestartSec=5

[Install]
WantedBy=default.target
`
