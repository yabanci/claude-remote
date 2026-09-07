package bridge

import (
	"strings"
	"unicode/utf8"
)

const (
	trustPromptMarker  = "Yes, I trust this folder"
	choicePromptMarker = "Enter to confirm"
	maxChoiceRunes     = 3
)

func AwaitsTrustConfirmation(pane string) bool {
	return strings.Contains(pane, trustPromptMarker)
}

func AwaitsInteractiveChoice(pane string) bool {
	return strings.Contains(pane, choicePromptMarker)
}

func IsDeliberateChoice(text string) bool {
	return utf8.RuneCountInString(strings.TrimSpace(text)) <= maxChoiceRunes
}
