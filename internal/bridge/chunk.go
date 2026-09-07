package bridge

import (
	"strings"
	"unicode/utf8"
)

type chunker struct {
	limit   int
	current strings.Builder
	chunks  []string
}

func (c *chunker) add(piece string) {
	if c.current.Len()+len(piece) > c.limit {
		c.flush()
	}
	c.current.WriteString(piece)
}

func (c *chunker) flush() {
	if c.current.Len() == 0 {
		return
	}
	c.chunks = append(c.chunks, c.current.String())
	c.current.Reset()
}

func (c *chunker) result() []string {
	c.flush()
	return c.chunks
}

func SplitForTelegram(text string, limit int) []string {
	if limit <= 0 || len(text) <= limit {
		return []string{text}
	}

	c := &chunker{limit: limit}
	for _, line := range strings.SplitAfter(text, "\n") {
		for _, piece := range splitRuneSafe(line, limit) {
			c.add(piece)
		}
	}

	chunks := c.result()
	if len(chunks) == 0 {
		return []string{text}
	}
	return chunks
}

func splitRuneSafe(s string, limit int) []string {
	if len(s) <= limit {
		return []string{s}
	}

	var pieces []string
	for len(s) > limit {
		cut := limit
		for cut > 0 && !utf8.RuneStart(s[cut]) {
			cut--
		}
		if cut == 0 {
			_, size := utf8.DecodeRuneInString(s)
			cut = size
		}
		pieces = append(pieces, s[:cut])
		s = s[cut:]
	}
	if s != "" {
		pieces = append(pieces, s)
	}
	return pieces
}
