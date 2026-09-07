package tmux

import (
	"errors"
	"fmt"
	"os/exec"
	"strconv"
)

func Exists(session string) bool {
	cmd := exec.Command("tmux", "has-session", "-t", session)
	return cmd.Run() == nil
}

func Start(session, dir, command string) error {
	if err := exec.Command("tmux", "new-session", "-d", "-s", session, "-c", dir).Run(); err != nil {
		return fmt.Errorf("create tmux session %s: %w", session, err)
	}
	if command == "" {
		return nil
	}
	return SendKeys(session, command)
}

func Kill(session string) error {
	if err := exec.Command("tmux", "kill-session", "-t", session).Run(); err != nil {
		return fmt.Errorf("kill tmux session %s: %w", session, err)
	}
	return nil
}

func SendKeys(session, text string) error {
	if err := exec.Command("tmux", "send-keys", "-t", session, "-l", text).Run(); err != nil {
		return fmt.Errorf("send keys to %s: %w", session, err)
	}
	if err := exec.Command("tmux", "send-keys", "-t", session, "Enter").Run(); err != nil {
		return fmt.Errorf("send enter to %s: %w", session, err)
	}
	return nil
}

func Interrupt(session string) error {
	if err := exec.Command("tmux", "send-keys", "-t", session, "C-c").Run(); err != nil {
		return fmt.Errorf("send interrupt to %s: %w", session, err)
	}
	return nil
}

func CapturePane(session string, historyLines int) (string, error) {
	args := []string{"capture-pane", "-p", "-t", session}
	if historyLines > 0 {
		args = append(args, "-S", "-"+strconv.Itoa(historyLines))
	}
	out, err := exec.Command("tmux", args...).Output()
	if err != nil {
		return "", fmt.Errorf("capture pane %s: %w", session, err)
	}
	return string(out), nil
}

func ListSessions() ([]string, error) {
	out, err := exec.Command("tmux", "list-sessions", "-F", "#{session_name}").Output()
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("list tmux sessions: %w", err)
	}
	return splitNonEmptyLines(string(out)), nil
}

func splitNonEmptyLines(s string) []string {
	var lines []string
	start := 0
	for i := 0; i <= len(s); i++ {
		if i == len(s) || s[i] == '\n' {
			if i > start {
				lines = append(lines, s[start:i])
			}
			start = i + 1
		}
	}
	return lines
}
