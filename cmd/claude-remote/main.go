package main

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/yabanci/claude-remote/internal/bridge"
	"github.com/yabanci/claude-remote/internal/config"
	"github.com/yabanci/claude-remote/internal/service"
	"github.com/yabanci/claude-remote/internal/telegram"
)

var version = "dev"

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		printUsage(stdout)
		return 1
	}

	var err error
	switch args[0] {
	case "init":
		err = cmdInit(args[1:])
	case "run":
		err = cmdRun(args[1:])
	case "service":
		err = cmdService(args[1:], stdout)
	case "version":
		_, _ = fmt.Fprintln(stdout, version)
	case "help", "-h", "--help":
		printUsage(stdout)
	default:
		printUsage(stdout)
		return 1
	}

	if err != nil {
		_, _ = fmt.Fprintln(stderr, "error:", err)
		return 1
	}
	return 0
}

func printUsage(w io.Writer) {
	_, _ = fmt.Fprint(w, `claude-remote — control a running claude session over Telegram

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
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return runBridge(ctx, args)
}

func runBridge(ctx context.Context, args []string) error {
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
	var clientOpts []telegram.Option
	if cfg.APIBase != "" {
		clientOpts = append(clientOpts, telegram.WithBaseURL(cfg.APIBase))
	}
	if cfg.MaxRetries > 0 {
		clientOpts = append(clientOpts, telegram.WithRetryPolicy(cfg.MaxRetries, time.Sleep))
	}
	tg := telegram.NewClient(cfg.ResolveToken(), clientOpts...)
	runner := bridge.NewTmuxRunner()
	br := bridge.New(cfg, configPath, tg, runner, logger, stateDir)

	logger.Info("claude-remote starting", "version", version, "config", configPath)
	if err := br.Run(ctx); err != nil {
		return fmt.Errorf("bridge stopped with error: %w", err)
	}
	logger.Info("claude-remote stopped")
	return nil
}

type serviceManager interface {
	Install() error
	Uninstall() error
	Status() (string, error)
}

func cmdService(args []string, stdout io.Writer) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: claude-remote service <install|uninstall|status>")
	}

	execPath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("resolve executable path: %w", err)
	}

	return runService(args, service.NewManager(execPath), stdout)
}

func runService(args []string, mgr serviceManager, stdout io.Writer) error {
	switch args[0] {
	case "install":
		if err := verifyConfigBeforeInstall(args); err != nil {
			return err
		}
		if err := mgr.Install(); err != nil {
			return fmt.Errorf("install service: %w", err)
		}
		_, _ = fmt.Fprintln(stdout, "service installed and started")
	case "uninstall":
		if err := mgr.Uninstall(); err != nil {
			return fmt.Errorf("uninstall service: %w", err)
		}
		_, _ = fmt.Fprintln(stdout, "service uninstalled")
	case "status":
		status, err := mgr.Status()
		if err != nil {
			return fmt.Errorf("service status: %w", err)
		}
		_, _ = fmt.Fprintln(stdout, strings.TrimSpace(status))
	default:
		return fmt.Errorf("unknown service subcommand %q", args[0])
	}
	return nil
}

func verifyConfigBeforeInstall(args []string) error {
	configPath, err := resolveConfigPath(args)
	if err != nil {
		return err
	}

	cfg, err := config.Load(configPath)
	if err != nil {
		return fmt.Errorf("refusing to install a service that cannot start: %w\n\nrun `claude-remote init` first", err)
	}
	if err := cfg.Validate(); err != nil {
		return fmt.Errorf("refusing to install a service that cannot start: config %s is invalid: %w", configPath, err)
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
