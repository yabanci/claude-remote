package main

import (
	"bufio"
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"github.com/yabanci/claude-remote/internal/bridge"
	"github.com/yabanci/claude-remote/internal/config"
	"github.com/yabanci/claude-remote/internal/service"
	"github.com/yabanci/claude-remote/internal/telegram"
)

var version = "dev"

func main() {
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}

	var err error
	switch os.Args[1] {
	case "init":
		err = cmdInit(os.Args[2:])
	case "run":
		err = cmdRun(os.Args[2:])
	case "service":
		err = cmdService(os.Args[2:])
	case "version":
		fmt.Println(version)
	case "help", "-h", "--help":
		printUsage()
	default:
		printUsage()
		os.Exit(1)
	}

	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func printUsage() {
	fmt.Print(`claude-remote — control a running claude session over Telegram

Usage:
  claude-remote init              interactive setup wizard
  claude-remote run               start the bridge in the foreground
  claude-remote service install   install as a background service (launchd/systemd)
  claude-remote service uninstall remove the background service
  claude-remote service status    show background service status
  claude-remote version           print version
`)
}

func resolveConfigPath(args []string) (string, error) {
	for i, a := range args {
		if a == "-config" && i+1 < len(args) {
			return args[i+1], nil
		}
	}
	return config.DefaultPath()
}

func cmdRun(args []string) error {
	configPath, err := resolveConfigPath(args)
	if err != nil {
		return err
	}

	cfg, err := config.Load(configPath)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	if err := cfg.Validate(); err != nil {
		return fmt.Errorf("invalid config %s: %w", configPath, err)
	}

	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	if cfg.NeedsBootstrap() {
		logger.Warn("allowed_users is empty — bridge will bind to whoever messages the bot first; keep the bot token secret")
	}

	stateDir := filepath.Join(filepath.Dir(configPath), "state")
	tg := telegram.NewClient(cfg.ResolveToken())
	runner := bridge.NewTmuxRunner()
	br := bridge.New(cfg, configPath, tg, runner, logger, stateDir)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	logger.Info("claude-remote starting", "version", version, "config", configPath)
	if err := br.Run(ctx); err != nil {
		return fmt.Errorf("bridge stopped with error: %w", err)
	}
	logger.Info("claude-remote stopped")
	return nil
}

func cmdService(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: claude-remote service <install|uninstall|status>")
	}

	execPath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("resolve executable path: %w", err)
	}
	mgr := service.NewManager(execPath)

	switch args[0] {
	case "install":
		if err := mgr.Install(); err != nil {
			return fmt.Errorf("install service: %w", err)
		}
		fmt.Println("service installed and started")
	case "uninstall":
		if err := mgr.Uninstall(); err != nil {
			return fmt.Errorf("uninstall service: %w", err)
		}
		fmt.Println("service uninstalled")
	case "status":
		status, err := mgr.Status()
		if err != nil {
			return fmt.Errorf("service status: %w", err)
		}
		fmt.Println(strings.TrimSpace(status))
	default:
		return fmt.Errorf("unknown service subcommand %q", args[0])
	}
	return nil
}

func cmdInit(args []string) error {
	configPath, err := resolveConfigPath(args)
	if err != nil {
		return err
	}

	reader := bufio.NewScanner(os.Stdin)
	prompt := func(label string) string {
		fmt.Print(label)
		reader.Scan()
		return strings.TrimSpace(reader.Text())
	}

	fmt.Println("Создай отдельного бота через @BotFather в Telegram (/newbot) и вставь его токен ниже.")
	token := prompt("Bot token: ")
	if token == "" {
		return fmt.Errorf("bot token is required")
	}

	fmt.Println("Твой числовой Telegram user id (узнать можно у @userinfobot).")
	fmt.Println("Оставь пустым — бридж привяжется к первому, кто ему напишет.")
	userIDInput := prompt("User id (optional): ")

	fmt.Println("Рабочая директория для сессии по умолчанию (Enter — домашняя папка).")
	dirInput := prompt("Project dir (optional): ")

	cfg := config.Default()
	cfg.BotToken = token
	if userIDInput != "" {
		id, err := strconv.ParseInt(userIDInput, 10, 64)
		if err != nil {
			return fmt.Errorf("invalid user id %q: %w", userIDInput, err)
		}
		cfg.AllowedUsers = []int64{id}
	}
	if dirInput != "" {
		cfg.Sessions[cfg.DefaultSession] = config.SessionConfig{Dir: dirInput, Command: "claude"}
	}

	if err := config.Save(configPath, cfg); err != nil {
		return fmt.Errorf("save config: %w", err)
	}

	fmt.Printf("\nконфиг сохранён: %s\n", configPath)
	fmt.Println("запусти вручную:      claude-remote run")
	fmt.Println("или как сервис:       claude-remote service install")
	return nil
}
