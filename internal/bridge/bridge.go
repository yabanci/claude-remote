package bridge

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/yabanci/claude-remote/internal/config"
	"github.com/yabanci/claude-remote/internal/telegram"
)

const (
	captureHistoryLines = 5000
	visiblePaneOnly     = 0
	maxInlineReplyLen   = 3500
	maxTotalInlineLen   = 12000
	getUpdatesTimeoutS  = 25

	replyDeliveryTimeout = 2 * time.Minute
)

type Bridge struct {
	cfg        config.Config
	configPath string
	tg         *telegram.Client
	runner     Runner
	log        *slog.Logger
	stateDir   string
	offsets    *offsetStore

	activeSession map[int64]string
}

func New(cfg config.Config, configPath string, tg *telegram.Client, runner Runner, log *slog.Logger, stateDir string) *Bridge {
	return &Bridge{
		cfg:           cfg,
		configPath:    configPath,
		tg:            tg,
		runner:        runner,
		log:           log,
		stateDir:      stateDir,
		offsets:       newOffsetStore(stateDir, log),
		activeSession: make(map[int64]string),
	}
}

func (b *Bridge) Run(ctx context.Context) error {
	if err := b.tg.SetMyCommands(ctx, commandMenu()); err != nil {
		b.log.Warn("set my commands failed", "err", err)
	}

	offset := b.offsets.load()

	for ctx.Err() == nil {
		updates, err := b.tg.GetUpdates(ctx, offset, getUpdatesTimeoutS)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			b.log.Error("get updates failed", "err", err)
			time.Sleep(5 * time.Second)
			continue
		}

		for _, u := range updates {
			offset = u.UpdateID + 1
			b.offsets.save(offset)
			b.handleUpdate(ctx, u)
		}
	}
	return nil
}

func (b *Bridge) handleUpdate(ctx context.Context, u telegram.Update) {
	msg := u.Message
	if msg == nil {
		return
	}
	if msg.From == nil {
		return
	}
	if b.cfg.NeedsBootstrap() {
		b.bootstrapAllowedUser(ctx, msg.From.ID, msg.Chat.ID)
	} else if !b.cfg.IsAllowed(msg.From.ID) {
		b.log.Warn("ignored message from unauthorized sender", "from", msg.From.ID)
		return
	}

	chatID := msg.Chat.ID
	switch {
	case msg.Document != nil:
		b.handleDocument(ctx, chatID, msg.Document)
	case strings.HasPrefix(msg.Text, "/cr_"):
		b.handleCommand(ctx, chatID, msg.Text)
	case strings.TrimSpace(msg.Text) != "":
		b.forwardToSession(ctx, chatID, msg.Text)
	}
}

func (b *Bridge) bootstrapAllowedUser(ctx context.Context, userID, chatID int64) {
	b.cfg.AllowedUsers = []int64{userID}
	if err := config.Save(b.configPath, b.cfg); err != nil {
		b.log.Error("bootstrap: save config failed", "err", err)
	}
	b.log.Warn("bootstrapped allowed_users to first sender", "user_id", userID)
	b.reply(ctx, chatID, fmt.Sprintf("привязан к пользователю %d — теперь только он может использовать бридж", userID))
}

type sessionRef struct {
	name    string
	dir     string
	command string
}

func (b *Bridge) resolveSession(chatID int64, name string) (sessionRef, error) {
	if name == "" {
		name = b.activeSessionName(chatID)
	}
	sc, ok := b.cfg.Sessions[name]
	if !ok {
		return sessionRef{}, fmt.Errorf("сессия %q не настроена", name)
	}
	dir, err := config.ExpandDir(sc.Dir)
	if err != nil {
		return sessionRef{}, fmt.Errorf("не удалось развернуть путь %q: %w", sc.Dir, err)
	}
	return sessionRef{name: name, dir: dir, command: sc.Command}, nil
}

func (b *Bridge) activeSessionName(chatID int64) string {
	if name, ok := b.activeSession[chatID]; ok {
		if _, exists := b.cfg.Sessions[name]; exists {
			return name
		}
	}
	return b.cfg.DefaultSession
}

func (b *Bridge) forwardToSession(ctx context.Context, chatID int64, text string) {
	s, err := b.resolveSession(chatID, "")
	if err != nil {
		b.reply(ctx, chatID, err.Error())
		return
	}
	name := s.name

	if !b.runner.Exists(name) {
		if err := b.runner.Start(name, s.dir, s.command); err != nil {
			b.reply(ctx, chatID, fmt.Sprintf("не удалось запустить сессию: %v", err))
			return
		}
		time.Sleep(b.cfg.Settle.ColdStartDelay())
	}

	before, err := b.runner.CapturePane(name, captureHistoryLines)
	if err != nil {
		b.reply(ctx, chatID, fmt.Sprintf("не удалось прочитать экран сессии: %v", err))
		return
	}

	if err := b.runner.SendKeys(name, text); err != nil {
		b.reply(ctx, chatID, fmt.Sprintf("не удалось отправить текст в сессию: %v", err))
		return
	}
	time.Sleep(b.cfg.Settle.PostSendDelay())

	onInterim := func(elapsed time.Duration) {
		b.reply(ctx, chatID, fmt.Sprintf("ещё работает (%dс)…", int(elapsed.Seconds())))
	}
	watchVisible := func() (string, error) { return b.runner.CapturePane(name, visiblePaneOnly) }
	if _, err := WaitForSettle(watchVisible, b.cfg.Settle, onInterim); err != nil {
		b.reply(ctx, chatID, fmt.Sprintf("ошибка чтения экрана: %v", err))
		return
	}

	after, err := b.runner.CapturePane(name, captureHistoryLines)
	if err != nil {
		b.reply(ctx, chatID, fmt.Sprintf("не удалось прочитать экран сессии: %v", err))
		return
	}

	diff := DiffTail(before, after)
	if diff == "" {
		diff = "(нет видимых изменений на экране — см. /cr_status)"
	}
	b.reply(ctx, chatID, diff)
}

func (b *Bridge) handleDocument(ctx context.Context, chatID int64, doc *telegram.Document) {
	s, err := b.resolveSession(chatID, "")
	if err != nil {
		b.reply(ctx, chatID, err.Error())
		return
	}

	file, err := b.tg.GetFile(ctx, doc.FileID)
	if err != nil {
		b.reply(ctx, chatID, fmt.Sprintf("не удалось получить файл: %v", err))
		return
	}

	safeName := sanitizeFileName(doc.FileName)
	relPath := filepath.Join("telegram-inbox", safeName)
	destPath := filepath.Join(s.dir, relPath)

	if err := b.tg.DownloadFile(ctx, file.FilePath, destPath); err != nil {
		b.reply(ctx, chatID, fmt.Sprintf("не удалось скачать файл: %v", err))
		return
	}

	b.forwardToSession(ctx, chatID, fmt.Sprintf("[telegram] загружен файл: %s", relPath))
}

func sanitizeFileName(name string) string {
	base := filepath.Base(name)
	if base == "" || base == "." || base == ".." || base == string(filepath.Separator) {
		return "upload_" + strconv.FormatInt(time.Now().Unix(), 10)
	}
	return base
}

func (b *Bridge) reply(ctx context.Context, chatID int64, text string) {
	sendCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), replyDeliveryTimeout)
	defer cancel()

	if len(text) > maxTotalInlineLen {
		b.replyAsDocument(sendCtx, chatID, text)
		return
	}
	for _, chunk := range SplitForTelegram(text, maxInlineReplyLen) {
		if err := b.tg.SendMessage(sendCtx, chatID, chunk); err != nil {
			b.log.Error("send message failed", "err", err)
			return
		}
	}
}

func (b *Bridge) replyAsDocument(ctx context.Context, chatID int64, text string) {
	path := filepath.Join(b.stateDir, fmt.Sprintf("reply-%d.txt", time.Now().UnixNano()))
	if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
		b.log.Error("write long reply to file failed", "err", err)
		return
	}
	defer func() { _ = os.Remove(path) }()

	if err := b.tg.SendDocument(ctx, chatID, path); err != nil {
		b.log.Error("send long reply as document failed", "err", err)
	}
}
