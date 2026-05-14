# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project

Single-file Go Telegram bot that fetches crypto prices from CoinGecko on a ticker and posts a formatted Markdown message to a Telegram channel. Also renders a **vertical bar chart of current USD prices** (one bar per coin, sorted most-expensive to cheapest, colored to match `coinColors`, dollar value labeled above each bar) as a TradingView-styled dark PNG, and posts it on a separate, configurable ticker via `sendPhoto`. Y-axis is positive-only, starting at 0; ticks use compact USD notation (`$100K`, `$2.5K`, …). Chart rendering uses **QuickChart** (https://quickchart.io — POST JSON, get PNG; free tier 60 req/min, or self-host with `ianw/quickchart` Docker image). Go-side dependency: `golang.org/x/image` (font drawing + high-quality bilinear scaling). Everything else is stdlib.

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
- `CHART_WINDOW` (default `session`) — either `session` (everything since bot start) or any duration like `15m`/`1h`/`24h`. Bar heights come from the **latest** sample in the window (the current USD price of each coin); the window mainly controls how stale `current` is allowed to be. Y-axis max = `1.15 × max(price)` to leave headroom for the value label above the tallest bar.
- `SAMPLE_INTERVAL` (default `20s`) — independent sampling cadence that feeds the chart history. Decoupled from the text ticker so the chart stays smooth even with a long `INTERVAL`.
- `QUICKCHART_URL` (default `https://quickchart.io`) — base URL of QuickChart. Override to point at a self-hosted instance.

## Architecture

All logic lives in `main.go`. Three goroutines run concurrently after `main()`:

1. **Text ticker** (`runCycle`) — every `INTERVAL`. Calls `fetchPrices` + `fetchWTIPerp` + `fetchUSDInToman`, builds a Markdown message via `formatMessage`, and posts via `sendToTelegram`. Each cycle is wrapped in a 30s `context.WithTimeout`. Also pushes the fetched prices into the shared `history` buffer.
2. **Sample ticker** — every `SAMPLE_INTERVAL`. Same fetch as above but skips the Telegram message; only records into `history`. Decoupled so chart density doesn't depend on text cadence.
3. **Chart ticker** (`runChartCycle`) — every `CHART_INTERVAL`. Snapshots `history` for the active window, builds a Chart.js v3 config in `buildQuickChartReq`, POSTs to `QUICKCHART_URL/chart` via `fetchQuickChartPNG`, composes the returned PNG with a price-list grid + USD→Toman footer inside `renderChartPNG`, and posts via `sendPhoto`.

The `history` struct (mutex-protected `[]sample`) is the single source of shared state. `maxAge = ChartWindowDur` (0 for `session`) caps memory.

`fetchPrices` makes one batched CoinGecko `/simple/price` request for all coin IDs in the package-level `coins` slice. `fetchWTIPerp` hits the Hyperliquid derivatives endpoint separately. `fetchUSDInToman` scrapes bonbast.com (two-step CSRF dance — see `bonbastParamRe`).

`renderChartPNG` requests a 2560×1080 PNG from QuickChart with `devicePixelRatio=1`, then scales it down to 1280×540 with `xdraw.CatmullRom.Scale` for crispness. Fonts inside the chart config are sized at ~22–26 pt so they remain readable after the down-scale.

`buildQuickChartReq` sends the chart config as a **JavaScript object literal string** (not a nested JSON object) so the `chartjs-plugin-datalabels` `formatter` can be a real JS function that prints `+1.23%` / `-0.45%` above each bar. A placeholder string `"__FORMATTER__"` is substituted with the JS function after `json.Marshal`; QuickChart parses the resulting string as JS.

### Adding a coin

Append to the `coins` slice in `main.go`. The `ID` must match the CoinGecko API id (visible on each coin's page on coingecko.com).

### Things to be aware of

- Telegram messages use `parse_mode=Markdown`. Any new dynamic text inserted into `formatMessage` must not contain unescaped Markdown special chars (`_ * [ ` `) or the Telegram API will reject the message.
- CoinGecko free tier is ~30 req/min; the 1-minute default interval is safe but shorter intervals risk rate-limiting.
- Errors inside `runCycle` are logged and swallowed by design — the bot keeps running. Don't change this to `log.Fatal`.
- The README is in Persian; user-facing log messages and errors in code are also Persian. Preserve that when editing.
- The QuickChart **public** instance is rate-limited (~60 req/min) and shared with everyone. For production, self-host with `docker run -d -p 8080:3400 --name quickchart ianw/quickchart` and set `QUICKCHART_URL=http://localhost:8080`.
- The Go-side fonts (`goregular`, `gobold`) do not include emoji glyphs. Anything drawn onto the canvas via `font.Drawer` must use plain ASCII/Latin/Persian text — keep emojis out of the rendered PNG (they belong in Telegram captions/text messages only).
