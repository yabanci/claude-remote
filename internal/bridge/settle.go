package bridge

import (
	"strings"
	"time"

	"github.com/yabanci/claude-remote/internal/config"
)

func DiffTail(before, after string) string {
	beforeLines := strings.Split(before, "\n")
	afterLines := strings.Split(after, "\n")

	common := 0
	for common < len(beforeLines) && common < len(afterLines) && beforeLines[common] == afterLines[common] {
		common++
	}
	return strings.TrimSpace(strings.Join(afterLines[common:], "\n"))
}

type CaptureFunc func() (string, error)

type InterimFunc func(elapsed time.Duration)

func WaitForSettle(capture CaptureFunc, cfg config.SettleConfig, onInterim InterimFunc) (string, error) {
	pollInterval := cfg.PollInterval()
	hardCap := cfg.HardCapDuration()
	interimEvery := cfg.InterimNoticeDuration()

	last, err := capture()
	if err != nil {
		return "", err
	}

	var elapsed time.Duration
	var lastNotice time.Duration
	stableRounds := 0

	for stableRounds < cfg.StableRounds && elapsed < hardCap {
		time.Sleep(pollInterval)
		elapsed += pollInterval

		current, err := capture()
		if err != nil {
			return "", err
		}

		if current == last {
			stableRounds++
		} else {
			stableRounds = 0
			last = current
		}

		if onInterim != nil && elapsed-lastNotice >= interimEvery {
			lastNotice = elapsed
			onInterim(elapsed)
		}
	}

	return last, nil
}
