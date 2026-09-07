package bridge

import "github.com/yabanci/claude-remote/internal/tmux"

type Runner interface {
	Exists(session string) bool
	Start(session, dir, command string) error
	Kill(session string) error
	SendKeys(session, text string) error
	Interrupt(session string) error
	CapturePane(session string, historyLines int) (string, error)
}

type tmuxRunner struct{}

func NewTmuxRunner() Runner {
	return tmuxRunner{}
}

func (tmuxRunner) Exists(session string) bool {
	return tmux.Exists(session)
}

func (tmuxRunner) Start(session, dir, command string) error {
	return tmux.Start(session, dir, command)
}

func (tmuxRunner) Kill(session string) error {
	return tmux.Kill(session)
}

func (tmuxRunner) SendKeys(session, text string) error {
	return tmux.SendKeys(session, text)
}

func (tmuxRunner) Interrupt(session string) error {
	return tmux.Interrupt(session)
}

func (tmuxRunner) CapturePane(session string, historyLines int) (string, error) {
	return tmux.CapturePane(session, historyLines)
}
