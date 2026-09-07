package bridge_test

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/yabanci/claude-remote/internal/bridge"
)

func FuzzSplitForTelegram(f *testing.F) {
	f.Add("короткий ответ", 3500)
	f.Add(strings.Repeat("проверка кириллицы\n", 300), 3500)
	f.Add(strings.Repeat("я", 5000), 10)
	f.Add("mixed текст 混合 🚀\nsecond line\n", 7)
	f.Add("", 100)
	f.Add("\n\n\n", 2)

	f.Fuzz(func(t *testing.T, text string, limit int) {
		if limit < 1 || limit > 1<<16 {
			t.Skip()
		}
		if !utf8.ValidString(text) {
			t.Skip()
		}

		chunks := bridge.SplitForTelegram(text, limit)

		if got := strings.Join(chunks, ""); got != text {
			t.Fatalf("chunks do not rejoin into the original\n got: %q\nwant: %q", got, text)
		}
		for i, c := range chunks {
			if !utf8.ValidString(c) {
				t.Fatalf("chunk %d is not valid UTF-8: %q", i, c)
			}
			if len(text) > limit && len(c) > limit && utf8.RuneCountInString(c) > 1 {
				t.Fatalf("chunk %d is %d bytes, limit is %d: %q", i, len(c), limit, c)
			}
		}
	})
}

func FuzzDiffTail(f *testing.F) {
	f.Add("hello\nworld", "hello\nworld\nnew")
	f.Add("", "reply")
	f.Add("same", "same")
	f.Add("многострочный\nответ", "многострочный\nответ\nхвост")

	f.Fuzz(func(t *testing.T, before, after string) {
		diff := bridge.DiffTail(before, after)

		if diff == "" {
			return
		}
		if !strings.Contains(after, strings.TrimSpace(diff)) {
			t.Fatalf("diff is not a substring of the new screen\ndiff: %q\nafter: %q", diff, after)
		}
	})
}
