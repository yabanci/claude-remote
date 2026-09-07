package bridge

import "strings"

const boxDrawingRunes = "─━│┃╭╮╰╯┌┐└┘├┤┬┴┼═║"

func CleanReply(text string) string {
	var kept []string
	for _, line := range strings.Split(text, "\n") {
		if isChrome(line) {
			continue
		}
		kept = append(kept, strings.TrimRight(line, " \t"))
	}
	return strings.TrimSpace(strings.Join(collapseBlankRuns(kept), "\n"))
}

func isChrome(line string) bool {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" {
		return false
	}
	if isRuler(trimmed) {
		return true
	}
	if strings.HasPrefix(trimmed, "❯") {
		return true
	}
	if trimmed == "/rc" {
		return true
	}
	if strings.Contains(trimmed, "auto mode on (shift+tab") {
		return true
	}
	if strings.Contains(trimmed, "(esc to interrupt)") {
		return true
	}
	if isMeter(trimmed, "Context") || isMeter(trimmed, "Usage") || isMeter(trimmed, "Weekly") {
		return true
	}
	if strings.HasPrefix(trimmed, "[") && strings.Contains(trimmed, "│") {
		return true
	}
	return false
}

func isRuler(trimmed string) bool {
	return strings.TrimLeft(trimmed, boxDrawingRunes+" ") == ""
}

func isMeter(trimmed, label string) bool {
	return strings.HasPrefix(trimmed, label) && strings.Contains(trimmed, "%")
}

func collapseBlankRuns(lines []string) []string {
	var out []string
	blank := false
	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			if blank {
				continue
			}
			blank = true
		} else {
			blank = false
		}
		out = append(out, line)
	}
	return out
}
