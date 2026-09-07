package service

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
)

const statusNotInstalled = "not installed"

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
	execPath   string
	searchPath string
	runner     CommandRunner
	platform   platform
	platErr    error
}

func NewManager(execPath string) *Manager {
	return NewManagerWithRunner(execPath, execRunner{})
}

func NewManagerWithRunner(execPath string, runner CommandRunner) *Manager {
	p, err := platformFor(runtime.GOOS)
	if err != nil {
		return &Manager{execPath: execPath, runner: runner, platErr: err}
	}
	return newManagerForPlatform(execPath, runner, p)
}

func newManagerForPlatform(execPath string, runner CommandRunner, p platform) *Manager {
	return &Manager{
		execPath:   execPath,
		searchPath: currentSearchPath(),
		runner:     runner,
		platform:   p,
	}
}

func currentSearchPath() string {
	if p := os.Getenv("PATH"); p != "" {
		return p
	}
	return "/usr/local/bin:/usr/bin:/bin:/usr/sbin:/sbin"
}

func (m *Manager) Install() error {
	if m.platErr != nil {
		return fmt.Errorf("service install is %w — run %q manually", m.platErr, m.execPath+" run")
	}

	unitPath, err := m.platform.unitPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(unitPath), 0o755); err != nil {
		return fmt.Errorf("create service dir: %w", err)
	}

	logDir, err := m.platform.logDir()
	if err != nil {
		return err
	}
	if logDir != "" {
		if err := os.MkdirAll(logDir, 0o755); err != nil {
			return fmt.Errorf("create log dir: %w", err)
		}
	}

	content := m.platform.render(m.execPath, logDir, m.searchPath)
	if err := os.WriteFile(unitPath, []byte(content), 0o644); err != nil {
		return fmt.Errorf("write service file: %w", err)
	}
	return m.platform.enable(m.runner, unitPath)
}

func (m *Manager) Uninstall() error {
	if m.platErr != nil {
		return fmt.Errorf("service uninstall is %w", m.platErr)
	}

	unitPath, err := m.platform.unitPath()
	if err != nil {
		return err
	}
	if err := m.platform.disable(m.runner, unitPath); err != nil {
		return err
	}
	if err := os.Remove(unitPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove service file: %w", err)
	}
	return nil
}

func (m *Manager) Status() (string, error) {
	if m.platErr != nil {
		return "", fmt.Errorf("service status is %w", m.platErr)
	}
	return m.platform.status(m.runner)
}
