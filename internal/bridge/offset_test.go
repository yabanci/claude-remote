package bridge

import (
	"bytes"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestOffsetStoreSaveWritesAtomicallyNoTempFileLeftBehind(t *testing.T) {
	dir := t.TempDir()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	store := newOffsetStore(dir, logger)

	store.save(42)

	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	require.Len(t, entries, 1, "only offset.txt should remain, no leftover temp file")
	assert.Equal(t, "offset.txt", entries[0].Name())

	info, err := os.Stat(filepath.Join(dir, "offset.txt"))
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), info.Mode().Perm())

	assert.Equal(t, int64(42), store.load())
}

func TestOffsetStoreSaveReplacesExistingContentWholesale(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "offset.txt"), []byte("999999999999"), 0o600))

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	store := newOffsetStore(dir, logger)
	store.save(7)

	assert.Equal(t, int64(7), store.load())
}

func TestOffsetStoreLoadMissingFileIsSilent(t *testing.T) {
	dir := t.TempDir()
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))
	store := newOffsetStore(dir, logger)

	assert.Equal(t, int64(0), store.load())
	assert.Empty(t, buf.String(), "a missing offset file on first run is normal, not worth a log line")
}

func TestOffsetStoreLoadWarnsOnNonMissingReadError(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.Mkdir(filepath.Join(dir, "offset.txt"), 0o700))
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))
	store := newOffsetStore(dir, logger)

	assert.Equal(t, int64(0), store.load())
	assert.Contains(t, buf.String(), "offset file read failed")
}
