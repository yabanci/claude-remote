package service

import (
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
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
	return fmt.Sprintf(launchdTemplate, launchdLabel,
		xmlEscape(execPath), xmlEscape(searchPath), xmlEscape(logDir), xmlEscape(logDir))
}

func xmlEscape(s string) string {
	var buf bytes.Buffer
	if err := xml.EscapeText(&buf, []byte(s)); err != nil {
		return s
	}
	return buf.String()
}

func (launchd) enable(runner CommandRunner, unitPath string) error {
	if err := runner.Run("launchctl", "load", "-w", unitPath); err != nil {
		return fmt.Errorf("launchctl load: %w", err)
	}
	return nil
}

func (launchd) disable(runner CommandRunner, unitPath string) error {
	if err := runner.Run("launchctl", "unload", unitPath); err != nil {
		return fmt.Errorf("launchctl unload: %w", err)
	}
	return nil
}

func (launchd) status(runner CommandRunner) (string, error) {
	out, err := runner.CombinedOutput("launchctl", "list", launchdLabel)
	if err != nil {
		return statusNotInstalled, nil
	}
	text := string(out)
	if strings.Contains(text, `"PID"`) {
		return text, nil
	}
	if launchdLastExitStatusNonZero(text) {
		return statusFailed, nil
	}
	return statusStopped, nil
}

func launchdLastExitStatusNonZero(text string) bool {
	_, afterKey, found := strings.Cut(text, `"LastExitStatus"`)
	if !found {
		return false
	}
	_, afterEquals, found := strings.Cut(afterKey, "=")
	if !found {
		return false
	}
	if end := strings.IndexAny(afterEquals, ";\n"); end != -1 {
		afterEquals = afterEquals[:end]
	}
	value := strings.TrimSpace(afterEquals)
	return value != "" && value != "0"
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
	return fmt.Sprintf(systemdTemplate, systemdEnv("PATH", searchPath), systemdArg(execPath))
}

var systemdEscaper = strings.NewReplacer(`\`, `\\`, `"`, `\"`)

func systemdArg(s string) string {
	return `"` + systemdEscaper.Replace(s) + `"`
}

func systemdEnv(key, value string) string {
	return `"` + key + "=" + systemdEscaper.Replace(value) + `"`
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
	var errs []error
	if err := runner.Run("systemctl", "--user", "disable", "--now", systemdUnit); err != nil {
		errs = append(errs, fmt.Errorf("systemctl disable --now: %w", err))
	}
	if err := runner.Run("systemctl", "--user", "daemon-reload"); err != nil {
		errs = append(errs, fmt.Errorf("systemctl daemon-reload: %w", err))
	}
	return errors.Join(errs...)
}

func (systemd) status(runner CommandRunner) (string, error) {
	out, err := runner.CombinedOutput("systemctl", "--user", "is-active", systemdUnit)
	state := strings.TrimSpace(string(out))
	if err == nil {
		return state, nil
	}
	switch state {
	case "failed":
		return statusFailed, nil
	case "inactive":
		return statusStopped, nil
	default:
		return statusNotInstalled, nil
	}
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
Environment=%s
ExecStart=%s run
Restart=on-failure
RestartSec=5

[Install]
WantedBy=default.target
`
