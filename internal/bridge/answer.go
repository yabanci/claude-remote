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
)

var toolCallPattern = regexp.MustCompile(`^[A-Z][A-Za-z]*\(`)

type Answer struct {
	Text       string
	ToolBlocks int
}

func ExtractAnswer(pane string) Answer {
	var text []string
	tools := 0
	inTextBlock := false

	for _, line := range strings.Split(pane, "\n") {
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
			if toolCallPattern.MatchString(body) {
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

func stripMarkerGutter(line string) string {
	return strings.TrimPrefix(line, markerGutter)
}

func FormatReply(pane string) string {
	answer := ExtractAnswer(pane)
	if answer.Text != "" {
		return answer.Text
	}
	if answer.ToolBlocks > 0 {
		return fmt.Sprintf("сессия выполнила %d действий, но текстового ответа не дала", answer.ToolBlocks)
	}
	return CleanReply(pane)
}
