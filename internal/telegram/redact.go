package telegram

import (
	"fmt"
	"regexp"
)

var tokenInURL = regexp.MustCompile(`/bot[0-9]+:[A-Za-z0-9_-]+`)

func redact(err error) error {
	if err == nil {
		return nil
	}
	text := err.Error()
	clean := tokenInURL.ReplaceAllString(text, "/bot***")
	if clean == text {
		return err
	}
	return fmt.Errorf("%s", clean)
}
