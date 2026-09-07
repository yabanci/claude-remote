package bridge

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/yabanci/claude-remote/internal/config"
	"github.com/yabanci/claude-remote/internal/telegram"
	"github.com/yabanci/claude-remote/internal/tmux"
)

const (
	captureHistoryLines = tmux.HistoryLimit
	visiblePaneOnly     = 0
	maxInlineReplyLen   = 3500
	maxTotalInlineLen   = 12000
	getUpdatesTimeoutS  = 25

	replyDeliveryTimeout = 2 * time.Minute
	dialogTailLines      = 20
	typingRefresh        = 4 * time.Second
	chatActionTyping     = "typing"
)

type Bridge struct {
	cfg        config.Config
	configPath string
	tg         *telegram.Client
	runner     Runner
	log        *slog.Logger
	stateDir   string
	offsets    *offsetStore
	replyTo    int64

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
			if u.CallbackQuery != nil {
				b.handleCallback(ctx, u.CallbackQuery)
				continue
			}
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
	b.replyTo = msg.MessageID
	switch {
	case msg.Document != nil:
		b.handleDocument(ctx, chatID, msg.Document)
	case strings.HasPrefix(msg.Text, "/cr_"):
		b.handleCommand(ctx, chatID, msg.Text)
	case strings.TrimSpace(msg.Text) != "":
		b.forwardToSession(ctx, chatID, msg.Text)
	}
	b.replyTo = 0
}

func (b *Bridge) handleCallback(ctx context.Context, q *telegram.CallbackQuery) {
	if q.From == nil || q.Message == nil {
		return
	}
	if !b.cfg.IsAllowed(q.From.ID) {
		b.log.Warn("ignored callback from unauthorized sender", "from", q.From.ID)
		return
	}
	if err := b.tg.AnswerCallback(ctx, q.ID, "отправил "+q.Data); err != nil {
		b.log.Warn("answer callback failed", "err", err)
	}
	b.replyTo = q.Message.MessageID
	b.forwardToSession(ctx, q.Message.Chat.ID, q.Data)
	b.replyTo = 0
}

func (b *Bridge) showTyping(ctx context.Context, chatID int64) (stop func()) {
	done := make(chan struct{})
	var once sync.Once
	go func() {
		ticker := time.NewTicker(typingRefresh)
		defer ticker.Stop()
		for {
			if err := b.tg.SendChatAction(ctx, chatID, chatActionTyping); err != nil {
				b.log.Debug("chat action failed", "err", err)
			}
			select {
			case <-done:
				return
			case <-ticker.C:
			}
		}
	}()
	return func() { once.Do(func() { close(done) }) }
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

	if !b.ensureRunning(ctx, chatID, s) {
		return
	}
	if b.heldBackByOpenDialog(ctx, chatID, s, text) {
		return
	}

	before, err := b.runner.CapturePane(s.name, captureHistoryLines)
	if err != nil {
		b.reportCaptureFailure(ctx, chatID, s.name, err)
		return
	}

	if !b.sendAndAwait(ctx, chatID, s.name, text) {
		return
	}
	b.deliverAnswer(ctx, chatID, s.name, before, text)
}

func (b *Bridge) ensureRunning(ctx context.Context, chatID int64, s sessionRef) bool {
	if b.runner.Exists(s.name) {
		return true
	}
	if err := b.runner.Start(s.name, s.dir, s.command); err != nil {
		b.reply(ctx, chatID, fmt.Sprintf("не удалось запустить сессию: %v", err))
		return false
	}

	time.Sleep(b.cfg.Settle.ColdStartDelay())
	if _, err := WaitForSettle(b.watchVisible(s.name), b.cfg.Settle, nil); err != nil {
		b.reply(ctx, chatID, fmt.Sprintf("не удалось дождаться запуска сессии: %v", err))
		return false
	}
	return true
}

func (b *Bridge) heldBackByOpenDialog(ctx context.Context, chatID int64, s sessionRef, text string) bool {
	visible, err := b.runner.CapturePane(s.name, visiblePaneOnly)
	if err != nil {
		b.reportCaptureFailure(ctx, chatID, s.name, err)
		return true
	}

	if AwaitsTrustConfirmation(visible) {
		b.reply(ctx, chatID, fmt.Sprintf(
			"сессия %q ждёт подтверждения доверия к каталогу %s.\n\n"+
				"Это вопрос безопасности — ни я, ни бот на него за тебя не отвечаем. Подтверди в терминале:\n"+
				"  tmux attach -t %s\n\n"+
				"или один раз запусти claude в этом каталоге. После этого повтори сообщение.",
			s.name, s.dir, s.name))
		return true
	}

	if AwaitsInteractiveChoice(visible) && !IsDeliberateChoice(text) {
		b.reply(ctx, chatID, fmt.Sprintf(
			"на экране сессии %q открыт диалог, твоё сообщение туда не отправлено — иначе текст ушёл бы в меню.\n\n%s\n\n"+
				"Ответь коротко (номер варианта, yes/no) — это я передам как есть. Потом повтори вопрос.",
			s.name, visibleTail(visible, dialogTailLines)))
		return true
	}
	return false
}

func (b *Bridge) sendAndAwait(ctx context.Context, chatID int64, name, text string) bool {
	if err := b.runner.SendKeys(name, text); err != nil {
		b.reply(ctx, chatID, fmt.Sprintf("не удалось отправить текст в сессию: %v", err))
		return false
	}

	stopTyping := b.showTyping(ctx, chatID)
	defer stopTyping()

	time.Sleep(b.cfg.Settle.PostSendDelay())
	if _, err := WaitForSettle(b.watchVisible(name), b.cfg.Settle, nil); err != nil {
		b.reportCaptureFailure(ctx, chatID, name, err)
		return false
	}
	return true
}

func (b *Bridge) deliverAnswer(ctx context.Context, chatID int64, name, before, text string) {
	after, err := b.runner.CapturePane(name, captureHistoryLines)
	if err != nil {
		b.reportCaptureFailure(ctx, chatID, name, err)
		return
	}

	visible, err := b.runner.CapturePane(name, visiblePaneOnly)
	if err != nil {
		b.reportCaptureFailure(ctx, chatID, name, err)
		return
	}
	if menu, isMenu := ParseMenu(visible); isMenu {
		b.replyWithMenu(ctx, chatID, menu)
		return
	}

	produced, ok := TailAfterPrompt(after, text)
	if !ok {
		produced = DiffTail(before, after)
	}

	reply, rawFallback := FormatReply(produced)
	if rawFallback {
		b.log.Warn("reply carried no known TUI marker, sent raw pane text instead",
			"session", name, "expected_markers", ExpectedMarkers())
	}
	if reply == "" {
		reply = "(сессия ничего не вывела — см. /cr_status)"
	}
	b.reply(ctx, chatID, reply)
}

func (b *Bridge) reportCaptureFailure(ctx context.Context, chatID int64, name string, err error) {
	if !b.runner.Exists(name) {
		b.reply(ctx, chatID, fmt.Sprintf(
			"сессия %q пропала, пока готовился ответ — он потерян.\n\nПодними её: /cr_restart", name))
		return
	}
	b.reply(ctx, chatID, fmt.Sprintf("не удалось прочитать экран сессии: %v", err))
}

func (b *Bridge) watchVisible(name string) CaptureFunc {
	return func() (string, error) { return b.runner.CapturePane(name, visiblePaneOnly) }
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

func visibleTail(pane string, lines int) string {
	all := strings.Split(strings.TrimRight(pane, "\n"), "\n")
	if len(all) > lines {
		all = all[len(all)-lines:]
	}
	return strings.TrimSpace(strings.Join(all, "\n"))
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
	for i, chunk := range SplitForTelegram(text, maxInlineReplyLen) {
		opts := telegram.SendOptions{}
		if i == 0 {
			opts.ReplyTo = b.replyTo
		}
		if err := b.tg.Send(sendCtx, chatID, chunk, opts); err != nil {
			b.log.Error("send message failed", "err", err)
			return
		}
	}
}

func (b *Bridge) replyWithMenu(ctx context.Context, chatID int64, menu Menu) {
	sendCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), replyDeliveryTimeout)
	defer cancel()

	var row []telegram.InlineButton
	for _, option := range menu.Options {
		row = append(row, telegram.InlineButton{
			Text:         option.Key + ". " + option.Label,
			CallbackData: option.Key,
		})
	}

	text := menu.Question
	if text == "" {
		text = "сессия ждёт выбора"
	}
	opts := telegram.SendOptions{
		ReplyTo:  b.replyTo,
		Keyboard: &telegram.InlineKeyboard{Rows: [][]telegram.InlineButton{row}},
	}
	if err := b.tg.Send(sendCtx, chatID, text, opts); err != nil {
		b.log.Error("send menu failed", "err", err)
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
