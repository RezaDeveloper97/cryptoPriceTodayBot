# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project

Single-file Go Telegram bot that fetches crypto prices from CoinGecko on a ticker and posts a formatted Markdown message to a Telegram channel. Also renders a multi-line price chart (one color per coin) as a PNG and posts it on a separate, configurable ticker via `sendPhoto`. Depends on `github.com/wcharczuk/go-chart/v2` (chart rendering) and `golang.org/x/image` (font drawing) — everything else is stdlib.

## Commands

```bash
# Run (requires TELEGRAM_BOT_TOKEN and TELEGRAM_CHANNEL_ID env vars; see env.example)
go run main.go

# Build
go build -o crypto-bot .

# Format / vet (no tests exist yet)
go fmt ./...
go vet ./...
```

Optional `INTERVAL` env var accepts any `time.ParseDuration` value (default `1m`).

Chart envs:
- `CHART_INTERVAL` (default `5m`) — how often the chart image is posted.
- `CHART_WINDOW` (default `session`) — either `session` (everything since bot start) or any duration like `15m`/`1h`/`24h`. Y-axis = percentage change from the first sample inside the window.

## Architecture

All logic lives in `main.go`. The flow is a single goroutine driven by `time.Ticker`:

1. `loadConfig` reads env vars into `Config`.
2. `main` builds an `http.Client` (20s timeout), sets up `signal.NotifyContext` for SIGINT/SIGTERM, runs one cycle immediately, then loops on the ticker.
3. `runCycle` wraps each iteration in its own 30s `context.WithTimeout` so a slow cycle cannot block the next tick. It calls `fetchPrices` → `formatMessage` → `sendToTelegram` and logs (never exits) on error.
4. `fetchPrices` makes one batched CoinGecko `/simple/price` request for all coin IDs in the package-level `coins` slice.
5. `formatMessage` builds a Telegram Markdown message with 🟢/🔴 based on 24h change, prices formatted via `formatPrice`/`addThousandsSep` (2 decimals + thousands separators above $1, 6 decimals below), and a timestamp in `Asia/Tehran`.

### Adding a coin

Append to the `coins` slice in `main.go`. The `ID` must match the CoinGecko API id (visible on each coin's page on coingecko.com).

### Things to be aware of

- Telegram messages use `parse_mode=Markdown`. Any new dynamic text inserted into `formatMessage` must not contain unescaped Markdown special chars (`_ * [ ` `) or the Telegram API will reject the message.
- CoinGecko free tier is ~30 req/min; the 1-minute default interval is safe but shorter intervals risk rate-limiting.
- Errors inside `runCycle` are logged and swallowed by design — the bot keeps running. Don't change this to `log.Fatal`.
- The README is in Persian; user-facing log messages and errors in code are also Persian. Preserve that when editing.
