package bridge

import "strings"

const promptAnchorRunes = 24

func TailAfterPrompt(pane, sent string) (string, bool) {
	anchor := promptAnchor(sent)
	if anchor == "" {
		return "", false
	}

	lines := strings.Split(pane, "\n")
	found := -1
	for i, line := range lines {
		if isInputEcho(line) && strings.Contains(line, anchor) {
			found = i
		}
	}
	if found < 0 {
		return "", false
	}
	return strings.Join(lines[found+1:], "\n"), true
}

func isInputEcho(line string) bool {
	trimmed := strings.TrimSpace(line)
	return strings.HasPrefix(trimmed, "❯") || strings.HasPrefix(trimmed, ">")
}

func promptAnchor(sent string) string {
	first := sent
	if idx := strings.IndexByte(first, '\n'); idx >= 0 {
		first = first[:idx]
	}
	first = strings.TrimSpace(first)
	if first == "" {
		return ""
	}

	runes := []rune(first)
	if len(runes) > promptAnchorRunes {
		runes = runes[:promptAnchorRunes]
	}
	return strings.TrimSpace(string(runes))
}
