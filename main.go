package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"io"
	"log"
	"mime/multipart"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"os/signal"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	xdraw "golang.org/x/image/draw"
	xfont "golang.org/x/image/font"
	"golang.org/x/image/font/gofont/gobold"
	"golang.org/x/image/font/gofont/goregular"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/math/fixed"
)

// Coin یک ارز دیجیتال در CoinGecko را توصیف می‌کند
type Coin struct {
	ID     string // شناسه در CoinGecko
	Symbol string // نماد بازار مثل BTC
	Name   string // نام انگلیسی برای نمایش
	Emoji  string // ایموجی نماد ارز
}

// لیست ارزهایی که می‌خواهیم رصد کنیم
// برای اضافه کردن ارز جدید، id را از coingecko.com پیدا کنید
var coins = []Coin{
	{ID: "bitcoin", Symbol: "BTC", Name: "Bitcoin", Emoji: "🟠"},
	{ID: "tether-gold", Symbol: "XAUT", Name: "Tether Gold", Emoji: "🥇"},
	{ID: "pax-gold", Symbol: "PAXG", Name: "PAX Gold", Emoji: "🥇"},
	{ID: "ishares-silver-trust-ondo-tokenized-stock", Symbol: "SLVON", Name: "iShares Silver", Emoji: "🥈"},
	{ID: "wti-perp", Symbol: "WTI", Name: "WTI Crude Oil", Emoji: "🛢️"},
	{ID: "ethereum", Symbol: "ETH", Name: "Ethereum", Emoji: "🔷"},
	{ID: "tether", Symbol: "USDT", Name: "Tether", Emoji: "💵"},
	{ID: "binancecoin", Symbol: "BNB", Name: "BNB", Emoji: "🟡"},
	{ID: "ripple", Symbol: "XRP", Name: "Ripple", Emoji: "🔵"},
	{ID: "solana", Symbol: "SOL", Name: "Solana", Emoji: "🟣"},
	{ID: "tron", Symbol: "TRX", Name: "Tron", Emoji: "🔴"},
	{ID: "dogecoin", Symbol: "DOGE", Name: "Dogecoin", Emoji: "🐕"},
}

// پالت رنگ هر ارز برای استفاده هم در خطوط نمودار و هم در مربع کنار قیمت‌ها
var coinColors = map[string]color.RGBA{
	"bitcoin":     {247, 147, 26, 255},
	"tether-gold": {212, 175, 55, 255},
	"pax-gold":    {255, 193, 37, 255},
	"ishares-silver-trust-ondo-tokenized-stock": {130, 130, 140, 255},
	"wti-perp":    {51, 51, 51, 255},
	"ethereum":    {98, 126, 234, 255},
	"tether":      {38, 161, 123, 255},
	"binancecoin": {243, 186, 47, 255},
	"ripple":      {35, 41, 47, 255},
	"solana":      {153, 69, 255, 255},
	"tron":        {235, 0, 41, 255},
	"dogecoin":    {186, 160, 82, 255},
}

type Config struct {
	BotToken       string        // توکن گرفته شده از BotFather
	ChannelID      string        // @yourchannel یا -100xxxxxxxxx
	BotUsername    string        // نام کاربری ربات بدون @ برای ساخت لینک عمیق دکمه تبدیل — اختیاری
	Interval       time.Duration // فاصله ارسال پیام متنی - پیش‌فرض ۱ دقیقه
	ChartInterval  time.Duration // فاصله ارسال عکس نمودار - پیش‌فرض ۵ دقیقه
	ChartWindowDur time.Duration // پنجره نمایش روی نمودار. 0 یعنی session
	ChartWindowRaw string        // مقدار خام برای نمایش روی عکس
	QuickChartURL  string        // base URL سرویس QuickChart — پیش‌فرض https://quickchart.io
}

func loadConfig() (*Config, error) {
	token := os.Getenv("TELEGRAM_BOT_TOKEN")
	if token == "" {
		return nil, fmt.Errorf("متغیر محیطی TELEGRAM_BOT_TOKEN تنظیم نشده")
	}
	channel := os.Getenv("TELEGRAM_CHANNEL_ID")
	if channel == "" {
		return nil, fmt.Errorf("متغیر محیطی TELEGRAM_CHANNEL_ID تنظیم نشده")
	}

	interval := time.Minute
	if v := os.Getenv("INTERVAL"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil {
			return nil, fmt.Errorf("مقدار INTERVAL نامعتبر است: %w", err)
		}
		interval = d
	}

	chartInterval := 5 * time.Minute
	if v := os.Getenv("CHART_INTERVAL"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil {
			return nil, fmt.Errorf("مقدار CHART_INTERVAL نامعتبر است: %w", err)
		}
		chartInterval = d
	}

	windowRaw := os.Getenv("CHART_WINDOW")
	if windowRaw == "" {
		windowRaw = "session"
	}
	var windowDur time.Duration
	if windowRaw != "session" {
		d, err := time.ParseDuration(windowRaw)
		if err != nil {
			return nil, fmt.Errorf("مقدار CHART_WINDOW نامعتبر است (یا session یا مثل 15m/1h/24h): %w", err)
		}
		windowDur = d
	}

	quickChart := strings.TrimRight(os.Getenv("QUICKCHART_URL"), "/")
	if quickChart == "" {
		quickChart = "https://quickchart.io"
	}

	botUsername := strings.TrimPrefix(strings.TrimSpace(os.Getenv("TELEGRAM_BOT_USERNAME")), "@")

	return &Config{
		BotToken:       token,
		ChannelID:      channel,
		BotUsername:    botUsername,
		Interval:       interval,
		ChartInterval:  chartInterval,
		ChartWindowDur: windowDur,
		ChartWindowRaw: windowRaw,
		QuickChartURL:  quickChart,
	}, nil
}

// ساختار پاسخ CoinGecko برای هر ارز
type priceInfo struct {
	USD       float64 `json:"usd"`
	Change24h float64 `json:"usd_24h_change"`
}

// fetchPrices قیمت همه ارزها را با یک درخواست از CoinGecko می‌گیرد
func fetchPrices(ctx context.Context, client *http.Client) (map[string]priceInfo, error) {
	ids := make([]string, 0, len(coins))
	for _, c := range coins {
		if c.ID == "wti-perp" {
			continue // قیمت WTI از CoinGecko/derivatives گرفته می‌شود نه simple/price
		}
		ids = append(ids, c.ID)
	}
	endpoint := fmt.Sprintf(
		"https://api.coingecko.com/api/v3/simple/price?ids=%s&vs_currencies=usd&include_24hr_change=true",
		strings.Join(ids, ","),
	)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("کد وضعیت CoinGecko %d: %s", resp.StatusCode, string(body))
	}

	var data map[string]priceInfo
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return nil, fmt.Errorf("پارس پاسخ شکست خورد: %w", err)
	}
	return data, nil
}

// fetchLivePrice قیمت USD یک ارز را به‌تنهایی از CoinGecko می‌گیرد. برای ارزهایی
// که در coins ردیابی نمی‌شوند (مثل TRX, POL, ...) استفاده می‌شود.
func fetchLivePrice(ctx context.Context, client *http.Client, id string) (float64, error) {
	endpoint := fmt.Sprintf(
		"https://api.coingecko.com/api/v3/simple/price?ids=%s&vs_currencies=usd",
		url.QueryEscape(id),
	)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return 0, err
	}
	req.Header.Set("Accept", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 256))
		return 0, fmt.Errorf("کد وضعیت CoinGecko %d: %s", resp.StatusCode, string(body))
	}

	var data map[string]struct {
		USD float64 `json:"usd"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return 0, fmt.Errorf("پارس پاسخ شکست خورد: %w", err)
	}
	p, ok := data[id]
	if !ok || p.USD <= 0 {
		return 0, fmt.Errorf("قیمت برای %s پیدا نشد", id)
	}
	return p.USD, nil
}

// loadCoinIndex ۲۵۰ ارز برتر بازار را از CoinGecko می‌گیرد و در symToID /
// currencyAlias جای می‌دهد تا مبدل بتواند هر ارز معتبری را پشتیبانی کند.
// ارزهای ردیابی‌شده فعلی override نمی‌شوند. تعداد ارز اضافه‌شده را برمی‌گرداند.
func loadCoinIndex(ctx context.Context, client *http.Client) (int, error) {
	const endpoint = "https://api.coingecko.com/api/v3/coins/markets?vs_currency=usd&order=market_cap_desc&per_page=250&page=1"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return 0, err
	}
	req.Header.Set("Accept", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 256))
		return 0, fmt.Errorf("کد وضعیت CoinGecko %d: %s", resp.StatusCode, string(body))
	}

	var data []struct {
		ID     string `json:"id"`
		Symbol string `json:"symbol"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return 0, fmt.Errorf("پارس پاسخ شکست خورد: %w", err)
	}

	added := 0
	for _, c := range data {
		if c.Symbol == "" || c.ID == "" {
			continue
		}
		sym := strings.ToUpper(c.Symbol)
		// ارزهای ردیابی‌شده override نمی‌شوند — همان history آن‌ها معتبر است
		if _, exists := symToID[sym]; exists {
			continue
		}
		symToID[sym] = c.ID
		currencyAlias[strings.ToLower(c.Symbol)] = sym
		if lowerID := strings.ToLower(c.ID); lowerID != strings.ToLower(c.Symbol) {
			// نام id را هم به عنوان alias اضافه کن (مثل "cardano" → "ADA")
			if _, exists := currencyAlias[lowerID]; !exists {
				currencyAlias[lowerID] = sym
			}
		}
		added++
	}
	return added, nil
}

// fetchWTIPerp قیمت قرارداد پرپچوال WTI را از Hyperliquid (از طریق CoinGecko) می‌گیرد.
// WTI روی endpoint simple/price نیست چون یک قرارداد مشتقه است نه توکن اسپات.
func fetchWTIPerp(ctx context.Context, client *http.Client) (priceInfo, error) {
	const endpoint = "https://api.coingecko.com/api/v3/derivatives/exchanges/hyperliquid?include_tickers=all"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return priceInfo{}, err
	}
	req.Header.Set("Accept", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return priceInfo{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return priceInfo{}, fmt.Errorf("کد وضعیت Hyperliquid %d: %s", resp.StatusCode, string(body))
	}

	var data struct {
		Tickers []struct {
			Symbol string  `json:"symbol"`
			Last   float64 `json:"last"`
			H24    float64 `json:"h24_percentage_change"`
		} `json:"tickers"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return priceInfo{}, fmt.Errorf("پارس پاسخ Hyperliquid شکست خورد: %w", err)
	}
	for _, t := range data.Tickers {
		if t.Symbol == "CASH:WTI-USD" {
			return priceInfo{USD: t.Last, Change24h: t.H24}, nil
		}
	}
	return priceInfo{}, fmt.Errorf("نماد CASH:WTI-USD در tickers پیدا نشد")
}

// formatPrice برای قیمت‌های بالای ۱ دلار دو رقم اعشار و برای ارزهای ارزان شش رقم
// همراه با جداکننده هزارگان برای خوانایی
func formatPrice(v float64) string {
	if v >= 1 {
		return addThousandsSep(fmt.Sprintf("%.2f", v))
	}
	return fmt.Sprintf("%.6f", v)
}

// addThousandsSep کاما به بخش صحیح عدد اضافه می‌کند: 67000.50 -> 67,000.50
func addThousandsSep(s string) string {
	dot := strings.IndexByte(s, '.')
	intPart := s
	frac := ""
	if dot >= 0 {
		intPart = s[:dot]
		frac = s[dot:]
	}
	n := len(intPart)
	if n <= 3 {
		return s
	}
	var b strings.Builder
	first := n % 3
	if first > 0 {
		b.WriteString(intPart[:first])
		if n > first {
			b.WriteByte(',')
		}
	}
	for i := first; i < n; i += 3 {
		b.WriteString(intPart[i : i+3])
		if i+3 < n {
			b.WriteByte(',')
		}
	}
	b.WriteString(frac)
	return b.String()
}

const bonbastUserAgent = "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0 Safari/537.36"

// regex برای استخراج پارامتر CSRF از HTML صفحه اصلی bonbast
// قالب: param: "TOKEN,CSRF,TIMESTAMP"
var bonbastParamRe = regexp.MustCompile(`param:\s*"([^"]+)"`)

// fetchUSDInToman قیمت لحظه‌ای دلار آمریکا به تومان را از bonbast.com (بازار آزاد ایران)
// می‌گیرد. روال: GET صفحه اصلی برای دریافت پارامتر CSRF و کوکی، سپس POST به /json.
// مقدار usd1 (قیمت فروش) که bonbast به تومان برمی‌گرداند را پارس می‌کند.
func fetchUSDInToman(ctx context.Context, baseClient *http.Client) (float64, error) {
	// کوکی‌جار محلی برای هر فراخوانی، چون پارامتر صفحه و کوکی‌اش با هم گره خورده‌اند
	jar, err := cookiejar.New(nil)
	if err != nil {
		return 0, err
	}
	client := &http.Client{Timeout: baseClient.Timeout, Jar: jar}

	homeReq, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://www.bonbast.com/", nil)
	if err != nil {
		return 0, err
	}
	homeReq.Header.Set("User-Agent", bonbastUserAgent)
	homeReq.Header.Set("Accept", "text/html")

	homeResp, err := client.Do(homeReq)
	if err != nil {
		return 0, fmt.Errorf("درخواست صفحه bonbast شکست خورد: %w", err)
	}
	defer homeResp.Body.Close()
	if homeResp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("کد وضعیت bonbast %d", homeResp.StatusCode)
	}
	html, err := io.ReadAll(io.LimitReader(homeResp.Body, 1<<20))
	if err != nil {
		return 0, err
	}

	m := bonbastParamRe.FindSubmatch(html)
	if len(m) < 2 {
		return 0, fmt.Errorf("پارامتر bonbast در HTML پیدا نشد")
	}
	param := string(m[1])

	form := url.Values{}
	form.Set("param", param)
	jsonReq, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://www.bonbast.com/json", strings.NewReader(form.Encode()))
	if err != nil {
		return 0, err
	}
	jsonReq.Header.Set("User-Agent", bonbastUserAgent)
	jsonReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	jsonReq.Header.Set("X-Requested-With", "XMLHttpRequest")
	jsonReq.Header.Set("Referer", "https://www.bonbast.com/")
	jsonReq.Header.Set("Origin", "https://www.bonbast.com")
	jsonReq.Header.Set("Accept", "application/json")

	jsonResp, err := client.Do(jsonReq)
	if err != nil {
		return 0, fmt.Errorf("درخواست JSON bonbast شکست خورد: %w", err)
	}
	defer jsonResp.Body.Close()
	if jsonResp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("کد وضعیت bonbast/json %d", jsonResp.StatusCode)
	}

	var data map[string]any
	if err := json.NewDecoder(jsonResp.Body).Decode(&data); err != nil {
		return 0, fmt.Errorf("پارس JSON bonbast شکست خورد: %w", err)
	}

	if _, expired := data["reset"]; expired {
		return 0, fmt.Errorf("نشست bonbast منقضی شد (reset=1)")
	}

	usdRaw, ok := data["usd1"]
	if !ok {
		return 0, fmt.Errorf("فیلد usd1 در پاسخ bonbast نبود")
	}
	usdStr, ok := usdRaw.(string)
	if !ok {
		return 0, fmt.Errorf("نوع usd1 در پاسخ bonbast غیرمنتظره: %T", usdRaw)
	}
	toman, err := strconv.ParseFloat(strings.ReplaceAll(usdStr, ",", ""), 64)
	if err != nil {
		return 0, fmt.Errorf("قیمت دلار bonbast نامعتبر: %w", err)
	}
	return toman, nil
}

// formatMessage پیام نهایی Markdown را می‌سازد
func formatMessage(prices map[string]priceInfo, usdToman float64) string {
	var b strings.Builder
	b.WriteString("📊 Live Crypto Prices\n")
	b.WriteString("━━━━━━━━━━━━━━━━━━━━\n")

	for _, c := range coins {
		p, ok := prices[c.ID]
		if !ok {
			continue
		}
		sign := "🟢"
		if p.Change24h < 0 {
			sign = "🔴"
		}
		fmt.Fprintf(&b,
			"%s %s · $%s · %+.2f%%\n",
			sign, c.Symbol,
			formatPrice(p.USD), p.Change24h,
		)
	}

	b.WriteString("━━━━━━━━━━━━━━━━━━━━\n")
	if usdToman > 0 {
		fmt.Fprintf(&b,
			"💵 1 USD ≈ %s Toman\n",
			addThousandsSep(fmt.Sprintf("%.0f", usdToman)),
		)
	} else {
		b.WriteString("💵 USD/IRR unavailable\n")
	}

	// زمان به وقت تهران
	loc, err := time.LoadLocation("Asia/Tehran")
	if err != nil {
		loc = time.UTC
	}
	fmt.Fprintf(&b,
		"🕐 %s (Tehran)",
		time.Now().In(loc).Format("2006-01-02 15:04"),
	)
	return b.String()
}

// sendToTelegram پیام را به کانال می‌فرستد. اگر replyMarkup خالی نباشد، به
// عنوان مقدار خام reply_markup (JSON) به API ارسال می‌شود.
func sendToTelegram(ctx context.Context, client *http.Client, cfg *Config, text, replyMarkup string) error {
	api := fmt.Sprintf("https://api.telegram.org/bot%s/sendMessage", cfg.BotToken)

	form := url.Values{}
	form.Set("chat_id", cfg.ChannelID)
	form.Set("text", text)
	form.Set("parse_mode", "Markdown")
	form.Set("disable_web_page_preview", "true")
	if replyMarkup != "" {
		form.Set("reply_markup", replyMarkup)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, api, strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return fmt.Errorf("تلگرام کد %d برگرداند: %s", resp.StatusCode, string(body))
	}
	return nil
}

// convertButtonMarkup مارک‌آپ JSON برای دکمه inline «🔁 تبدیل» را می‌سازد.
// اگر BotUsername تنظیم نشده باشد رشته خالی برمی‌گرداند تا دکمه پیوست نشود.
func convertButtonMarkup(cfg *Config) string {
	if cfg.BotUsername == "" {
		return ""
	}
	markup := map[string]any{
		"inline_keyboard": [][]map[string]string{{{
			"text": "🔁 تبدیل",
			"url":  "https://t.me/" + cfg.BotUsername + "?start=convert",
		}}},
	}
	b, err := json.Marshal(markup)
	if err != nil {
		return ""
	}
	return string(b)
}

// sendPhoto یک عکس PNG را به کانال تلگرام به‌صورت multipart می‌فرستد
func sendPhoto(ctx context.Context, client *http.Client, cfg *Config, pngBytes []byte, caption string) error {
	api := fmt.Sprintf("https://api.telegram.org/bot%s/sendPhoto", cfg.BotToken)

	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	_ = w.WriteField("chat_id", cfg.ChannelID)
	if caption != "" {
		_ = w.WriteField("caption", caption)
		_ = w.WriteField("parse_mode", "Markdown")
	}
	part, err := w.CreateFormFile("photo", "chart.png")
	if err != nil {
		return err
	}
	if _, err := part.Write(pngBytes); err != nil {
		return err
	}
	if err := w.Close(); err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, api, &body)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", w.FormDataContentType())

	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		buf, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return fmt.Errorf("تلگرام (sendPhoto) کد %d برگرداند: %s", resp.StatusCode, string(buf))
	}
	return nil
}

// sendPrivate پیام مارک‌داون به chat_id دلخواه می‌فرستد. برای پاسخ‌های DM
// مبدل ارز استفاده می‌شود. replyMarkup خالی یعنی بدون کیبورد inline.
func sendPrivate(ctx context.Context, client *http.Client, cfg *Config, chatID int64, text, replyMarkup string) error {
	api := fmt.Sprintf("https://api.telegram.org/bot%s/sendMessage", cfg.BotToken)

	form := url.Values{}
	form.Set("chat_id", strconv.FormatInt(chatID, 10))
	form.Set("text", text)
	form.Set("parse_mode", "Markdown")
	form.Set("disable_web_page_preview", "true")
	if replyMarkup != "" {
		form.Set("reply_markup", replyMarkup)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, api, strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return fmt.Errorf("تلگرام (sendPrivate) کد %d برگرداند: %s", resp.StatusCode, string(body))
	}
	return nil
}

// answerCallback اسپینر روی دکمه‌ی inline را پاک می‌کند
func answerCallback(ctx context.Context, client *http.Client, cfg *Config, callbackID string) error {
	api := fmt.Sprintf("https://api.telegram.org/bot%s/answerCallbackQuery", cfg.BotToken)

	form := url.Values{}
	form.Set("callback_query_id", callbackID)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, api, strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	resp.Body.Close()
	return nil
}

// ratesCache آخرین نرخ USD→تومان را برای استفاده در تبدیل‌ها نگه می‌دارد
type ratesCache struct {
	mu        sync.Mutex
	usdToman  float64
	updatedAt time.Time
}

func (r *ratesCache) set(v float64) {
	r.mu.Lock()
	r.usdToman = v
	r.updatedAt = time.Now()
	r.mu.Unlock()
}

func (r *ratesCache) get() (float64, time.Time) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.usdToman, r.updatedAt
}

// livePriceCache قیمت زنده ارزهای غیرردیابی‌شده را برای مدتی نگه می‌دارد
// تا با هر تبدیل به CoinGecko درخواست نزنیم.
type livePriceCache struct {
	mu  sync.Mutex
	m   map[string]livePrice
	ttl time.Duration
}

type livePrice struct {
	usd       float64
	fetchedAt time.Time
}

func (c *livePriceCache) lookup(id string) (float64, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	p, ok := c.m[id]
	if !ok || time.Since(p.fetchedAt) > c.ttl {
		return 0, false
	}
	return p.usd, true
}

func (c *livePriceCache) store(id string, usd float64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.m == nil {
		c.m = make(map[string]livePrice)
	}
	c.m[id] = livePrice{usd: usd, fetchedAt: time.Now()}
}

// convDeps وابستگی‌های مشترک تبدیل ارز را در یک struct جمع می‌کند تا
// امضای توابع داخلی شلوغ نشود.
type convDeps struct {
	client *http.Client
	hist   *history
	rates  *ratesCache
	live   *livePriceCache
}

// sample یک نمونه از قیمت‌های همه ارزها در یک لحظه
type sample struct {
	t      time.Time
	prices map[string]priceInfo // coin ID -> USD + 24h change
}

// history بافر ایمن از thread برای نگه‌داری نمونه‌های قیمت
type history struct {
	mu      sync.Mutex
	samples []sample
	maxAge  time.Duration // 0 یعنی نامحدود (حالت session)
}

func (h *history) add(s sample) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.samples = append(h.samples, s)
	if h.maxAge > 0 {
		// نمونه‌های قدیمی‌تر از 2*maxAge پاک می‌شوند تا حافظه کنترل شود
		cutoff := s.t.Add(-2 * h.maxAge)
		idx := 0
		for i, x := range h.samples {
			if x.t.After(cutoff) {
				idx = i
				break
			}
		}
		if idx > 0 {
			h.samples = append([]sample(nil), h.samples[idx:]...)
		}
	}
}

// snapshot یک کپی از نمونه‌های داخل پنجره برمی‌گرداند. win=0 یعنی همه.
func (h *history) snapshot(win time.Duration) []sample {
	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.samples) == 0 {
		return nil
	}
	if win <= 0 {
		out := make([]sample, len(h.samples))
		copy(out, h.samples)
		return out
	}
	cutoff := time.Now().Add(-win)
	start := 0
	for i, x := range h.samples {
		if !x.t.Before(cutoff) {
			start = i
			break
		}
		start = i + 1
	}
	if start >= len(h.samples) {
		return nil
	}
	out := make([]sample, len(h.samples)-start)
	copy(out, h.samples[start:])
	return out
}

// recordSample نمونه فعلی را در history ذخیره می‌کند
func recordSample(h *history, prices map[string]priceInfo) {
	if len(prices) == 0 {
		return
	}
	ps := make(map[string]priceInfo, len(prices))
	for id, p := range prices {
		ps[id] = p
	}
	h.add(sample{t: time.Now(), prices: ps})
}

// فونت‌های بارگذاری‌شده برای ترکیب تصویر نهایی
var (
	faceRegular xfont.Face
	faceBold    xfont.Face
	faceTitle   xfont.Face
)

func init() {
	reg, err := opentype.Parse(goregular.TTF)
	if err != nil {
		panic(err)
	}
	bold, err := opentype.Parse(gobold.TTF)
	if err != nil {
		panic(err)
	}
	faceRegular, err = opentype.NewFace(reg, &opentype.FaceOptions{Size: 18, DPI: 96, Hinting: xfont.HintingFull})
	if err != nil {
		panic(err)
	}
	faceBold, err = opentype.NewFace(bold, &opentype.FaceOptions{Size: 20, DPI: 96, Hinting: xfont.HintingFull})
	if err != nil {
		panic(err)
	}
	faceTitle, err = opentype.NewFace(bold, &opentype.FaceOptions{Size: 28, DPI: 96, Hinting: xfont.HintingFull})
	if err != nil {
		panic(err)
	}
}

func drawText(img *image.RGBA, x, y int, face xfont.Face, c color.Color, s string) {
	d := &xfont.Drawer{
		Dst:  img,
		Src:  image.NewUniform(c),
		Face: face,
		Dot:  fixed.P(x, y),
	}
	d.DrawString(s)
}

func textWidth(face xfont.Face, s string) int {
	d := &xfont.Drawer{Face: face}
	return d.MeasureString(s).Round()
}

// رنگ‌های تم تیره مشابه TradingView
var (
	bgDark     = color.RGBA{0x13, 0x17, 0x22, 0xFF}
	bgCard     = color.RGBA{0x1E, 0x22, 0x2D, 0xFF}
	textBright = color.RGBA{0xE5, 0xE7, 0xEB, 0xFF}
	textMuted  = color.RGBA{0x9C, 0xA3, 0xAF, 0xFF}
	textDim    = color.RGBA{0x6B, 0x72, 0x80, 0xFF}
	greenTV    = color.RGBA{0x26, 0xA6, 0x9A, 0xFF}
	redTV      = color.RGBA{0xEF, 0x53, 0x50, 0xFF}
)

// buildQuickChartReq درخواست POST برای QuickChart می‌سازد — نمودار میله‌ای
// عمودی فقط مثبت که برای هر ارز یک میله نشان می‌دهد: قیمت دلاری ارز در
// آخرین نمونه. میله‌ها از گرون‌ترین به ارزون‌ترین مرتب می‌شوند و رنگ هر میله
// از coinColors می‌آید. محور Y از 0 شروع می‌شود.
func buildQuickChartReq(snap []sample, maxY float64) map[string]interface{} {
	type bar struct {
		symbol string
		price  float64
		color  string
	}
	bars := make([]bar, 0, len(coins))

	last := snap[len(snap)-1]
	for _, c := range coins {
		p, ok := last.prices[c.ID]
		if !ok || p.USD <= 0 {
			continue
		}
		col := coinColors[c.ID]
		bars = append(bars, bar{
			symbol: c.Symbol,
			price:  p.USD,
			color:  fmt.Sprintf("#%02X%02X%02X", col.R, col.G, col.B),
		})
	}
	// مرتب‌سازی نزولی بر اساس قیمت دلاری (گرون‌ترین در سمت چپ)
	sort.SliceStable(bars, func(i, j int) bool {
		return bars[i].price > bars[j].price
	})

	labels := make([]string, len(bars))
	data := make([]float64, len(bars))
	colors := make([]string, len(bars))
	for i, b := range bars {
		labels[i] = b.symbol
		data[i] = b.price
		colors[i] = b.color
	}

	dataset := map[string]interface{}{
		"label":           "USD Price",
		"data":            data,
		"backgroundColor": colors,
		"borderColor":     colors,
		"borderWidth":     0,
		"borderRadius":    8,
	}

	cfg := map[string]interface{}{
		"type": "bar",
		"data": map[string]interface{}{
			"labels":   labels,
			"datasets": []map[string]interface{}{dataset},
		},
		"options": map[string]interface{}{
			"responsive":          false,
			"maintainAspectRatio": false,
			"layout": map[string]interface{}{
				"padding": map[string]interface{}{"top": 60, "right": 30, "left": 10, "bottom": 10},
			},
			"plugins": map[string]interface{}{
				"legend": map[string]interface{}{"display": false},
				"title":  map[string]interface{}{"display": false},
				// formatter به‌صورت placeholder رشته‌ای ست می‌شود و بعد از marshal
				// با تابع JS واقعی جایگزین می‌شود (chart را به‌صورت رشته می‌فرستیم
				// تا QuickChart بتواند تابع را اجرا کند).
				"datalabels": map[string]interface{}{
					"anchor":    "end",
					"align":     "end",
					"clip":      false,
					"color":     "#E5E7EB",
					"font":      map[string]interface{}{"size": 22, "weight": "bold"},
					"formatter": "__FORMATTER__",
				},
			},
			"scales": map[string]interface{}{
				"x": map[string]interface{}{
					"ticks": map[string]interface{}{
						"color":   "#E5E7EB",
						"padding": 8,
						"font":    map[string]interface{}{"size": 26, "weight": "bold"},
					},
					"grid":   map[string]interface{}{"display": false},
					"border": map[string]interface{}{"color": "rgba(255,255,255,0.15)"},
				},
				"y": map[string]interface{}{
					"min":         0,
					"max":         maxY,
					"beginAtZero": true,
					"ticks": map[string]interface{}{
						"color":   "#9CA3AF",
						"padding": 8,
						"font":    map[string]interface{}{"size": 22},
						"format":  map[string]interface{}{"style": "currency", "currency": "USD", "notation": "compact", "maximumFractionDigits": 1},
					},
					"grid": map[string]interface{}{"color": "rgba(255,255,255,0.07)", "drawBorder": false},
				},
			},
		},
	}

	// chart را به‌صورت رشته JS (نه شیء JSON تو در تو) می‌فرستیم تا formatter
	// به‌عنوان تابع واقعی JavaScript توسط QuickChart اجرا شود.
	// formatter: قیمت‌های ≥ ۱۰هزار را به‌صورت "$95k"، ≥ ۱ را با کاما و دو رقم
	// اعشار، و کمتر از یک دلار را با ۶ رقم اعشار نشان می‌دهد.
	cfgBytes, _ := json.Marshal(cfg)
	cfgStr := strings.Replace(string(cfgBytes),
		`"__FORMATTER__"`,
		`function(v){if(v>=10000){var k=Math.round(v/1000);return '$'+k.toString().replace(/\B(?=(\d{3})+(?!\d))/g,',')+'k';}if(v>=1)return '$'+v.toFixed(2).replace(/\B(?=(\d{3})+(?!\d))/g,',');return '$'+v.toFixed(6);}`,
		1)

	return map[string]interface{}{
		"chart":            cfgStr,
		"width":            2560, // ۲x برای کرامیت — بعد در Go با CatmullRom scale می‌شود
		"height":           1080,
		"format":           "png",
		"version":          "3",
		"backgroundColor":  "#131722",
		"devicePixelRatio": 1.0,
	}
}

// fetchQuickChartPNG درخواست را به QuickChart می‌فرستد و PNG را می‌گیرد
func fetchQuickChartPNG(ctx context.Context, client *http.Client, baseURL string, req map[string]interface{}) ([]byte, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+"/chart", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "image/png")

	resp, err := client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("درخواست QuickChart شکست خورد: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		buf, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return nil, fmt.Errorf("QuickChart کد %d: %s", resp.StatusCode, string(buf))
	}
	return io.ReadAll(resp.Body)
}

// renderChartPNG تصویر نهایی PNG را می‌سازد: نمودار QuickChart + لیست قیمت + نرخ دلار
func renderChartPNG(ctx context.Context, client *http.Client, baseURL string, snap []sample, current map[string]priceInfo, usdToman float64, windowLabel string, now time.Time) ([]byte, error) {
	const (
		width     = 1280
		height    = 960
		chartH    = 540
		chartTop  = 70
		pricesTop = chartTop + chartH + 40
		footerTop = height - 60
	)

	canvas := image.NewRGBA(image.Rect(0, 0, width, height))
	draw.Draw(canvas, canvas.Bounds(), &image.Uniform{C: bgDark}, image.Point{}, draw.Src)

	// عنوان بالا
	drawText(canvas, 30, 45, faceTitle, textBright, "Crypto Market — USD Prices")
	tehran, err := time.LoadLocation("Asia/Tehran")
	if err != nil {
		tehran = time.UTC
	}
	subtitle := fmt.Sprintf("Window: %s   |   %s (Tehran)", windowLabel, now.In(tehran).Format("2006-01-02 15:04:05"))
	drawText(canvas, width-30-textWidth(faceRegular, subtitle), 45, faceRegular, textMuted, subtitle)

	// نمودار قیمت دلاری: محور Y از 0 تا بیشترین قیمت در آخرین نمونه + ۱۵٪ padding
	// برای جا دادن لیبل بالای بلندترین میله. فقط نیمه مثبت محور Y استفاده می‌شود.
	maxY := 0.0
	if len(snap) >= 1 {
		last := snap[len(snap)-1]
		for _, p := range last.prices {
			if p.USD > maxY {
				maxY = p.USD
			}
		}
	}
	if maxY <= 0 {
		maxY = 1
	}
	maxY *= 1.15

	chartImgRect := image.Rect(0, chartTop, width, chartTop+chartH)

	if len(snap) >= 2 {
		qcReq := buildQuickChartReq(snap, maxY)
		pngBytes, err := fetchQuickChartPNG(ctx, client, baseURL, qcReq)
		if err != nil {
			return nil, fmt.Errorf("رندر QuickChart: %w", err)
		}
		chartImg, err := png.Decode(bytes.NewReader(pngBytes))
		if err != nil {
			return nil, fmt.Errorf("decode QuickChart PNG: %w", err)
		}
		// scale با کیفیت بالا از 2560x1080 به اندازه ناحیه چارت
		xdraw.CatmullRom.Scale(canvas, chartImgRect, chartImg, chartImg.Bounds(), draw.Over, nil)
	} else {
		drawText(canvas, width/2-160, chartTop+chartH/2, faceBold, textMuted,
			"در حال جمع‌آوری داده برای نمودار...")
	}

	// لیست قیمت در دو ستون با کارت تیره
	draw.Draw(canvas, image.Rect(20, pricesTop-30, width-20, pricesTop+220), &image.Uniform{C: bgCard}, image.Point{}, draw.Src)

	type row struct {
		coin   Coin
		price  float64
		change float64
		ok     bool
	}
	rows := make([]row, 0, len(coins))
	for _, c := range coins {
		p, ok := current[c.ID]
		rows = append(rows, row{coin: c, price: p.USD, change: p.Change24h, ok: ok})
	}
	sort.SliceStable(rows, func(i, j int) bool {
		if rows[i].ok != rows[j].ok {
			return rows[i].ok
		}
		return rows[i].price > rows[j].price
	})

	colCount := 2
	rowsPerCol := (len(rows) + colCount - 1) / colCount
	colW := (width - 60) / colCount
	rowH := 34

	for i, r := range rows {
		col := i / rowsPerCol
		rowIdx := i % rowsPerCol
		x := 40 + col*colW
		y := pricesTop + rowIdx*rowH

		swatch := coinColors[r.coin.ID]
		draw.Draw(canvas,
			image.Rect(x, y-14, x+16, y+2),
			&image.Uniform{C: swatch},
			image.Point{}, draw.Src)

		drawText(canvas, x+26, y, faceBold, textBright, r.coin.Symbol)

		var priceStr string
		if r.ok {
			priceStr = "$" + formatPrice(r.price)
		} else {
			priceStr = "n/a"
		}
		drawText(canvas, x+135, y, faceRegular, textBright, priceStr)

		if r.ok {
			changeStr := fmt.Sprintf("%+.2f%%", r.change)
			cc := greenTV
			if r.change < 0 {
				cc = redTV
			}
			drawText(canvas, x+colW-110, y, faceBold, cc, changeStr)
		}
	}

	// فوتر: نرخ دلار و منابع
	var footer string
	if usdToman > 0 {
		footer = fmt.Sprintf("IRR   1 USD  ≈  %s Toman", addThousandsSep(fmt.Sprintf("%.0f", usdToman)))
	} else {
		footer = "IRR   USD → Toman: unavailable"
	}
	fw := textWidth(faceBold, footer)
	drawText(canvas, (width-fw)/2, footerTop, faceBold, textBright, footer)

	credit := "Sources: CoinGecko · Hyperliquid · Bonbast"
	cw := textWidth(faceRegular, credit)
	drawText(canvas, (width-cw)/2, footerTop+26, faceRegular, textDim, credit)

	var out bytes.Buffer
	if err := png.Encode(&out, canvas); err != nil {
		return nil, fmt.Errorf("encode PNG شکست خورد: %w", err)
	}
	return out.Bytes(), nil
}

// runCycle یک چرخه کامل: دریافت قیمت + ارسال پیام متنی + ثبت در history
func runCycle(ctx context.Context, client *http.Client, cfg *Config, hist *history, rates *ratesCache) {
	// timeout مستقل برای هر چرخه تا اگر شبکه کند بود، چرخه بعدی قفل نشود
	cycleCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	prices, err := fetchPrices(cycleCtx, client)
	if err != nil {
		log.Printf("❌ خطای دریافت قیمت: %v", err)
		return
	}

	// قیمت WTI اختیاری است؛ اگر گرفته نشد فقط آن خط در پیام رد می‌شود
	if wti, err := fetchWTIPerp(cycleCtx, client); err != nil {
		log.Printf("⚠️ خطای دریافت قیمت WTI: %v", err)
	} else {
		prices["wti-perp"] = wti
	}

	// ثبت نمونه در history برای استفاده در نمودار
	recordSample(hist, prices)

	// قیمت دلار اختیاری است؛ اگر شکست خورد پیام را بدون آن می‌فرستیم
	usdToman, err := fetchUSDInToman(cycleCtx, client)
	if err != nil {
		log.Printf("⚠️ خطای دریافت قیمت دلار: %v", err)
		usdToman = 0
	} else if rates != nil && usdToman > 0 {
		rates.set(usdToman)
	}

	msg := formatMessage(prices, usdToman)

	if err := sendToTelegram(cycleCtx, client, cfg, msg, convertButtonMarkup(cfg)); err != nil {
		log.Printf("❌ خطای ارسال به تلگرام: %v", err)
		return
	}

	log.Printf("✅ پیام ارسال شد - تعداد ارز: %d", len(prices))
}

// runChartCycle یک چرخه: رندر نمودار + ارسال عکس. هیچ درخواست API نمی‌زند —
// هم قیمت‌ها و درصد ۲۴ ساعته از آخرین نمونه history می‌آید و هم نرخ دلار از
// ratesCache. fetcher واحد (runCycle) مسئول تازه نگه داشتن این‌هاست.
func runChartCycle(ctx context.Context, client *http.Client, cfg *Config, hist *history, rates *ratesCache) {
	cycleCtx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()

	snap := hist.snapshot(cfg.ChartWindowDur)
	if len(snap) < 2 {
		log.Printf("ℹ️ داده کافی برای نمودار جمع نشده (%d نمونه) — صبر می‌کنیم", len(snap))
		return
	}

	// قیمت‌های فعلی + درصد ۲۴ ساعته از آخرین نمونه history (هر دو در RAM)
	last := snap[len(snap)-1]
	current := make(map[string]priceInfo, len(last.prices))
	for id, p := range last.prices {
		current[id] = p
	}

	usdToman, _ := rates.get()

	pngBytes, err := renderChartPNG(cycleCtx, client, cfg.QuickChartURL, snap, current, usdToman, cfg.ChartWindowRaw, time.Now())
	if err != nil {
		log.Printf("❌ خطای ساخت نمودار: %v", err)
		return
	}

	caption := fmt.Sprintf("📈 *Crypto Chart* — `%s` window", cfg.ChartWindowRaw)
	if err := sendPhoto(cycleCtx, client, cfg, pngBytes, caption); err != nil {
		log.Printf("❌ خطای ارسال عکس نمودار: %v", err)
		return
	}
	log.Printf("🖼️ نمودار ارسال شد — %d نمونه در پنجره", len(snap))
}

// ─── مبدل ارز ─────────────────────────────────────────────────────────────

// currencyAlias نگاشت ورودی کاربر (لاتین/فارسی، حروف کوچک) به نماد استاندارد.
// در init برای هر coin، Symbol آن هم اضافه می‌شود.
var currencyAlias = map[string]string{
	"usd": "USD", "dollar": "USD", "دلار": "USD",
	"irr": "IRR", "rial": "IRR", "ریال": "IRR",
	"toman": "TMN", "tmn": "TMN", "irt": "TMN", "تومان": "TMN",
	"btc": "BTC", "bitcoin": "BTC", "بیتکوین": "BTC",
	"eth": "ETH", "ethereum": "ETH", "اتریوم": "ETH",
	"usdt": "USDT", "tether": "USDT", "تتر": "USDT",
	"bnb": "BNB",
	"xrp": "XRP", "ripple": "XRP",
	"sol": "SOL", "solana": "SOL",
	"doge": "DOGE", "dogecoin": "DOGE",
	"xaut": "XAUT",
	"paxg": "PAXG",
	"slv":  "SLVON", "slvon": "SLVON", "نقره": "SLVON",
	"wti": "WTI", "oil": "WTI", "نفت": "WTI",
}

// symToID نگاشت Symbol (مثل BTC) به CoinGecko ID (مثل bitcoin).
var symToID = map[string]string{}

func init() {
	for _, c := range coins {
		symToID[c.Symbol] = c.ID
		currencyAlias[strings.ToLower(c.Symbol)] = c.Symbol
	}
}

// normalizeDigits ارقام فارسی (۰-۹) و عربی (٠-٩) را به ASCII تبدیل می‌کند
func normalizeDigits(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch {
		case r >= '۰' && r <= '۹':
			b.WriteRune(r - '۰' + '0')
		case r >= '٠' && r <= '٩':
			b.WriteRune(r - '٠' + '0')
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// parseConversion ورودی متنی را به (مقدار، نماد مبدأ، نماد مقصد) تبدیل می‌کند.
// نمونه‌های پشتیبانی‌شده: "100 usdt irr"، "100 usdt to irr"، "2.5 btc → toman"،
// "5,000,000 toman btc"، "۱۰۰ usd toman".
func parseConversion(text string) (amount float64, fromSym, toSym string, ok bool) {
	s := normalizeDigits(text)
	s = strings.ToLower(s)
	// کاما و آندرسکور را از داخل عدد حذف کن (نه با space جایگزین کن، چون توکن می‌شکند)
	s = strings.NewReplacer(",", "", "_", "").Replace(s)
	// کانکتورهای جهت‌دار را با space جایگزین کن
	s = strings.NewReplacer("→", " ", "←", " ", "⇒", " ", "=", " ", ">", " ", "<", " ").Replace(s)
	tokens := strings.Fields(s)
	filtered := make([]string, 0, len(tokens))
	for _, t := range tokens {
		if t == "to" || t == "in" || t == "be" {
			continue
		}
		filtered = append(filtered, t)
	}
	if len(filtered) != 3 {
		return 0, "", "", false
	}
	amt, err := strconv.ParseFloat(filtered[0], 64)
	if err != nil || amt <= 0 {
		return 0, "", "", false
	}
	from, ok1 := currencyAlias[filtered[1]]
	to, ok2 := currencyAlias[filtered[2]]
	if !ok1 || !ok2 {
		return 0, "", "", false
	}
	return amt, from, to, true
}

// usdPer قیمت USD برای یک واحد از نماد می‌دهد.
// USD → 1، TMN/IRR → از کَش نرخ، ارزهای ردیابی‌شده → آخرین نمونه history،
// بقیه → live cache یا fetch تازه از CoinGecko.
func usdPer(ctx context.Context, sym string, deps *convDeps) (float64, error) {
	switch sym {
	case "USD":
		return 1.0, nil
	case "TMN":
		v, _ := deps.rates.get()
		if v <= 0 {
			return 0, fmt.Errorf("نرخ دلار به تومان هنوز آماده نیست — چند ثانیه دیگر امتحان کنید")
		}
		return 1.0 / v, nil
	case "IRR":
		v, _ := deps.rates.get()
		if v <= 0 {
			return 0, fmt.Errorf("نرخ دلار به تومان هنوز آماده نیست — چند ثانیه دیگر امتحان کنید")
		}
		return 1.0 / (v * 10), nil
	default:
		id, ok := symToID[sym]
		if !ok {
			return 0, fmt.Errorf("ارز %s پشتیبانی نمی‌شود", sym)
		}
		// مرحله ۱: ارزهای ردیابی‌شده مستقیماً از history (رایگان، فوری)
		snap := deps.hist.snapshot(0)
		if len(snap) > 0 {
			if p, ok := snap[len(snap)-1].prices[id]; ok && p.USD > 0 {
				return p.USD, nil
			}
		}
		// مرحله ۲: live cache (تا ۶۰ ثانیه)
		if p, ok := deps.live.lookup(id); ok {
			return p, nil
		}
		// مرحله ۳: fetch تازه از CoinGecko با timeout مستقل
		liveCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
		defer cancel()
		p, err := fetchLivePrice(liveCtx, deps.client, id)
		if err != nil {
			return 0, fmt.Errorf("قیمت %s در دسترس نیست", sym)
		}
		deps.live.store(id, p)
		return p, nil
	}
}

func convert(ctx context.Context, amount float64, fromSym, toSym string, deps *convDeps) (result, rate float64, err error) {
	fromUSD, err := usdPer(ctx, fromSym, deps)
	if err != nil {
		return 0, 0, err
	}
	toUSD, err := usdPer(ctx, toSym, deps)
	if err != nil {
		return 0, 0, err
	}
	rate = fromUSD / toUSD
	result = amount * rate
	return result, rate, nil
}

// formatAmount عدد را با دقت مناسب نماد فرمت می‌کند
func formatAmount(v float64, sym string) string {
	switch sym {
	case "USD":
		return addThousandsSep(fmt.Sprintf("%.2f", v))
	case "IRR", "TMN":
		return addThousandsSep(fmt.Sprintf("%.0f", v))
	default:
		var s string
		if v >= 1 {
			s = fmt.Sprintf("%.4f", v)
		} else {
			s = fmt.Sprintf("%.8f", v)
		}
		if strings.Contains(s, ".") {
			s = strings.TrimRight(s, "0")
			s = strings.TrimRight(s, ".")
		}
		return addThousandsSep(s)
	}
}

func formatConvertReply(amount float64, fromSym string, result float64, toSym string, rate float64) string {
	return fmt.Sprintf(
		"🔁 *تبدیل ارز*\n`%s %s`  →  `%s %s`\n\nنرخ: `1 %s ≈ %s %s`",
		formatAmount(amount, fromSym), fromSym,
		formatAmount(result, toSym), toSym,
		fromSym,
		formatAmount(rate, toSym), toSym,
	)
}

const welcomeMessage = "سلام 👋\nهرچیزی را به هرچیزی تبدیل کن — کافیست متنش را بفرستی:\n\n" +
	"`100 usdt irr`\n`2.5 btc toman`\n`5,000,000 toman btc`\n`100 trx usd`\n`50 pol usdt`\n\n" +
	"از ۲۵۰ ارز برتر بازار پشتیبانی می‌شود (btc, eth, usdt, trx, pol, ada, sol, doge, …)\n" +
	"فیات: usd, irr (ریال), tmn (تومان)"

const usageHint = "متوجه نشدم 🤔 یک نمونه‌ی درست:\n\n" +
	"`100 usdt irr`\n`2.5 btc toman`\n`100 trx usd`\n`50 pol usdt`\n\n" +
	"از ۲۵۰ ارز برتر بازار پشتیبانی می‌شود.\n" +
	"فیات: usd, irr (ریال), tmn (تومان)"

func quickKeyboardMarkup() string {
	markup := map[string]any{
		"inline_keyboard": [][]map[string]string{{
			{"text": "1 USDT → IRR", "callback_data": "conv:1:USDT:IRR"},
			{"text": "1 BTC → IRR", "callback_data": "conv:1:BTC:IRR"},
			{"text": "1 BTC → USDT", "callback_data": "conv:1:BTC:USDT"},
		}},
	}
	b, _ := json.Marshal(markup)
	return string(b)
}

// tgUpdate شکل ساده‌شده‌ی آپدیت تلگرام — فقط فیلدهایی که نیاز داریم.
type tgUpdate struct {
	UpdateID int64 `json:"update_id"`
	Message  *struct {
		Chat struct {
			ID int64 `json:"id"`
		} `json:"chat"`
		Text string `json:"text"`
	} `json:"message"`
	CallbackQuery *struct {
		ID      string `json:"id"`
		Data    string `json:"data"`
		Message struct {
			Chat struct {
				ID int64 `json:"id"`
			} `json:"chat"`
		} `json:"message"`
	} `json:"callback_query"`
}

// runUpdatesLoop با long-polling آپدیت‌ها را می‌گیرد و پاسخ تبدیل می‌فرستد.
// خطاها فقط لاگ می‌شوند و حلقه ادامه پیدا می‌کند.
func runUpdatesLoop(ctx context.Context, cfg *Config, deps *convDeps) {
	poll := &http.Client{Timeout: 40 * time.Second}
	var offset int64

	for {
		if ctx.Err() != nil {
			return
		}

		endpoint := fmt.Sprintf(
			"https://api.telegram.org/bot%s/getUpdates?timeout=30&offset=%d",
			cfg.BotToken, offset,
		)
		reqCtx, cancel := context.WithTimeout(ctx, 35*time.Second)
		req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, endpoint, nil)
		if err != nil {
			cancel()
			log.Printf("⚠️ ساخت درخواست getUpdates شکست خورد: %v", err)
			time.Sleep(2 * time.Second)
			continue
		}
		resp, err := poll.Do(req)
		if err != nil {
			cancel()
			if ctx.Err() != nil {
				return
			}
			log.Printf("⚠️ خطای getUpdates: %v", err)
			time.Sleep(2 * time.Second)
			continue
		}
		if resp.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
			resp.Body.Close()
			cancel()
			log.Printf("⚠️ getUpdates کد %d: %s", resp.StatusCode, string(body))
			time.Sleep(2 * time.Second)
			continue
		}
		var data struct {
			Ok     bool       `json:"ok"`
			Result []tgUpdate `json:"result"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
			resp.Body.Close()
			cancel()
			log.Printf("⚠️ پارس پاسخ getUpdates شکست خورد: %v", err)
			time.Sleep(2 * time.Second)
			continue
		}
		resp.Body.Close()
		cancel()

		for _, u := range data.Result {
			if u.UpdateID >= offset {
				offset = u.UpdateID + 1
			}
			handleUpdate(ctx, cfg, deps, u)
		}
	}
}

func handleUpdate(ctx context.Context, cfg *Config, deps *convDeps, u tgUpdate) {
	if u.CallbackQuery != nil {
		cb := u.CallbackQuery
		defer func() {
			ackCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
			defer cancel()
			if err := answerCallback(ackCtx, deps.client, cfg, cb.ID); err != nil {
				log.Printf("⚠️ answerCallback: %v", err)
			}
		}()
		parts := strings.Split(cb.Data, ":")
		if len(parts) != 4 || parts[0] != "conv" {
			return
		}
		amt, err := strconv.ParseFloat(parts[1], 64)
		if err != nil || amt <= 0 {
			return
		}
		from, to := parts[2], parts[3]
		replyCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
		defer cancel()
		result, rate, err := convert(replyCtx, amt, from, to, deps)
		var text string
		if err != nil {
			text = "❌ " + err.Error()
		} else {
			text = formatConvertReply(amt, from, result, to, rate)
		}
		if err := sendPrivate(replyCtx, deps.client, cfg, cb.Message.Chat.ID, text, ""); err != nil {
			log.Printf("⚠️ sendPrivate (callback): %v", err)
		}
		return
	}

	if u.Message == nil {
		return
	}
	text := strings.TrimSpace(u.Message.Text)
	if text == "" {
		return
	}
	chatID := u.Message.Chat.ID

	replyCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	if strings.HasPrefix(text, "/start") {
		if err := sendPrivate(replyCtx, deps.client, cfg, chatID, welcomeMessage, quickKeyboardMarkup()); err != nil {
			log.Printf("⚠️ sendPrivate (/start): %v", err)
		}
		return
	}

	amount, from, to, ok := parseConversion(text)
	if !ok {
		if err := sendPrivate(replyCtx, deps.client, cfg, chatID, usageHint, ""); err != nil {
			log.Printf("⚠️ sendPrivate (usage): %v", err)
		}
		return
	}
	result, rate, err := convert(replyCtx, amount, from, to, deps)
	var reply string
	if err != nil {
		reply = "❌ " + err.Error()
	} else {
		reply = formatConvertReply(amount, from, result, to, rate)
	}
	if err := sendPrivate(replyCtx, deps.client, cfg, chatID, reply, ""); err != nil {
		log.Printf("⚠️ sendPrivate (conv): %v", err)
	}
}

func main() {
	cfg, err := loadConfig()
	if err != nil {
		log.Fatalf("خطای تنظیمات: %v", err)
	}

	client := &http.Client{Timeout: 20 * time.Second}
	hist := &history{maxAge: cfg.ChartWindowDur}
	rates := &ratesCache{}
	live := &livePriceCache{ttl: 5 * time.Minute}
	deps := &convDeps{client: client, hist: hist, rates: rates, live: live}

	// graceful shutdown با Ctrl+C یا SIGTERM
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	log.Printf("🚀 ربات شروع به کار کرد — بازه fetch/متن: %s — بازه نمودار: %s — پنجره: %s — کانال: %s",
		cfg.Interval, cfg.ChartInterval, cfg.ChartWindowRaw, cfg.ChannelID)

	// ایندکس ارزها از CoinGecko (۲۵۰ ارز برتر بازار) برای اینکه مبدل بتواند
	// هر ارز معتبری را پشتیبانی کند، نه فقط ارزهای ردیابی‌شده. شکست خوردنش
	// بحرانی نیست — فقط ارزهای داخل coins کار می‌کنند.
	bootCtx, bootCancel := context.WithTimeout(ctx, 15*time.Second)
	if n, err := loadCoinIndex(bootCtx, client); err != nil {
		log.Printf("⚠️ بارگذاری ایندکس ارزها شکست خورد — فقط ارزهای ردیابی‌شده در دسترس مبدل خواهند بود: %v", err)
	} else {
		log.Printf("📖 ایندکس ارزها بارگذاری شد — %d ارز اضافی برای مبدل", n)
	}
	bootCancel()

	// اولین چرخه متن را بلافاصله بفرست (هم history را پر می‌کند هم پیام را)
	runCycle(ctx, client, cfg, hist, rates)

	// goroutine جدا برای ارسال عکس نمودار — هیچ درخواست API نمی‌زند،
	// فقط از history و ratesCache که توسط runCycle پر می‌شوند، می‌خواند.
	go func() {
		tk := time.NewTicker(cfg.ChartInterval)
		defer tk.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-tk.C:
				runChartCycle(ctx, client, cfg, hist, rates)
			}
		}
	}()

	// goroutine مستقل برای دریافت پیام‌های خصوصی و پاسخ تبدیل ارز
	go runUpdatesLoop(ctx, cfg, deps)

	ticker := time.NewTicker(cfg.Interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			log.Println("🛑 سیگنال خاتمه دریافت شد، خروج تمیز...")
			return
		case <-ticker.C:
			runCycle(ctx, client, cfg, hist, rates)
		}
	}
}
