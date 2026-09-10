package atomicfile_test

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yabanci/claude-remote/internal/atomicfile"
)

func TestWriteCreatesFileWithContentAndPerm(t *testing.T) {
	path := filepath.Join(t.TempDir(), "target.txt")

	require.NoError(t, atomicfile.Write(path, []byte("first"), 0o600))

	got, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, "first", string(got))

	if runtime.GOOS != "windows" {
		info, err := os.Stat(path)
		require.NoError(t, err)
		assert.Equal(t, os.FileMode(0o600), info.Mode().Perm())
	}
}

func TestWriteReplacesExistingContentWholesale(t *testing.T) {
	path := filepath.Join(t.TempDir(), "target.txt")
	require.NoError(t, os.WriteFile(path, []byte("a very long previous value that must not survive"), 0o600))

	require.NoError(t, atomicfile.Write(path, []byte("new"), 0o600))

	got, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, "new", string(got), "old content must be fully replaced, not partially overwritten or appended")
}

func TestWriteLeavesNoTempFileBehindOnSuccess(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "target.txt")

	require.NoError(t, atomicfile.Write(path, []byte("done"), 0o600))

	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	require.Len(t, entries, 1, "only the final target file should remain, no leftover temp file")
	assert.Equal(t, "target.txt", entries[0].Name())
}

func TestWriteFailsCleanlyWhenDirMissing(t *testing.T) {
	path := filepath.Join(t.TempDir(), "does-not-exist", "target.txt")

	err := atomicfile.Write(path, []byte("data"), 0o600)

	require.Error(t, err)
	_, statErr := os.Stat(path)
	assert.True(t, os.IsNotExist(statErr), "no partial file should appear at the target path")
}

func TestWriteLeavesOriginalIntactWhenRenameFails(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "target")
	require.NoError(t, os.Mkdir(path, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(path, "keepme"), []byte("x"), 0o600))

	err := atomicfile.Write(path, []byte("new data"), 0o600)

	require.Error(t, err, "renaming a file onto a non-empty directory must fail")

	info, statErr := os.Stat(path)
	require.NoError(t, statErr)
	assert.True(t, info.IsDir(), "original path must be untouched when the rename step fails")

	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	for _, e := range entries {
		assert.NotContains(t, e.Name(), ".tmp-", "a failed rename must not leak its temp file")
	}
}
