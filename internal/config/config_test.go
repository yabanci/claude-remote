package config_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yabanci/claude-remote/internal/config"
)

func TestValidate(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(c *config.Config)
		wantErr bool
	}{
		{"valid default", func(c *config.Config) {}, false},
		{"no token", func(c *config.Config) { c.BotToken = "" }, true},
		{"no allowed users is valid bootstrap state", func(c *config.Config) { c.AllowedUsers = nil }, false},
		{"no sessions", func(c *config.Config) { c.Sessions = nil }, true},
		{"default session missing", func(c *config.Config) { c.DefaultSession = "ghost" }, true},
		{"session with empty dir", func(c *config.Config) {
			c.Sessions["main"] = config.SessionConfig{Dir: "", Command: "claude"}
		}, true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg := config.Default()
			cfg.BotToken = "test-token"
			cfg.AllowedUsers = []int64{1}
			tc.mutate(&cfg)

			err := cfg.Validate()
			if tc.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
		})
	}
}

func TestValidateReportsAllMissingSessionDirs(t *testing.T) {
	cfg := config.Default()
	cfg.BotToken = "test-token"
	cfg.AllowedUsers = []int64{1}
	cfg.Sessions["main"] = config.SessionConfig{Dir: t.TempDir(), Command: "claude"}
	cfg.Sessions["ghost-one"] = config.SessionConfig{Dir: "/no/such/dir/one", Command: "claude"}
	cfg.Sessions["ghost-two"] = config.SessionConfig{Dir: "/no/such/dir/two", Command: "claude"}

	err := cfg.Validate()

	require.Error(t, err)
	assert.ErrorContains(t, err, "ghost-one")
	assert.ErrorContains(t, err, "ghost-two")
}

func TestValidateExpandsHomeBeforeCheckingSessionDir(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	require.NoError(t, os.MkdirAll(filepath.Join(home, "work"), 0o755))

	cfg := config.Default()
	cfg.BotToken = "test-token"
	cfg.AllowedUsers = []int64{1}
	cfg.Sessions["main"] = config.SessionConfig{Dir: "~/work", Command: "claude"}

	assert.NoError(t, cfg.Validate())
}

func TestSaveLoadRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")

	cfg := config.Default()
	cfg.BotToken = "abc123"
	cfg.AllowedUsers = []int64{42}
	cfg.Sessions["work"] = config.SessionConfig{Dir: "/tmp/work", Command: "claude"}

	require.NoError(t, config.Save(path, cfg))

	loaded, err := config.Load(path)
	require.NoError(t, err)

	assert.Equal(t, cfg.BotToken, loaded.BotToken)
	assert.Equal(t, cfg.AllowedUsers, loaded.AllowedUsers)
	assert.Equal(t, cfg.Sessions["work"], loaded.Sessions["work"])
	assert.Equal(t, cfg.Settle, loaded.Settle)
}

func TestSaveWritesAtomicallyNoTempFileLeftBehind(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")

	cfg := config.Default()
	cfg.BotToken = "abc123"
	require.NoError(t, config.Save(path, cfg))

	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	require.Len(t, entries, 1, "only config.yaml should remain, no leftover temp file")
	assert.Equal(t, "config.yaml", entries[0].Name())

	info, err := os.Stat(path)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), info.Mode().Perm())
}

func TestSaveReplacesExistingConfigWholesale(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	require.NoError(t, os.WriteFile(path, []byte("a very long stale config body that must not survive a save"), 0o600))

	cfg := config.Default()
	cfg.BotToken = "fresh-token"
	require.NoError(t, config.Save(path, cfg))

	data, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.NotContains(t, string(data), "stale config body")
}

func TestSaveSaysWhichStepFailedWhenTheConfigDirCannotBeCreated(t *testing.T) {
	occupied := filepath.Join(t.TempDir(), "not-a-dir")
	require.NoError(t, os.WriteFile(occupied, []byte("x"), 0o600))

	err := config.Save(filepath.Join(occupied, "config.yaml"), config.Default())

	require.Error(t, err)
	assert.Contains(t, err.Error(), "create config dir",
		"the caller has to be able to tell a bad directory from a bad write")
}

func TestSaveSaysWhichStepFailedWhenTheConfigCannotBeWritten(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, os.Mkdir(path, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(path, "keepme"), []byte("x"), 0o600))

	err := config.Save(path, config.Default())

	require.Error(t, err)
	assert.Contains(t, err.Error(), "write config",
		"the caller has to be able to tell a bad write from a bad directory")
	assert.Contains(t, err.Error(), path, "and which file it was")
}

func TestApplySettleDefaultsOnPartialConfig(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	require.NoError(t, config.Save(path, config.Config{
		BotToken:       "x",
		DefaultSession: "main",
		Sessions:       map[string]config.SessionConfig{"main": {Dir: "/tmp"}},
	}))

	loaded, err := config.Load(path)
	require.NoError(t, err)

	assert.Equal(t, config.Default().Settle, loaded.Settle)
}

func TestIsAllowed(t *testing.T) {
	cfg := config.Config{AllowedUsers: []int64{10, 20}}

	assert.True(t, cfg.IsAllowed(10))
	assert.False(t, cfg.IsAllowed(30))
}

func TestNeedsBootstrap(t *testing.T) {
	assert.True(t, config.Config{}.NeedsBootstrap())
	assert.False(t, config.Config{AllowedUsers: []int64{1}}.NeedsBootstrap())
}

func TestResolveTokenPrefersEnv(t *testing.T) {
	t.Setenv(config.EnvBotToken, "from-env")
	cfg := config.Config{BotToken: "from-file"}

	assert.Equal(t, "from-env", cfg.ResolveToken())
}

func TestExpandDir(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	got, err := config.ExpandDir("~/projects")
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(home, "projects"), got)

	got, err = config.ExpandDir("/abs/path")
	require.NoError(t, err)
	assert.Equal(t, "/abs/path", got)
}

func TestSessionsFromFileReplaceDefaults(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, os.WriteFile(path, []byte(`
bot_token: "x"
allowed_users: [1]
default_session: work
sessions:
  work:
    dir: /tmp/work
    command: claude
`), 0o600))

	cfg, err := config.Load(path)

	require.NoError(t, err)
	assert.Len(t, cfg.Sessions, 1, "a session the user never declared must not appear")
	assert.NotContains(t, cfg.Sessions, "main")
	assert.Equal(t, "/tmp/work", cfg.Sessions["work"].Dir)
}

func TestDefaultSessionSurvivesWhenFileOmitsSessions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, os.WriteFile(path, []byte("bot_token: \"x\"\nallowed_users: [1]\n"), 0o600))

	cfg, err := config.Load(path)

	require.NoError(t, err)
	assert.Contains(t, cfg.Sessions, "main")
}
