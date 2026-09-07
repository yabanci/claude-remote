package tmux_test

import (
	"fmt"
	"os/exec"
	"testing"
	"time"

	"github.com/yabanci/claude-remote/internal/tmux"
)

func benchSession(b *testing.B, fillLines int) string {
	b.Helper()
	if _, err := exec.LookPath("tmux"); err != nil {
		b.Skip("tmux not installed")
	}
	name := fmt.Sprintf("cr-bench-%d", time.Now().UnixNano())
	if err := tmux.Start(name, b.TempDir(), ""); err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { _ = tmux.Kill(name) })

	fill := fmt.Sprintf("for i in $(seq 1 %d); do echo \"line $i some representative claude output text goes here\"; done", fillLines)
	if err := tmux.SendKeys(name, fill); err != nil {
		b.Fatal(err)
	}
	time.Sleep(5 * time.Second)
	return name
}

func BenchmarkCapturePaneFullHistory(b *testing.B) {
	name := benchSession(b, 3000)
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		out, err := tmux.CapturePane(name, 5000)
		if err != nil {
			b.Fatal(err)
		}
		if len(out) == 0 {
			b.Fatal("empty capture")
		}
	}
}

func BenchmarkCapturePaneVisibleOnly(b *testing.B) {
	name := benchSession(b, 3000)
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		out, err := tmux.CapturePane(name, 0)
		if err != nil {
			b.Fatal(err)
		}
		if len(out) == 0 {
			b.Fatal("empty capture")
		}
	}
}
