package tmux

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
)

const HistoryLimit = 5000

func Exists(session string) bool {
	cmd := exec.Command("tmux", "has-session", "-t", session)
	return cmd.Run() == nil
}

func Start(session, dir, command string) error {
	info, err := os.Stat(dir)
	if err != nil {
		return fmt.Errorf("working dir %s for session %s: %w", dir, session, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("working dir %s for session %s is not a directory", dir, session)
	}

	if err := exec.Command("tmux", "new-session", "-d", "-s", session, "-c", dir).Run(); err != nil {
		return fmt.Errorf("create tmux session %s: %w", session, err)
	}

	limit := strconv.Itoa(HistoryLimit)
	if err := exec.Command("tmux", "set-option", "-t", session, "history-limit", limit).Run(); err != nil {
		return fmt.Errorf("set history-limit for %s: %w", session, err)
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
