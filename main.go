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
	"math"
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
	{ID: "dogecoin", Symbol: "DOGE", Name: "Dogecoin", Emoji: "🐕"},
}

// پالت رنگ هر ارز برای استفاده هم در خطوط نمودار و هم در مربع کنار قیمت‌ها
var coinColors = map[string]color.RGBA{
	"bitcoin":     {247, 147, 26, 255},
	"tether-gold": {212, 175, 55, 255},
	"pax-gold":    {255, 193, 37, 255},
	"ishares-silver-trust-ondo-tokenized-stock": {130, 130, 140, 255},
	"wti-perp":     {51, 51, 51, 255},
	"ethereum":     {98, 126, 234, 255},
	"tether":       {38, 161, 123, 255},
	"binancecoin":  {243, 186, 47, 255},
	"ripple":       {35, 41, 47, 255},
	"solana":       {153, 69, 255, 255},
	"dogecoin":     {186, 160, 82, 255},
}

type Config struct {
	BotToken       string        // توکن گرفته شده از BotFather
	ChannelID      string        // @yourchannel یا -100xxxxxxxxx
	Interval       time.Duration // فاصله ارسال پیام متنی - پیش‌فرض ۱ دقیقه
	ChartInterval  time.Duration // فاصله ارسال عکس نمودار - پیش‌فرض ۵ دقیقه
	ChartWindowDur time.Duration // پنجره نمایش روی نمودار. 0 یعنی session
	ChartWindowRaw string        // مقدار خام برای نمایش روی عکس
	SampleInterval time.Duration // فاصله نمونه‌گیری مستقل از تیکر متن - پیش‌فرض 20s
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

	sampleInterval := 20 * time.Second
	if v := os.Getenv("SAMPLE_INTERVAL"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil {
			return nil, fmt.Errorf("مقدار SAMPLE_INTERVAL نامعتبر است: %w", err)
		}
		sampleInterval = d
	}

	quickChart := strings.TrimRight(os.Getenv("QUICKCHART_URL"), "/")
	if quickChart == "" {
		quickChart = "https://quickchart.io"
	}

	return &Config{
		BotToken:       token,
		ChannelID:      channel,
		Interval:       interval,
		ChartInterval:  chartInterval,
		ChartWindowDur: windowDur,
		ChartWindowRaw: windowRaw,
		SampleInterval: sampleInterval,
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
	b.WriteString("📊 *Live Crypto Prices*\n")
	b.WriteString("━━━━━━━━━━━━━━━━━━━━\n\n")

	for _, c := range coins {
		p, ok := prices[c.ID]
		if !ok {
			continue
		}
		sign := "🟢 ▲"
		if p.Change24h < 0 {
			sign = "🔴 ▼"
		}
		fmt.Fprintf(&b,
			"%s *%s* `%s`\n   `$%s`  %s `%+.2f%%`\n\n",
			c.Emoji, c.Name, c.Symbol,
			formatPrice(p.USD), sign, p.Change24h,
		)
	}

	b.WriteString("━━━━━━━━━━━━━━━━━━━━\n")
	if usdToman > 0 {
		fmt.Fprintf(&b,
			"🇮🇷 *Iranian Rial* `IRR`\n   `1 USD ≈ %s Toman`\n\n",
			addThousandsSep(fmt.Sprintf("%.0f", usdToman)),
		)
	} else {
		b.WriteString("🇮🇷 *Iranian Rial* `IRR`\n   _unavailable_\n\n")
	}

	// زمان به وقت تهران
	loc, err := time.LoadLocation("Asia/Tehran")
	if err != nil {
		loc = time.UTC
	}
	fmt.Fprintf(&b,
		"🕐 %s (Tehran)\n_Sources: CoinGecko · Nobitex_",
		time.Now().In(loc).Format("2006-01-02 15:04:05"),
	)
	return b.String()
}

// sendToTelegram پیام را به کانال می‌فرستد
func sendToTelegram(ctx context.Context, client *http.Client, cfg *Config, text string) error {
	api := fmt.Sprintf("https://api.telegram.org/bot%s/sendMessage", cfg.BotToken)

	form := url.Values{}
	form.Set("chat_id", cfg.ChannelID)
	form.Set("text", text)
	form.Set("parse_mode", "Markdown")
	form.Set("disable_web_page_preview", "true")

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

// sample یک نمونه از قیمت‌های همه ارزها در یک لحظه
type sample struct {
	t      time.Time
	prices map[string]float64 // coin ID -> USD
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
	ps := make(map[string]float64, len(prices))
	for id, p := range prices {
		ps[id] = p.USD
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
	bgDark      = color.RGBA{0x13, 0x17, 0x22, 0xFF}
	bgCard      = color.RGBA{0x1E, 0x22, 0x2D, 0xFF}
	textBright  = color.RGBA{0xE5, 0xE7, 0xEB, 0xFF}
	textMuted   = color.RGBA{0x9C, 0xA3, 0xAF, 0xFF}
	textDim     = color.RGBA{0x6B, 0x72, 0x80, 0xFF}
	greenTV     = color.RGBA{0x26, 0xA6, 0x9A, 0xFF}
	redTV       = color.RGBA{0xEF, 0x53, 0x50, 0xFF}
)

// quickChartDataset یک سری داده برای Chart.js
type quickChartDataset struct {
	Label           string    `json:"label"`
	Data            []float64 `json:"data"`
	BorderColor     string    `json:"borderColor"`
	BackgroundColor string    `json:"backgroundColor"`
	Tension         float64   `json:"tension"`
	BorderWidth     float64   `json:"borderWidth"`
	PointRadius     int       `json:"pointRadius"`
	Fill            bool      `json:"fill"`
	CubicInterpMode string    `json:"cubicInterpolationMode"`
}

// buildQuickChartReq کانفیگ POST برای QuickChart می‌سازد
func buildQuickChartReq(snap []sample, xFormat string, minY, maxY float64) map[string]interface{} {
	labels := make([]string, len(snap))
	for i, s := range snap {
		labels[i] = s.t.Format(xFormat)
	}

	datasets := []quickChartDataset{}
	for _, c := range coins {
		var base float64
		ys := make([]float64, 0, len(snap))
		has := false
		for _, s := range snap {
			p, ok := s.prices[c.ID]
			if !ok || p <= 0 {
				ys = append(ys, math.NaN())
				continue
			}
			if base == 0 {
				base = p
			}
			ys = append(ys, (p/base-1)*100)
			has = true
		}
		if !has {
			continue
		}
		col := coinColors[c.ID]
		hex := fmt.Sprintf("#%02X%02X%02X", col.R, col.G, col.B)
		datasets = append(datasets, quickChartDataset{
			Label:           c.Symbol,
			Data:            ys,
			BorderColor:     hex,
			BackgroundColor: "transparent",
			Tension:         0.35,
			BorderWidth:     5.0,
			PointRadius:     0,
			Fill:            false,
			CubicInterpMode: "monotone",
		})
	}

	cfg := map[string]interface{}{
		"type": "line",
		"data": map[string]interface{}{
			"labels":   labels,
			"datasets": datasets,
		},
		"options": map[string]interface{}{
			"responsive":          false,
			"maintainAspectRatio": false,
			"interaction":         map[string]interface{}{"intersect": false},
			"plugins": map[string]interface{}{
				"legend": map[string]interface{}{
					"position": "top",
					"align":    "center",
					"labels": map[string]interface{}{
						"color":      "#E5E7EB",
						"boxWidth":   36,
						"boxHeight":  6,
						"padding":    18,
						"usePointStyle": false,
						"font":       map[string]interface{}{"size": 26, "weight": "bold"},
					},
				},
				"title": map[string]interface{}{"display": false},
			},
			"scales": map[string]interface{}{
				"x": map[string]interface{}{
					"ticks": map[string]interface{}{
						"color":         "#9CA3AF",
						"maxRotation":   0,
						"autoSkip":      true,
						"maxTicksLimit": 8,
						"padding":       8,
						"font":          map[string]interface{}{"size": 22},
					},
					"grid": map[string]interface{}{"color": "rgba(255,255,255,0.05)", "drawBorder": false},
				},
				"y": map[string]interface{}{
					"min": minY,
					"max": maxY,
					"ticks": map[string]interface{}{
						"color":   "#9CA3AF",
						"padding": 8,
						"font":    map[string]interface{}{"size": 22},
						"format":  map[string]interface{}{"style": "decimal", "minimumFractionDigits": 2, "maximumFractionDigits": 2, "signDisplay": "exceptZero"},
					},
					"grid": map[string]interface{}{"color": "rgba(255,255,255,0.07)", "drawBorder": false},
				},
			},
		},
	}

	return map[string]interface{}{
		"chart":            cfg,
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
	drawText(canvas, 30, 45, faceTitle, textBright, "Crypto Market — % Change")
	tehran, err := time.LoadLocation("Asia/Tehran")
	if err != nil {
		tehran = time.UTC
	}
	subtitle := fmt.Sprintf("Window: %s   |   %s (Tehran)", windowLabel, now.In(tehran).Format("2006-01-02 15:04:05"))
	drawText(canvas, width-30-textWidth(faceRegular, subtitle), 45, faceRegular, textMuted, subtitle)

	// محاسبه min/max و فرمت محور X
	minY, maxY := math.Inf(1), math.Inf(-1)
	if len(snap) >= 2 {
		for _, c := range coins {
			var base float64
			for _, s := range snap {
				p, ok := s.prices[c.ID]
				if !ok || p <= 0 {
					continue
				}
				if base == 0 {
					base = p
				}
				yv := (p/base - 1) * 100
				if yv < minY {
					minY = yv
				}
				if yv > maxY {
					maxY = yv
				}
			}
		}
	}
	if math.IsInf(minY, 1) {
		minY, maxY = -0.5, 0.5
	}
	span := maxY - minY
	if span < 1.0 {
		mid := (minY + maxY) / 2
		minY = mid - 0.5
		maxY = mid + 0.5
	} else {
		minY -= span * 0.1
		maxY += span * 0.1
	}

	xFormat := "01-02 15:04"
	if len(snap) >= 2 {
		dur := snap[len(snap)-1].t.Sub(snap[0].t)
		switch {
		case dur < 10*time.Minute:
			xFormat = "15:04:05"
		case dur < 24*time.Hour:
			xFormat = "15:04"
		}
	}

	chartImgRect := image.Rect(0, chartTop, width, chartTop+chartH)

	if len(snap) >= 2 {
		qcReq := buildQuickChartReq(snap, xFormat, minY, maxY)
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
func runCycle(ctx context.Context, client *http.Client, cfg *Config, hist *history) {
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
	}

	msg := formatMessage(prices, usdToman)

	if err := sendToTelegram(cycleCtx, client, cfg, msg); err != nil {
		log.Printf("❌ خطای ارسال به تلگرام: %v", err)
		return
	}

	log.Printf("✅ پیام ارسال شد - تعداد ارز: %d", len(prices))
}

// runChartCycle یک چرخه: گرفتن قیمت دلار + رندر نمودار + ارسال عکس
func runChartCycle(ctx context.Context, client *http.Client, cfg *Config, hist *history) {
	cycleCtx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()

	snap := hist.snapshot(cfg.ChartWindowDur)
	if len(snap) < 2 {
		log.Printf("ℹ️ داده کافی برای نمودار جمع نشده (%d نمونه) — صبر می‌کنیم", len(snap))
		return
	}

	// قیمت‌های فعلی برای لیست زیر نمودار از آخرین نمونه
	last := snap[len(snap)-1]
	current := make(map[string]priceInfo, len(last.prices))
	for id, p := range last.prices {
		current[id] = priceInfo{USD: p}
	}
	// 24h change از یک fetch تازه (اختیاری — اگر شکست خورد فقط درصد را نشان نمی‌دهیم)
	if fresh, err := fetchPrices(cycleCtx, client); err == nil {
		if wti, werr := fetchWTIPerp(cycleCtx, client); werr == nil {
			fresh["wti-perp"] = wti
		}
		for id, p := range fresh {
			cur := current[id]
			cur.Change24h = p.Change24h
			if cur.USD == 0 {
				cur.USD = p.USD
			}
			current[id] = cur
		}
	}

	usdToman, err := fetchUSDInToman(cycleCtx, client)
	if err != nil {
		log.Printf("⚠️ خطای دریافت دلار برای نمودار: %v", err)
		usdToman = 0
	}

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

func main() {
	cfg, err := loadConfig()
	if err != nil {
		log.Fatalf("خطای تنظیمات: %v", err)
	}

	client := &http.Client{Timeout: 20 * time.Second}
	hist := &history{maxAge: cfg.ChartWindowDur}

	// graceful shutdown با Ctrl+C یا SIGTERM
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	log.Printf("🚀 ربات شروع به کار کرد — بازه متن: %s — نمونه‌گیری: %s — بازه نمودار: %s — پنجره: %s — کانال: %s",
		cfg.Interval, cfg.SampleInterval, cfg.ChartInterval, cfg.ChartWindowRaw, cfg.ChannelID)

	// اولین چرخه متن را بلافاصله بفرست (هم history را پر می‌کند هم پیام را)
	runCycle(ctx, client, cfg, hist)

	// goroutine مستقل برای نمونه‌گیری سریع (بدون ارسال) — تا نمودار داده کافی داشته باشد
	go func() {
		tk := time.NewTicker(cfg.SampleInterval)
		defer tk.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-tk.C:
				sampleCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
				prices, err := fetchPrices(sampleCtx, client)
				if err != nil {
					log.Printf("⚠️ نمونه‌گیری شکست خورد: %v", err)
					cancel()
					continue
				}
				if wti, werr := fetchWTIPerp(sampleCtx, client); werr == nil {
					prices["wti-perp"] = wti
				}
				recordSample(hist, prices)
				cancel()
			}
		}
	}()

	// goroutine جدا برای ارسال عکس نمودار
	go func() {
		tk := time.NewTicker(cfg.ChartInterval)
		defer tk.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-tk.C:
				runChartCycle(ctx, client, cfg, hist)
			}
		}
	}()

	ticker := time.NewTicker(cfg.Interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			log.Println("🛑 سیگنال خاتمه دریافت شد، خروج تمیز...")
			return
		case <-ticker.C:
			runCycle(ctx, client, cfg, hist)
		}
	}
}
