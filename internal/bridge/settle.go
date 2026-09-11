package bridge

import (
	"strings"
	"time"

	"github.com/yabanci/claude-remote/internal/config"
)

const scrollbackEvictedReply = "ответ недоступен — экран сессии прокрутился дальше истории, посмотри /cr_peek"

func DiffTail(before, after string) string {
	beforeLines := strings.Split(before, "\n")
	afterLines := strings.Split(after, "\n")

	common := 0
	for common < len(beforeLines) && common < len(afterLines) && beforeLines[common] == afterLines[common] {
		common++
	}

	if scrollbackLikelyEvicted(common, beforeLines, afterLines) {
		return scrollbackEvictedReply
	}
	return strings.TrimSpace(strings.Join(afterLines[common:], "\n"))
}

func scrollbackLikelyEvicted(common int, beforeLines, afterLines []string) bool {
	return common == 0 && len(beforeLines) >= captureHistoryLines && len(afterLines) >= captureHistoryLines
}

type CaptureFunc func() (string, error)

type InterimFunc func(elapsed time.Duration)

func WaitForSettle(capture CaptureFunc, cfg config.SettleConfig, onInterim InterimFunc) (string, error) {
	pollInterval := cfg.PollInterval()
	hardCap := cfg.HardCapDuration()
	interimEvery := cfg.InterimNoticeDuration()

	startedAt := time.Now()
	last, err := capture()
	if err != nil {
		return "", err
	}

	elapsed := time.Since(startedAt)
	var lastNotice time.Duration
	stableRounds := 0

	for stableRounds < cfg.StableRounds && elapsed < hardCap {
		time.Sleep(pollInterval)

		current, err := capture()
		if err != nil {
			return "", err
		}
		elapsed = time.Since(startedAt)

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
