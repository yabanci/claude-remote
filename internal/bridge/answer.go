package bridge

import (
	"fmt"
	"regexp"
	"strings"
)

const (
	answerMarker     = "⏺"
	toolResultMarker = "⎿"
	spinnerMarker    = "✻"
	markerGutter     = "  "

	fallbackWarnMinLines = 4
)

var toolCallPattern = regexp.MustCompile(`^[A-Z][A-Za-z]*\(`)

type Answer struct {
	Text       string
	ToolBlocks int
}

func ExtractAnswer(pane string) Answer {
	lines := strings.Split(pane, "\n")
	var text []string
	tools := 0
	inTextBlock := false

	for i, line := range lines {
		trimmed := strings.TrimSpace(line)

		if isChrome(line) || strings.HasPrefix(trimmed, spinnerMarker) {
			continue
		}
		if strings.HasPrefix(trimmed, toolResultMarker) {
			inTextBlock = false
			continue
		}

		if body, found := strings.CutPrefix(trimmed, answerMarker); found {
			body = strings.TrimSpace(body)
			if toolCallPattern.MatchString(body) && followedByToolResult(lines, i) {
				tools++
				inTextBlock = false
				continue
			}
			inTextBlock = true
			text = append(text, body)
			continue
		}

		if inTextBlock {
			text = append(text, strings.TrimRight(stripMarkerGutter(line), " \t"))
		}
	}

	return Answer{
		Text:       strings.TrimSpace(strings.Join(collapseBlankRuns(text), "\n")),
		ToolBlocks: tools,
	}
}

func followedByToolResult(lines []string, i int) bool {
	if i+1 >= len(lines) {
		return false
	}
	return strings.HasPrefix(strings.TrimSpace(lines[i+1]), toolResultMarker)
}

func stripMarkerGutter(line string) string {
	return strings.TrimPrefix(line, markerGutter)
}

func FormatReply(pane string) (string, bool) {
	answer := ExtractAnswer(pane)
	if answer.Text != "" {
		return answer.Text, false
	}
	if answer.ToolBlocks > 0 {
		return fmt.Sprintf("сессия выполнила %d действий, но текстового ответа не дала", answer.ToolBlocks), false
	}

	cleaned := CleanReply(pane)
	return cleaned, strings.Count(cleaned, "\n") >= fallbackWarnMinLines-1
}

func ExpectedMarkers() []string {
	return []string{answerMarker, toolResultMarker, spinnerMarker}
}
