package bridge

import "strings"

const promptAnchorRunes = 24

func TailAfterPrompt(pane, sent string) (string, bool) {
	anchor := promptAnchor(sent)
	if anchor == "" {
		return "", false
	}
	if tail, ok := tailAfterMarkedEcho(pane, anchor); ok {
		return tail, true
	}
	return tailAfterWrappedEcho(pane, anchor)
}

func tailAfterMarkedEcho(pane, anchor string) (string, bool) {
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

func tailAfterWrappedEcho(pane, anchor string) (string, bool) {
	flat := strings.ReplaceAll(pane, "\n", " ")
	idx := strings.LastIndex(flat, anchor)
	if idx < 0 {
		return "", false
	}
	if !strings.Contains(pane[idx:idx+len(anchor)], "\n") {
		return "", false
	}
	return pane[idx+len(anchor):], true
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
