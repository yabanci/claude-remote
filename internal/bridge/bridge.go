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
	maxInlineReplyLen   = 3500
	maxTotalInlineLen   = 12000
	getUpdatesTimeoutS  = 25
)

type Bridge struct {
	cfg        config.Config
	configPath string
	tg         *telegram.Client
	runner     Runner
	log        *slog.Logger
	stateDir   string

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
		activeSession: make(map[int64]string),
	}
}

func (b *Bridge) Run(ctx context.Context) error {
	if err := b.tg.SetMyCommands(ctx, commandMenu()); err != nil {
		b.log.Warn("set my commands failed", "err", err)
	}

	offset := b.loadOffset()

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
			b.saveOffset(offset)
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

func (b *Bridge) activeSessionName(chatID int64) string {
	if name, ok := b.activeSession[chatID]; ok {
		if _, exists := b.cfg.Sessions[name]; exists {
			return name
		}
	}
	return b.cfg.DefaultSession
}

func (b *Bridge) forwardToSession(ctx context.Context, chatID int64, text string) {
	name := b.activeSessionName(chatID)
	sc, ok := b.cfg.Sessions[name]
	if !ok {
		b.reply(ctx, chatID, fmt.Sprintf("сессия %q не настроена", name))
		return
	}

	dir, err := config.ExpandDir(sc.Dir)
	if err != nil {
		b.reply(ctx, chatID, fmt.Sprintf("не удалось развернуть путь %q: %v", sc.Dir, err))
		return
	}

	if !b.runner.Exists(name) {
		if err := b.runner.Start(name, dir, sc.Command); err != nil {
			b.reply(ctx, chatID, fmt.Sprintf("не удалось запустить сессию: %v", err))
			return
		}
		time.Sleep(b.cfg.Settle.ColdStartDelay())
	}

	capture := func() (string, error) { return b.runner.CapturePane(name, captureHistoryLines) }

	before, err := capture()
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
	after, err := WaitForSettle(capture, b.cfg.Settle, onInterim)
	if err != nil {
		b.reply(ctx, chatID, fmt.Sprintf("ошибка чтения экрана: %v", err))
		return
	}

	diff := DiffTail(before, after)
	if diff == "" {
		diff = "(нет видимых изменений на экране — см. /cr_status)"
	}
	b.reply(ctx, chatID, diff)
}

func (b *Bridge) handleDocument(ctx context.Context, chatID int64, doc *telegram.Document) {
	name := b.activeSessionName(chatID)
	sc, ok := b.cfg.Sessions[name]
	if !ok {
		b.reply(ctx, chatID, fmt.Sprintf("сессия %q не настроена", name))
		return
	}
	dir, err := config.ExpandDir(sc.Dir)
	if err != nil {
		b.reply(ctx, chatID, fmt.Sprintf("не удалось развернуть путь %q: %v", sc.Dir, err))
		return
	}

	file, err := b.tg.GetFile(ctx, doc.FileID)
	if err != nil {
		b.reply(ctx, chatID, fmt.Sprintf("не удалось получить файл: %v", err))
		return
	}

	safeName := sanitizeFileName(doc.FileName)
	relPath := filepath.Join("telegram-inbox", safeName)
	destPath := filepath.Join(dir, relPath)

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
	if len(text) > maxTotalInlineLen {
		b.replyAsDocument(ctx, chatID, text)
		return
	}
	for i := 0; i < len(text); i += maxInlineReplyLen {
		end := i + maxInlineReplyLen
		if end > len(text) {
			end = len(text)
		}
		if err := b.tg.SendMessage(ctx, chatID, text[i:end]); err != nil {
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

func (b *Bridge) offsetPath() string {
	return filepath.Join(b.stateDir, "offset.txt")
}

func (b *Bridge) loadOffset() int64 {
	data, err := os.ReadFile(b.offsetPath())
	if err != nil {
		return 0
	}
	offset, err := strconv.ParseInt(strings.TrimSpace(string(data)), 10, 64)
	if err != nil {
		return 0
	}
	return offset
}

func (b *Bridge) saveOffset(offset int64) {
	if err := os.MkdirAll(b.stateDir, 0o700); err != nil {
		b.log.Error("create state dir failed", "err", err)
		return
	}
	if err := os.WriteFile(b.offsetPath(), []byte(strconv.FormatInt(offset, 10)), 0o600); err != nil {
		b.log.Error("save offset failed", "err", err)
	}
}
