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

1. captures the tmux pane with scrollback (the "before" snapshot),
2. types your message into the pane and presses Enter,
3. polls **only the visible pane** until it stops changing (that's the reply being finished),
4. captures scrollback once more and diffs against "before" to send you what's new.

Step 3 deliberately reads the visible pane rather than the full scrollback: any new output
necessarily changes the bottom of the screen, so the cheap read is sufficient to detect
"still working", and the expensive one happens twice per reply instead of once per poll.

### What a reply looks like

A terminal screen is not a chat message, so the bridge does not forward one. It anchors on the
prompt it just typed, takes everything after it, and reduces that to the answer:

- assistant text only — tool invocations and their output are dropped
- no TUI chrome: rulers, status bar, context and usage meters, spinners, the input line
- no echo of your own message and no leftovers from the previous turn
- if the session produced only tool activity, you get one line saying so rather than a log
- if nothing matches the expected shape, the raw pane is cleaned and sent, so nothing is lost silently

The chat is treated as a chat, not a terminal: while the session is thinking you see Telegram's
typing indicator, answers come back as replies to the message that asked, and when the session
opens a numbered menu it arrives as tappable buttons — tapping one sends that choice into the
session.

An interactive dialog is never typed into. Prose is held back with the dialog shown to you; a short
answer (`1`, `2`, `yes`, `нет`) is passed through, so you can answer a menu deliberately. The
first-run "do you trust this folder?" question is the one thing the bridge will not relay an answer
for at all — confirm that in the terminal.

That is screen scraping, deliberately. It means the bridge works with whatever the session prints, needs no API access, and leaves you a session you can attach to by hand at any time: `tmux attach -t cr-main`.

## Install

```bash
go install github.com/yabanci/claude-remote/cmd/claude-remote@latest
```

Or grab a binary from [Releases](https://github.com/yabanci/claude-remote/releases).

Requirements: `tmux`, the `claude` CLI on your `PATH`, and Go 1.25+ if building from source.

The 1.25 floor is deliberate: the bridge speaks TLS to Telegram, and Go standard libraries
older than 1.25.11 carry CVEs in `crypto/tls`, `crypto/x509` and `net/http` that this code path
actually reaches — `govulncheck` in CI fails the build on them.

## Setup

1. Create a bot: message [@BotFather](https://t.me/BotFather), send `/newbot`, copy the token.
   Use a **dedicated** bot for this, not one you already use for notifications.

2. Run the wizard:

   ```bash
   claude-remote init
   ```

   It asks for the token, your numeric Telegram user id (optional — see below), and a default working directory. The config is written with `0600` permissions to the OS config directory: `~/Library/Application Support/claude-remote/config.yaml` on macOS, `~/.config/claude-remote/config.yaml` on Linux.

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
| `/cr_peek` | show the session screen without sending anything into it |
| `/cr_send <path>` | send a file from the working directory back to you |
| `/cr_help` | the same table, in chat |

Send a **file** to the bot and it lands in `<session dir>/telegram-inbox/`, and the session is told where it went — so you can ship a log or a screenshot from your phone and ask about it in the next message.

Replies longer than ~12k characters arrive as a `.txt` attachment instead of a wall of messages.

## Configuration

Config file (see paths above):

```yaml
bot_token: "123456:ABC-DEF..."      # or set CLAUDE_REMOTE_BOT_TOKEN
allowed_users:
  - 123456789                       # empty = bind to first sender
allowed_chats:
  - 123456789                       # empty = any chat that user writes in
default_session: main
sessions:
  main:
    dir: ~/projects
    command: claude
  work:
    dir: ~/work/api
    command: claude
api_base: ""                        # optional: a self-hosted Bot API server
max_retries: 3                      # retries when Telegram answers 429
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

The installed service inherits the `PATH` of the shell that installed it. That matters: launchd
and systemd hand a service a minimal `PATH`, and `tmux` from Homebrew would not be found.
Install from a shell where `tmux -V` and `claude --version` both work.

## Security

Read this part.

- **The bot token is a key to your machine.** Anyone who can message the bot as an allowed user can type anything into a session that has your shell, your files, and your credentials. Treat the token like an SSH private key.
- The allowlist is by Telegram user id *and* chat id, both checked on every message. Your own id alone is
  not enough: writing from a group would otherwise publish the pane — sources, keys, log output — to
  everyone in it. `init` pins both, and a config with no `allowed_chats` keeps the previous behaviour.
- Sessions the bridge runs are named `cr-<name>` in tmux. It can neither read nor kill a session you
  started yourself, even one named identically — the default session name is `main`, which is also the
  most common name people use by hand.
- `/cr_send` will send you any file the session could read. That is intentional — it's your machine — but it means a leaked token is a data-exfiltration path, not just a nuisance.
- Everything runs locally over Telegram's HTTPS long-polling. No inbound ports, no tunnel, no third-party server beyond Telegram itself.
- Telegram bot chats are not end-to-end encrypted. Telegram can see what passes through. Don't pipe secrets through the chat.

## Resource use

Measured on an Apple M1 (`go test ./internal/tmux -bench BenchmarkCapturePane -benchmem`):

| | per call | allocated |
|---|---|---|
| capture with 5000 lines of scrollback | 7.76 ms | 751 KB |
| capture of the visible pane only | 5.38 ms | 52 KB |

Polling the visible pane instead of the scrollback turns a 60-second reply (~40 polls) from
~30 MB of garbage into ~2 MB. Most of the remaining cost is forking `tmux` itself.

Idle, the daemon sits at **~10 MB RSS and 0% CPU** — one long-poll HTTP request every 25 seconds.
The heavy processes on your machine are `claude` and `tmux`, not this bridge.

## Limitations

- It reads a terminal pane, so tool-call noise and spinners can show up in replies alongside the actual answer.
- One message at a time per session: a new message waits for the previous reply to settle.
- Very long output may scroll past the captured history window (5000 lines).
- macOS and Linux only. `service install` uses launchd or systemd `--user`.

## Development

```bash
go build ./...
go test ./... -race -cover
golangci-lint run ./...
go test ./internal/tmux -bench BenchmarkCapturePane -benchmem
```

Coverage: `tmux` 90%, `service` 86%, `bridge` 79%, `telegram` 78%, `config` 75%, `cmd` 52%.

The suite is layered: pure-function unit tests, fuzz targets for the two functions with hard
invariants (`SplitForTelegram` must rejoin to the original with every chunk valid UTF-8;
`DiffTail` must return a substring of the new screen), integration tests that drive a real
`tmux`, and end-to-end tests that run the whole loop against a real session and a fake Bot API —
including output that scrolls past the visible pane, consecutive turns, and a goroutine-leak check.

Every bridge command is exercised against a real `tmux` session, not a mock: `/cr_new` must leave
a session that actually exists and a config that survives a restart, `/cr_interrupt` must stop a
running `sleep`, `/cr_send` must deliver a real file, an uploaded file must land on disk and the
session must be told where, and output past the inline limit must arrive as an attachment.

Two opt-in tests talk to the real Bot API — set `CLAUDE_REMOTE_LIVE_TOKEN_FILE` and
`CLAUDE_REMOTE_LIVE_CHAT_FILE` to run them. They check that Telegram accepts the inline keyboard
and chat-action payloads this code builds, which a local fake cannot prove.

CI runs on Linux and macOS, plus `govulncheck` and a 60-second fuzz round per target.

Anything that shells out sits behind an interface declared at the call site (`bridge.Runner`,
`service.CommandRunner`, `service.platform`), so the whole suite runs without a tmux session,
without touching `launchctl`/`systemctl`, and exercises the launchd *and* systemd paths on
either OS. The tests that do drive real `tmux` skip themselves when it isn't installed.

## License

MIT — see [LICENSE](LICENSE).
