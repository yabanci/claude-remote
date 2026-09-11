package bridge

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/yabanci/claude-remote/internal/config"
)

const (
	errSessionExists      = "сессия %q уже существует, используй /cr_use"
	defaultSessionCommand = "claude"
)

type replyToKey struct{}

func withReplyTo(ctx context.Context, messageID int64) context.Context {
	if messageID == 0 {
		return ctx
	}
	return context.WithValue(ctx, replyToKey{}, messageID)
}

func replyToOf(ctx context.Context) int64 {
	id, _ := ctx.Value(replyToKey{}).(int64)
	return id
}

type sessionLocks struct {
	mu    sync.Mutex
	locks map[string]*sync.Mutex
}

func newSessionLocks() *sessionLocks {
	return &sessionLocks{locks: make(map[string]*sync.Mutex)}
}

func (s *sessionLocks) of(name string) *sync.Mutex {
	s.mu.Lock()
	defer s.mu.Unlock()
	lock, ok := s.locks[name]
	if !ok {
		lock = &sync.Mutex{}
		s.locks[name] = lock
	}
	return lock
}

var lockFreeCommands = map[string]bool{
	"/cr_interrupt": true,
	"/cr_kill":      true,
	"/cr_status":    true,
	"/cr_peek":      true,
}

func runsWhileSessionIsBusy(text string) bool {
	trimmed := strings.TrimSpace(text)
	if !strings.HasPrefix(trimmed, "/cr_") {
		return false
	}
	name, _, _ := strings.Cut(trimmed, " ")
	return lockFreeCommands[stripBotSuffix(name)]
}

func (b *Bridge) inSessionTurn(chatID int64, turn func()) {
	lock := b.turns.of(b.activeSessionName(chatID))
	lock.Lock()
	defer lock.Unlock()
	turn()
}

func (b *Bridge) needsBootstrap() bool {
	b.state.Lock()
	defer b.state.Unlock()
	return b.cfg.NeedsBootstrap()
}

func (b *Bridge) allowedSender(userID, chatID int64) bool {
	b.state.Lock()
	defer b.state.Unlock()
	return b.cfg.IsAllowed(userID) && b.cfg.IsAllowedChat(chatID)
}

func (b *Bridge) settle() config.SettleConfig {
	b.state.Lock()
	defer b.state.Unlock()
	return b.cfg.Settle
}

func (b *Bridge) sessionsSnapshot() map[string]config.SessionConfig {
	b.state.Lock()
	defer b.state.Unlock()
	snapshot := make(map[string]config.SessionConfig, len(b.cfg.Sessions))
	for name, sc := range b.cfg.Sessions {
		snapshot[name] = sc
	}
	return snapshot
}

func (b *Bridge) sessionConfig(name string) (config.SessionConfig, bool) {
	b.state.Lock()
	defer b.state.Unlock()
	sc, ok := b.cfg.Sessions[name]
	return sc, ok
}

func (b *Bridge) bindOwner(userID, chatID int64) error {
	b.state.Lock()
	defer b.state.Unlock()
	candidate := b.cfg
	candidate.AllowedUsers = []int64{userID}
	candidate.AllowedChats = []int64{chatID}
	if err := config.Save(b.configPath, candidate); err != nil {
		return err
	}
	b.cfg = candidate
	return nil
}

func (b *Bridge) hasSession(name string) bool {
	b.state.Lock()
	defer b.state.Unlock()
	_, ok := b.cfg.Sessions[name]
	return ok
}

func (b *Bridge) setActiveSession(chatID int64, name string) {
	b.state.Lock()
	defer b.state.Unlock()
	b.activeSession[chatID] = name
}

func (b *Bridge) activeSessionName(chatID int64) string {
	b.state.Lock()
	defer b.state.Unlock()
	return b.activeSessionNameLocked(chatID)
}

func (b *Bridge) activeSessionNameLocked(chatID int64) string {
	if name, ok := b.activeSession[chatID]; ok {
		if _, exists := b.cfg.Sessions[name]; exists {
			return name
		}
	}
	return b.cfg.DefaultSession
}

func (b *Bridge) addSession(chatID int64, name, rawDir string) error {
	b.state.Lock()
	defer b.state.Unlock()
	if _, exists := b.cfg.Sessions[name]; exists {
		return fmt.Errorf(errSessionExists, name)
	}
	b.cfg.Sessions[name] = config.SessionConfig{Dir: rawDir, Command: defaultSessionCommand}
	if err := config.Save(b.configPath, b.cfg); err != nil {
		delete(b.cfg.Sessions, name)
		return fmt.Errorf("не удалось сохранить конфиг: %w", err)
	}
	b.activeSession[chatID] = name
	return nil
}

func (b *Bridge) rollbackNewSession(chatID int64, name string) error {
	b.state.Lock()
	defer b.state.Unlock()
	delete(b.cfg.Sessions, name)
	saveErr := config.Save(b.configPath, b.cfg)
	if saveErr != nil {
		b.log.Error("rollback: save config failed after session start error, entry left dangling", "session", name, "path", b.configPath, "err", saveErr)
	}
	if b.activeSession[chatID] == name {
		delete(b.activeSession, chatID)
	}
	return saveErr
}
