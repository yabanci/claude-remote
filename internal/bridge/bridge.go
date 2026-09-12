package bridge

import (
	"context"
	"errors"
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
	configPath string
	tg         *telegram.Client
	runner     Runner
	log        *slog.Logger
	stateDir   string
	offsets    *offsetStore

	state         sync.Mutex
	cfg           config.Config
	activeSession map[int64]string

	turns       *sessionLocks
	generations *sessionGenerations
	inflight    sync.WaitGroup
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
		turns:         newSessionLocks(),
		generations:   newSessionGenerations(),
	}
}

func (b *Bridge) Run(ctx context.Context) error {
	defer b.inflight.Wait()

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
			b.dispatch(ctx, u)
		}
	}
	return nil
}

func (b *Bridge) dispatch(ctx context.Context, u telegram.Update) {
	b.inflight.Add(1)
	go func() {
		defer b.inflight.Done()
		defer b.recoverFromPanic(ctx, u)
		if u.CallbackQuery != nil {
			b.handleCallback(ctx, u.CallbackQuery)
			return
		}
		b.handleUpdate(ctx, u)
	}()
}

func (b *Bridge) recoverFromPanic(ctx context.Context, u telegram.Update) {
	r := recover()
	if r == nil {
		return
	}
	b.log.Error("recovered from a panic while handling an update", "update_id", u.UpdateID, "panic", r)
	if chatID, ok := chatIDOf(u); ok {
		b.reply(ctx, chatID, "что-то сломалось при обработке этого сообщения, но бридж жив — попробуй ещё раз")
	}
}

func chatIDOf(u telegram.Update) (int64, bool) {
	if u.Message != nil {
		return u.Message.Chat.ID, true
	}
	if u.CallbackQuery != nil && u.CallbackQuery.Message != nil {
		return u.CallbackQuery.Message.Chat.ID, true
	}
	return 0, false
}

func (b *Bridge) handleUpdate(ctx context.Context, u telegram.Update) {
	msg := u.Message
	if msg == nil {
		return
	}
	if msg.From == nil {
		return
	}
	if b.needsBootstrap() {
		b.bootstrapOwner(ctx, msg.From.ID, msg.Chat.ID)
		return
	}
	if !b.allowedSender(msg.From.ID, msg.Chat.ID) {
		b.log.Warn("ignored message from an unauthorized sender or chat",
			"from", msg.From.ID, "chat", msg.Chat.ID)
		return
	}

	chatID := msg.Chat.ID
	ctx = withReplyTo(ctx, msg.MessageID)

	if runsWhileSessionIsBusy(msg.Text) {
		b.handleCommand(ctx, chatID, msg.Text)
		return
	}
	target := b.targetSessionFor(chatID, msg.Text)
	b.inSessionTurn(target, func() { b.handleMessage(ctx, chatID, msg) })
}

func (b *Bridge) handleMessage(ctx context.Context, chatID int64, msg *telegram.Message) {
	switch {
	case msg.Document != nil:
		b.handleUpload(ctx, chatID, msg.Document.FileID, msg.Document.FileName, msg.Caption)
	case msg.LargestPhoto() != nil:
		photo := msg.LargestPhoto()
		b.handleUpload(ctx, chatID, photo.FileID, photoFileName(photo), msg.Caption)
	case strings.HasPrefix(msg.Text, "/cr_"):
		b.handleCommand(ctx, chatID, msg.Text)
	case strings.TrimSpace(msg.Text) != "":
		b.forwardToSession(ctx, chatID, msg.Text)
	default:
		b.log.Warn("message carried nothing the bridge understands", "chat", chatID)
		b.reply(ctx, chatID, "не понял это сообщение: умею текст, файлы и фото")
	}
}

func (b *Bridge) handleCallback(ctx context.Context, q *telegram.CallbackQuery) {
	if q.From == nil || q.Message == nil {
		return
	}
	chatID := q.Message.Chat.ID
	if !b.allowedSender(q.From.ID, chatID) {
		b.log.Warn("ignored callback from unauthorized sender or chat",
			"from", q.From.ID, "chat", chatID)
		return
	}
	if err := b.tg.AnswerCallback(ctx, q.ID, "отправил "+q.Data); err != nil {
		b.log.Warn("answer callback failed", "err", err)
	}
	ctx = withReplyTo(ctx, q.Message.MessageID)
	b.inSessionTurn(b.activeSessionName(chatID), func() { b.forwardToSession(ctx, chatID, q.Data) })
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

func (b *Bridge) bootstrapOwner(ctx context.Context, userID, chatID int64) bool {
	switch err := b.bindOwner(userID, chatID); {
	case err == nil:
		b.log.Warn("bound to the first sender", "user_id", userID, "chat_id", chatID)
		b.reply(ctx, chatID, fmt.Sprintf(
			"привязан к пользователю %d в этом чате — только отсюда и только он.\n\nПовтори сообщение, оно будет первым выполненным.", userID))
		return true
	case errors.Is(err, errAlreadyBound):
		b.log.Warn("bootstrap lost the race to another sender, ignoring", "user_id", userID, "chat_id", chatID)
		return false
	default:
		b.log.Error("bootstrap: save config failed, refusing to bind", "err", err)
		b.reply(ctx, chatID, "не смог закрепить владельца в конфиге, поэтому ничего не выполняю. Проверь права на файл конфигурации и напиши снова.")
		return false
	}
}

type sessionRef struct {
	name       string
	dir        string
	command    string
	generation uint64
}

func (b *Bridge) resolveSession(chatID int64, name string) (sessionRef, error) {
	if name == "" {
		name = b.activeSessionName(chatID)
	}
	sc, ok := b.sessionConfig(name)
	if !ok {
		return sessionRef{}, fmt.Errorf("сессия %q не настроена", name)
	}
	dir, err := config.ExpandDir(sc.Dir)
	if err != nil {
		return sessionRef{}, fmt.Errorf("не удалось развернуть путь %q: %w", sc.Dir, err)
	}
	return sessionRef{name: name, dir: dir, command: sc.Command}, nil
}

func (b *Bridge) forwardToSession(ctx context.Context, chatID int64, text string) {
	s, err := b.resolveSession(chatID, "")
	if err != nil {
		b.reply(ctx, chatID, err.Error())
		return
	}

	s, ok := b.ensureRunning(ctx, chatID, s)
	if !ok {
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

	if !b.sendAndAwait(ctx, chatID, s, text) {
		return
	}
	b.deliverAnswer(ctx, chatID, s.name, before, text)
}

func (b *Bridge) ensureRunning(ctx context.Context, chatID int64, s sessionRef) (sessionRef, bool) {
	if b.runner.Exists(s.name) {
		s.generation = b.generations.current(s.name)
		return s, true
	}
	if err := b.runner.Start(s.name, s.dir, s.command); err != nil {
		b.reply(ctx, chatID, fmt.Sprintf("не удалось запустить сессию: %v", err))
		return sessionRef{}, false
	}
	s.generation = b.generations.bump(s.name)

	settle := b.settle()
	time.Sleep(settle.ColdStartDelay())
	if _, err := WaitForSettle(ctx, b.watchVisible(s.name, s.generation), settle, b.noticeSessionStillStarting(ctx, chatID, s.name)); err != nil {
		b.reply(ctx, chatID, fmt.Sprintf("не удалось дождаться запуска сессии: %v", err))
		return sessionRef{}, false
	}
	return s, true
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
			s.name, s.dir, TmuxSessionName(s.name)))
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

func (b *Bridge) sendAndAwait(ctx context.Context, chatID int64, s sessionRef, text string) bool {
	if err := b.runner.SendKeys(s.name, text); err != nil {
		b.reply(ctx, chatID, fmt.Sprintf("не удалось отправить текст в сессию: %v", err))
		return false
	}

	stopTyping := b.showTyping(ctx, chatID)
	defer stopTyping()

	settle := b.settle()
	time.Sleep(settle.PostSendDelay())
	if _, err := WaitForSettle(ctx, b.watchVisible(s.name, s.generation), settle, b.noticeAnswerStillComing(ctx, chatID)); err != nil {
		b.reportCaptureFailure(ctx, chatID, s.name, err)
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
	if errors.Is(err, errSessionReplaced) {
		b.reply(ctx, chatID, fmt.Sprintf(
			"сессия %q перезапустилась, пока готовился ответ на предыдущий вопрос — тот ответ потерян.\n\nПовтори вопрос.", name))
		return
	}
	if !b.runner.Exists(name) {
		b.reply(ctx, chatID, fmt.Sprintf(
			"сессия %q пропала, пока готовился ответ — он потерян.\n\nПодними её: /cr_restart", name))
		return
	}
	b.reply(ctx, chatID, fmt.Sprintf("не удалось прочитать экран сессии: %v", err))
}

var errSessionReplaced = errors.New("session was killed and restarted under the same name mid-turn")

func (b *Bridge) watchVisible(name string, generation uint64) CaptureFunc {
	return func() (string, error) {
		if b.generations.current(name) != generation {
			return "", errSessionReplaced
		}
		return b.runner.CapturePane(name, visiblePaneOnly)
	}
}

func (b *Bridge) handleUpload(ctx context.Context, chatID int64, fileID, fileName, caption string) {
	s, err := b.resolveSession(chatID, "")
	if err != nil {
		b.reply(ctx, chatID, err.Error())
		return
	}

	file, err := b.tg.GetFile(ctx, fileID)
	if err != nil {
		b.reply(ctx, chatID, fmt.Sprintf("не удалось получить файл: %v", err))
		return
	}

	relPath := filepath.Join("telegram-inbox", sanitizeFileName(fileName))
	if err := b.tg.DownloadFile(ctx, file.FilePath, filepath.Join(s.dir, relPath)); err != nil {
		b.reply(ctx, chatID, fmt.Sprintf("не удалось скачать файл: %v", err))
		return
	}

	b.forwardToSession(ctx, chatID, uploadPrompt(relPath, caption))
}

func uploadPrompt(relPath, caption string) string {
	if strings.TrimSpace(caption) == "" {
		return fmt.Sprintf("[telegram] загружен файл: %s", relPath)
	}
	return fmt.Sprintf("[telegram] загружен файл: %s\n\n%s", relPath, caption)
}

func photoFileName(photo *telegram.PhotoSize) string {
	return fmt.Sprintf("photo-%s.jpg", photo.FileID[:min(12, len(photo.FileID))])
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
			opts.ReplyTo = replyToOf(ctx)
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

	opts := telegram.SendOptions{
		ReplyTo:  replyToOf(ctx),
		Keyboard: &telegram.InlineKeyboard{Rows: menu.keyboardRows()},
	}
	err := b.tg.Send(sendCtx, chatID, menu.heading(), opts)
	if err == nil {
		return
	}

	b.log.Warn("sending the menu as buttons failed, falling back to a numbered text menu",
		"err", err, "options", len(menu.Options))
	b.reply(ctx, chatID, menu.asPlainText())
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
