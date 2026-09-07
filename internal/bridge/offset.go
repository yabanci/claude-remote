package bridge

import (
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

type offsetStore struct {
	path string
	log  *slog.Logger
}

func newOffsetStore(stateDir string, log *slog.Logger) *offsetStore {
	return &offsetStore{path: filepath.Join(stateDir, "offset.txt"), log: log}
}

func (s *offsetStore) load() int64 {
	data, err := os.ReadFile(s.path)
	if err != nil {
		return 0
	}
	offset, err := strconv.ParseInt(strings.TrimSpace(string(data)), 10, 64)
	if err != nil {
		s.log.Warn("offset file is unreadable, starting from scratch", "path", s.path, "err", err)
		return 0
	}
	return offset
}

func (s *offsetStore) save(offset int64) {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		s.log.Error("create state dir failed", "path", filepath.Dir(s.path), "err", err)
		return
	}
	if err := os.WriteFile(s.path, []byte(strconv.FormatInt(offset, 10)), 0o600); err != nil {
		s.log.Error("save offset failed", "path", s.path, "err", err)
	}
}
