package bridge

import (
	"regexp"
	"strings"

	"github.com/yabanci/claude-remote/internal/telegram"
)

const menuButtonsPerRow = 3

var menuOptionPattern = regexp.MustCompile(`^\s*[❯>]?\s*(\d{1,2})\.\s+(\S.*)$`)

type MenuOption struct {
	Key   string
	Label string
}

type Menu struct {
	Question string
	Options  []MenuOption
}

func ParseMenu(pane string) (Menu, bool) {
	if !AwaitsInteractiveChoice(pane) {
		return Menu{}, false
	}

	var options []MenuOption
	var question []string

	for _, line := range strings.Split(pane, "\n") {
		if match := menuOptionPattern.FindStringSubmatch(line); match != nil {
			options = append(options, MenuOption{Key: match[1], Label: strings.TrimSpace(match[2])})
			continue
		}
		if isChrome(line) || strings.Contains(line, choicePromptMarker) {
			continue
		}
		if trimmed := strings.TrimSpace(line); trimmed != "" && len(options) == 0 {
			question = append(question, trimmed)
		}
	}

	if len(options) == 0 {
		return Menu{}, false
	}
	return Menu{Question: strings.Join(question, "\n"), Options: options}, true
}

func (m Menu) heading() string {
	if m.Question == "" {
		return "сессия ждёт выбора"
	}
	return m.Question
}

func (m Menu) keyboardRows() [][]telegram.InlineButton {
	var rows [][]telegram.InlineButton
	for start := 0; start < len(m.Options); start += menuButtonsPerRow {
		end := min(start+menuButtonsPerRow, len(m.Options))
		row := make([]telegram.InlineButton, 0, end-start)
		for _, option := range m.Options[start:end] {
			row = append(row, telegram.InlineButton{Text: option.button(), CallbackData: option.Key})
		}
		rows = append(rows, row)
	}
	return rows
}

func (m Menu) asPlainText() string {
	lines := []string{m.heading(), ""}
	for _, option := range m.Options {
		lines = append(lines, option.button())
	}
	return strings.Join(append(lines, "", "Ответь номером варианта."), "\n")
}

func (o MenuOption) button() string {
	return o.Key + ". " + o.Label
}
