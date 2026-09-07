package bridge

import (
	"regexp"
	"strings"
)

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
