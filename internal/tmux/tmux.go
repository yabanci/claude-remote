package tmux

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

const (
	HistoryLimit    = 5000
	controlTimeout  = 5 * time.Second
	captureTimeout  = 15 * time.Second
	pasteBufferName = "claude-remote-input"
)

var ErrTimeout = errors.New("tmux did not answer in time")

func run(timeout time.Duration, stdin string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, "tmux", args...)
	if stdin != "" {
		cmd.Stdin = strings.NewReader(stdin)
	}

	out, err := cmd.CombinedOutput()
	if ctx.Err() != nil {
		return "", fmt.Errorf("tmux %s: %w", args[0], ErrTimeout)
	}
	if err != nil {
		return "", fmt.Errorf("tmux %s: %w: %s", args[0], err, firstLine(out))
	}
	return string(out), nil
}

func firstLine(out []byte) string {
	text := strings.TrimSpace(string(out))
	if idx := strings.IndexByte(text, '\n'); idx >= 0 {
		text = text[:idx]
	}
	if len(text) > 200 {
		text = text[:200]
	}
	return text
}

func Exists(session string) bool {
	_, err := run(controlTimeout, "", "has-session", "-t", session)
	return err == nil
}

func Start(session, dir, command string) error {
	info, err := os.Stat(dir)
	if err != nil {
		return fmt.Errorf("working dir %s for session %s: %w", dir, session, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("working dir %s for session %s is not a directory", dir, session)
	}

	if _, err := run(controlTimeout, "", "new-session", "-d", "-s", session, "-c", dir); err != nil {
		return fmt.Errorf("create tmux session %s: %w", session, err)
	}
	if _, err := run(controlTimeout, "", "set-option", "-t", session, "history-limit", strconv.Itoa(HistoryLimit)); err != nil {
		return fmt.Errorf("set history-limit for %s: %w", session, err)
	}

	if command == "" {
		return nil
	}
	return SendKeys(session, command)
}

func Kill(session string) error {
	if _, err := run(controlTimeout, "", "kill-session", "-t", session); err != nil {
		return fmt.Errorf("kill tmux session %s: %w", session, err)
	}
	return nil
}

func SendKeys(session, text string) error {
	if err := pasteLiterally(session, text); err != nil {
		return err
	}
	if _, err := run(controlTimeout, "", "send-keys", "-t", session, "Enter"); err != nil {
		return fmt.Errorf("send enter to %s: %w", session, err)
	}
	return nil
}

func pasteLiterally(session, text string) error {
	if _, err := run(controlTimeout, text, "load-buffer", "-b", pasteBufferName, "-"); err != nil {
		return fmt.Errorf("load input buffer for %s: %w", session, err)
	}
	if _, err := run(controlTimeout, "", "paste-buffer", "-d", "-p", "-b", pasteBufferName, "-t", session); err != nil {
		return fmt.Errorf("paste input into %s: %w", session, err)
	}
	return nil
}

func Interrupt(session string) error {
	if _, err := run(controlTimeout, "", "send-keys", "-t", session, "C-c"); err != nil {
		return fmt.Errorf("send interrupt to %s: %w", session, err)
	}
	return nil
}

func CapturePane(session string, historyLines int) (string, error) {
	args := []string{"capture-pane", "-p", "-t", session}
	if historyLines > 0 {
		args = append(args, "-S", "-"+strconv.Itoa(historyLines))
	}

	out, err := run(captureTimeout, "", args...)
	if err != nil {
		return "", fmt.Errorf("capture pane %s: %w", session, err)
	}
	return out, nil
}
