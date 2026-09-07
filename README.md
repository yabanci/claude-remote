# claude-remote

Control a running [Claude Code](https://claude.com/claude-code) session from Telegram.

Your laptop keeps the session. You get a chat window into it — from your phone, from another machine, from anywhere Telegram runs. Messages you send become input to the live session; what the session prints comes back as a reply. Sit down at the laptop and the same session is right there in your terminal, mid-conversation.

```
Telegram  ──►  claude-remote  ──►  tmux session  ──►  claude
   ▲                                    │
   └────────── pane diff ◄──────────────┘
```

## Why not just SSH?

You can SSH. But then you are typing into a TUI over a phone keyboard, holding a connection open, and losing the session when the tunnel dies. `claude-remote` sends one message and gets one reply — asynchronous, survives the network dropping, and reaches you as a normal push notification.

## How it works

The session runs inside a `tmux` session. For each incoming Telegram message, the bridge:

1. captures the tmux pane (the "before" snapshot),
2. types your message into the pane and presses Enter,
3. polls the pane until its contents stop changing (that's the reply being finished),
4. diffs against the "before" snapshot and sends you what's new.

That is screen scraping, deliberately. It means the bridge works with whatever the session prints, needs no API access, and leaves you a session you can attach to by hand at any time: `tmux attach -t main`.

## Install

```bash
go install github.com/yabanci/claude-remote/cmd/claude-remote@latest
```

Or grab a binary from [Releases](https://github.com/yabanci/claude-remote/releases).

Requirements: `tmux`, and the `claude` CLI on your `PATH`.

## Setup

1. Create a bot: message [@BotFather](https://t.me/BotFather), send `/newbot`, copy the token.
   Use a **dedicated** bot for this, not one you already use for notifications.

2. Run the wizard:

   ```bash
   claude-remote init
   ```

   It asks for the token, your numeric Telegram user id (optional — see below), and a default working directory. The config lands in `~/.config/claude-remote/config.yaml` with `0600` permissions.

3. Start it:

   ```bash
   claude-remote run                 # foreground
   claude-remote service install     # background, starts at login
   ```

4. Message your bot. That's it.

If you left the user id blank, the bridge binds to whoever messages it first and writes that id into the config permanently. Convenient, but it means anyone who has the token during that window could claim it — if that bothers you, set the id explicitly with [@userinfobot](https://t.me/userinfobot).

## Commands

Anything that isn't a bridge command is typed straight into the session — including Claude Code's own `/` commands, which pass through untouched. Bridge commands use a `cr_` prefix so they never collide.

| Command | Does |
|---|---|
| `/cr_status` | which sessions exist and which are running |
| `/cr_sessions` | list configured sessions and their directories |
| `/cr_use <name>` | switch which session your messages go to |
| `/cr_new <name> <dir>` | create a session for a project directory, start it, switch to it |
| `/cr_kill [name]` | stop a session |
| `/cr_restart [name]` | restart a session |
| `/cr_interrupt` | send Ctrl-C to the current session |
| `/cr_send <path>` | send a file from the working directory back to you |
| `/cr_help` | the same table, in chat |

Send a **file** to the bot and it lands in `<session dir>/telegram-inbox/`, and the session is told where it went — so you can ship a log or a screenshot from your phone and ask about it in the next message.

Replies longer than ~12k characters arrive as a `.txt` attachment instead of a wall of messages.

## Configuration

`~/.config/claude-remote/config.yaml`:

```yaml
bot_token: "123456:ABC-DEF..."      # or set CLAUDE_REMOTE_BOT_TOKEN
allowed_users:
  - 123456789                       # empty = bind to first sender
default_session: main
sessions:
  main:
    dir: ~/projects
    command: claude
  work:
    dir: ~/work/api
    command: claude
settle:
  poll_interval_ms: 1500            # how often to re-read the pane
  stable_rounds: 3                  # unchanged reads that mean "done"
  hard_cap_seconds: 1200            # give up waiting after this
  interim_notice_seconds: 120       # "still working…" ping interval
  cold_start_delay_ms: 3000         # wait after starting a session
  post_send_delay_ms: 2000          # wait before watching for a reply
```

`CLAUDE_REMOTE_BOT_TOKEN` overrides `bot_token`, so you can keep the token out of the file entirely.

Tune `stable_rounds` and `poll_interval_ms` if replies arrive truncated (raise them) or feel sluggish (lower them). The trade-off is real: the bridge cannot tell "thinking" from "finished", it can only tell "the screen stopped changing".

## Security

Read this part.

- **The bot token is a key to your machine.** Anyone who can message the bot as an allowed user can type anything into a session that has your shell, your files, and your credentials. Treat the token like an SSH private key.
- The allowlist is by Telegram user id, checked on every message. Messages from anyone else are dropped and logged.
- `/cr_send` will send you any file the session could read. That is intentional — it's your machine — but it means a leaked token is a data-exfiltration path, not just a nuisance.
- Everything runs locally over Telegram's HTTPS long-polling. No inbound ports, no tunnel, no third-party server beyond Telegram itself.
- Telegram bot chats are not end-to-end encrypted. Telegram can see what passes through. Don't pipe secrets through the chat.

## Limitations

- It reads a terminal pane, so tool-call noise and spinners can show up in replies alongside the actual answer.
- One message at a time per session: a new message waits for the previous reply to settle.
- Very long output may scroll past the captured history window (5000 lines).
- macOS and Linux only. `service install` uses launchd or systemd `--user`.

## Development

```bash
go build ./...
go test ./... -race
golangci-lint run ./...
```

## License

MIT — see [LICENSE](LICENSE).
