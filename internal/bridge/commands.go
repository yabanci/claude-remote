package bridge

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/yabanci/claude-remote/internal/config"
	"github.com/yabanci/claude-remote/internal/telegram"
)

var validSessionName = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

func commandMenu() []telegram.BotCommand {
	return []telegram.BotCommand{
		{Command: "cr_status", Description: "статус текущей и всех сессий"},
		{Command: "cr_sessions", Description: "список сессий"},
		{Command: "cr_use", Description: "переключиться на сессию: /cr_use <имя>"},
		{Command: "cr_new", Description: "создать сессию: /cr_new <имя> <путь>, имя — только буквы/цифры/_/-"},
		{Command: "cr_kill", Description: "остановить сессию: /cr_kill [имя]"},
		{Command: "cr_restart", Description: "перезапустить сессию: /cr_restart [имя]"},
		{Command: "cr_interrupt", Description: "Ctrl-C в текущей сессии"},
		{Command: "cr_peek", Description: "показать экран сессии, ничего в неё не отправляя"},
		{Command: "cr_send", Description: "прислать файл из рабочей директории сессии, за её пределы — запрет: /cr_send <путь>"},
		{Command: "cr_help", Description: "список команд"},
	}
}

func stripBotSuffix(cmd string) string {
	base, _, _ := strings.Cut(cmd, "@")
	return base
}

func (b *Bridge) handleCommand(ctx context.Context, chatID int64, text string) {
	fields := strings.SplitN(strings.TrimSpace(text), " ", 2)
	cmd := stripBotSuffix(fields[0])
	arg := ""
	if len(fields) > 1 {
		arg = strings.TrimSpace(fields[1])
	}

	switch cmd {
	case "/cr_status":
		b.cmdStatus(ctx, chatID)
	case "/cr_sessions":
		b.cmdSessions(ctx, chatID)
	case "/cr_use":
		b.cmdUse(ctx, chatID, arg)
	case "/cr_new":
		b.cmdNew(ctx, chatID, arg)
	case "/cr_kill":
		b.cmdKill(ctx, chatID, arg)
	case "/cr_restart":
		b.cmdRestart(ctx, chatID, arg)
	case "/cr_interrupt":
		b.cmdInterrupt(ctx, chatID)
	case "/cr_peek":
		b.cmdPeek(ctx, chatID)
	case "/cr_send":
		b.cmdSend(ctx, chatID, arg)
	case "/cr_help":
		b.cmdHelp(ctx, chatID)
	default:
		b.reply(ctx, chatID, fmt.Sprintf("неизвестная команда %s, см. /cr_help", cmd))
	}
}

func (b *Bridge) cmdStatus(ctx context.Context, chatID int64) {
	current := b.activeSessionName(chatID)
	var sb strings.Builder
	fmt.Fprintf(&sb, "текущая сессия: %s\n\n", current)
	for _, name := range sortedSessionNames(b.sessionsSnapshot()) {
		state := "остановлена"
		if b.runner.Exists(name) {
			state = "работает"
		}
		marker := "  "
		if name == current {
			marker = "->"
		}
		fmt.Fprintf(&sb, "%s %s: %s\n", marker, name, state)
	}
	b.reply(ctx, chatID, sb.String())
}

func (b *Bridge) cmdSessions(ctx context.Context, chatID int64) {
	current := b.activeSessionName(chatID)
	var sb strings.Builder
	sessions := b.sessionsSnapshot()
	for _, name := range sortedSessionNames(sessions) {
		sc := sessions[name]
		marker := ""
		if name == current {
			marker = " [active]"
		}
		fmt.Fprintf(&sb, "%s%s — %s\n", name, marker, sc.Dir)
	}
	b.reply(ctx, chatID, sb.String())
}

func (b *Bridge) cmdUse(ctx context.Context, chatID int64, name string) {
	if name == "" {
		b.reply(ctx, chatID, "укажи имя: /cr_use <имя>")
		return
	}
	if !b.hasSession(name) {
		b.reply(ctx, chatID, fmt.Sprintf("сессия %q не найдена, см. /cr_sessions", name))
		return
	}
	b.setActiveSession(chatID, name)
	b.reply(ctx, chatID, fmt.Sprintf("активная сессия: %s", name))
}

func (b *Bridge) cmdNew(ctx context.Context, chatID int64, arg string) {
	parts := strings.SplitN(arg, " ", 2)
	if len(parts) < 2 || parts[0] == "" || parts[1] == "" {
		b.reply(ctx, chatID, "формат: /cr_new <имя> <путь>")
		return
	}
	name, rawDir := parts[0], strings.TrimSpace(parts[1])

	if !validSessionName.MatchString(name) {
		b.reply(ctx, chatID, fmt.Sprintf("недопустимое имя сессии %q: разрешены только буквы, цифры, `_` и `-`", name))
		return
	}

	dir, err := config.ExpandDir(rawDir)
	if err != nil {
		b.reply(ctx, chatID, fmt.Sprintf("не удалось развернуть путь: %v", err))
		return
	}
	if info, err := os.Stat(dir); err != nil || !info.IsDir() {
		b.reply(ctx, chatID, fmt.Sprintf("директория %q не найдена", dir))
		return
	}

	if err := b.addSession(chatID, name, rawDir); err != nil {
		b.reply(ctx, chatID, err.Error())
		return
	}

	if err := b.runner.Start(name, dir, defaultSessionCommand); err != nil {
		b.rollbackNewSession(chatID, name)
		b.reply(ctx, chatID, fmt.Sprintf("сессия не запустилась, откатываю конфиг: %v", err))
		return
	}
	b.reply(ctx, chatID, fmt.Sprintf("создана и запущена: %s (%s)", name, dir))
}

func (b *Bridge) cmdKill(ctx context.Context, chatID int64, name string) {
	s, err := b.resolveSession(chatID, name)
	if err != nil {
		b.reply(ctx, chatID, err.Error())
		return
	}
	name = s.name
	if !b.runner.Exists(name) {
		b.reply(ctx, chatID, fmt.Sprintf("сессия %q не запущена", name))
		return
	}
	if err := b.runner.Kill(name); err != nil {
		b.reply(ctx, chatID, fmt.Sprintf("не удалось остановить: %v", err))
		return
	}
	b.reply(ctx, chatID, fmt.Sprintf("остановлена: %s", name))
}

func (b *Bridge) cmdRestart(ctx context.Context, chatID int64, name string) {
	s, err := b.resolveSession(chatID, name)
	if err != nil {
		b.reply(ctx, chatID, err.Error())
		return
	}
	if b.runner.Exists(s.name) {
		if err := b.runner.Kill(s.name); err != nil {
			b.reply(ctx, chatID, fmt.Sprintf("не удалось остановить: %v", err))
			return
		}
	}
	if err := b.runner.Start(s.name, s.dir, s.command); err != nil {
		b.reply(ctx, chatID, fmt.Sprintf("не удалось запустить: %v", err))
		return
	}
	b.reply(ctx, chatID, fmt.Sprintf("перезапущена: %s", s.name))
}

func (b *Bridge) cmdInterrupt(ctx context.Context, chatID int64) {
	name := b.activeSessionName(chatID)
	if !b.runner.Exists(name) {
		b.reply(ctx, chatID, fmt.Sprintf("сессия %q не запущена", name))
		return
	}
	if err := b.runner.Interrupt(name); err != nil {
		b.reply(ctx, chatID, fmt.Sprintf("не удалось прервать: %v", err))
		return
	}
	b.reply(ctx, chatID, "Ctrl-C отправлен")
}

func (b *Bridge) cmdPeek(ctx context.Context, chatID int64) {
	name := b.activeSessionName(chatID)
	if !b.runner.Exists(name) {
		b.reply(ctx, chatID, fmt.Sprintf("сессия %q не запущена", name))
		return
	}

	pane, err := b.runner.CapturePane(name, captureHistoryLines)
	if err != nil {
		b.reportCaptureFailure(ctx, chatID, name, err)
		return
	}

	shown, _ := FormatReply(pane)
	if shown == "" {
		shown = "(на экране пусто)"
	}
	b.reply(ctx, chatID, shown)
}

func (b *Bridge) cmdSend(ctx context.Context, chatID int64, arg string) {
	if arg == "" {
		b.reply(ctx, chatID, "формат: /cr_send <путь>")
		return
	}
	s, err := b.resolveSession(chatID, "")
	if err != nil {
		b.reply(ctx, chatID, err.Error())
		return
	}

	path, err := resolveSendPath(s.dir, arg)
	if err != nil {
		b.reply(ctx, chatID, err.Error())
		return
	}
	if _, err := os.Stat(path); err != nil {
		b.reply(ctx, chatID, fmt.Sprintf("файл не найден: %s", path))
		return
	}
	if err := b.tg.SendDocument(ctx, chatID, path); err != nil {
		b.reply(ctx, chatID, fmt.Sprintf("не удалось отправить файл: %v", err))
	}
}

func resolveSendPath(sessionDir, arg string) (string, error) {
	base := filepath.Clean(sessionDir)
	target := filepath.Clean(arg)
	if !filepath.IsAbs(target) {
		target = filepath.Join(base, target)
	}
	rel, err := filepath.Rel(base, target)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("путь %q выходит за пределы рабочей директории сессии", arg)
	}
	return target, nil
}

func (b *Bridge) cmdHelp(ctx context.Context, chatID int64) {
	var sb strings.Builder
	sb.WriteString("Всё, что не команда бриджа, идёт напрямую в claude как ввод.\n\n")
	for _, c := range commandMenu() {
		fmt.Fprintf(&sb, "/%s — %s\n", c.Command, c.Description)
	}
	b.reply(ctx, chatID, sb.String())
}

func sortedSessionNames(sessions map[string]config.SessionConfig) []string {
	names := make([]string, 0, len(sessions))
	for name := range sessions {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
