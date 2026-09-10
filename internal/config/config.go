package config

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/yabanci/claude-remote/internal/atomicfile"
)

const (
	EnvBotToken = "CLAUDE_REMOTE_BOT_TOKEN"

	defaultPollIntervalMS       = 1500
	defaultStableRounds         = 3
	defaultHardCapSeconds       = 1200
	defaultInterimNoticeSeconds = 120
	defaultColdStartDelayMS     = 3000
	defaultPostSendDelayMS      = 2000
)

type SessionConfig struct {
	Dir     string `yaml:"dir"`
	Command string `yaml:"command"`
}

type SettleConfig struct {
	PollIntervalMS       int `yaml:"poll_interval_ms"`
	StableRounds         int `yaml:"stable_rounds"`
	HardCapSeconds       int `yaml:"hard_cap_seconds"`
	InterimNoticeSeconds int `yaml:"interim_notice_seconds"`
	ColdStartDelayMS     int `yaml:"cold_start_delay_ms"`
	PostSendDelayMS      int `yaml:"post_send_delay_ms"`
}

type Config struct {
	BotToken       string                   `yaml:"bot_token"`
	APIBase        string                   `yaml:"api_base"`
	MaxRetries     int                      `yaml:"max_retries"`
	AllowedUsers   []int64                  `yaml:"allowed_users"`
	AllowedChats   []int64                  `yaml:"allowed_chats"`
	DefaultSession string                   `yaml:"default_session"`
	Sessions       map[string]SessionConfig `yaml:"sessions"`
	Settle         SettleConfig             `yaml:"settle"`
}

func Default() Config {
	home, _ := os.UserHomeDir()
	return Config{
		DefaultSession: "main",
		Sessions: map[string]SessionConfig{
			"main": {Dir: home, Command: "claude"},
		},
		Settle: SettleConfig{
			PollIntervalMS:       defaultPollIntervalMS,
			StableRounds:         defaultStableRounds,
			HardCapSeconds:       defaultHardCapSeconds,
			InterimNoticeSeconds: defaultInterimNoticeSeconds,
			ColdStartDelayMS:     defaultColdStartDelayMS,
			PostSendDelayMS:      defaultPostSendDelayMS,
		},
	}
}

func DefaultPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("resolve user config dir: %w", err)
	}
	return filepath.Join(dir, "claude-remote", "config.yaml"), nil
}

func Load(path string) (Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Config{}, fmt.Errorf("read config %s: %w", path, err)
	}

	cfg := Default()
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return Config{}, fmt.Errorf("parse config %s: %w", path, err)
	}

	var declared struct {
		Sessions map[string]SessionConfig `yaml:"sessions"`
	}
	if err := yaml.Unmarshal(data, &declared); err != nil {
		return Config{}, fmt.Errorf("parse config %s: %w", path, err)
	}
	if declared.Sessions != nil {
		cfg.Sessions = declared.Sessions
	}

	applySettleDefaults(&cfg.Settle)
	return cfg, nil
}

func applySettleDefaults(s *SettleConfig) {
	if s.PollIntervalMS <= 0 {
		s.PollIntervalMS = defaultPollIntervalMS
	}
	if s.StableRounds <= 0 {
		s.StableRounds = defaultStableRounds
	}
	if s.HardCapSeconds <= 0 {
		s.HardCapSeconds = defaultHardCapSeconds
	}
	if s.InterimNoticeSeconds <= 0 {
		s.InterimNoticeSeconds = defaultInterimNoticeSeconds
	}
	if s.ColdStartDelayMS <= 0 {
		s.ColdStartDelayMS = defaultColdStartDelayMS
	}
	if s.PostSendDelayMS <= 0 {
		s.PostSendDelayMS = defaultPostSendDelayMS
	}
}

func Save(path string, cfg Config) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create config dir: %w", err)
	}

	data, err := yaml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}

	if err := atomicfile.Write(path, data, 0o600); err != nil {
		return fmt.Errorf("write config %s: %w", path, err)
	}
	return nil
}

func (c Config) Validate() error {
	if strings.TrimSpace(c.BotToken) == "" && os.Getenv(EnvBotToken) == "" {
		return fmt.Errorf("bot_token is empty and %s is not set", EnvBotToken)
	}
	if len(c.Sessions) == 0 {
		return fmt.Errorf("sessions is empty — define at least one")
	}
	if _, ok := c.Sessions[c.DefaultSession]; !ok {
		return fmt.Errorf("default_session %q is not defined in sessions", c.DefaultSession)
	}
	var missing []string
	for name, s := range c.Sessions {
		if strings.TrimSpace(s.Dir) == "" {
			return fmt.Errorf("session %q: dir is empty", name)
		}
		dir, err := ExpandDir(s.Dir)
		if err != nil {
			return fmt.Errorf("session %q: %w", name, err)
		}
		if _, err := os.Stat(dir); err != nil {
			missing = append(missing, fmt.Sprintf("%s (%s)", name, dir))
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		return fmt.Errorf("session dir does not exist: %s", strings.Join(missing, ", "))
	}
	return nil
}

func (c Config) ResolveToken() string {
	if v := os.Getenv(EnvBotToken); v != "" {
		return v
	}
	return c.BotToken
}

func (c Config) IsAllowed(userID int64) bool {
	for _, id := range c.AllowedUsers {
		if id == userID {
			return true
		}
	}
	return false
}

func (c Config) NeedsBootstrap() bool {
	return len(c.AllowedUsers) == 0
}

func (c Config) IsAllowedChat(chatID int64) bool {
	if len(c.AllowedChats) == 0 {
		return true
	}
	for _, id := range c.AllowedChats {
		if id == chatID {
			return true
		}
	}
	return false
}

func (s SettleConfig) PollInterval() time.Duration {
	return time.Duration(s.PollIntervalMS) * time.Millisecond
}

func (s SettleConfig) HardCapDuration() time.Duration {
	return time.Duration(s.HardCapSeconds) * time.Second
}

func (s SettleConfig) InterimNoticeDuration() time.Duration {
	return time.Duration(s.InterimNoticeSeconds) * time.Second
}

func (s SettleConfig) ColdStartDelay() time.Duration {
	return time.Duration(s.ColdStartDelayMS) * time.Millisecond
}

func (s SettleConfig) PostSendDelay() time.Duration {
	return time.Duration(s.PostSendDelayMS) * time.Millisecond
}

func ExpandDir(dir string) (string, error) {
	if dir == "~" || strings.HasPrefix(dir, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("resolve home dir: %w", err)
		}
		return filepath.Join(home, strings.TrimPrefix(dir, "~")), nil
	}
	return dir, nil
}
