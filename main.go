package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"
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
	{ID: "ethereum", Symbol: "ETH", Name: "Ethereum", Emoji: "🔷"},
	{ID: "tether", Symbol: "USDT", Name: "Tether", Emoji: "💵"},
	{ID: "binancecoin", Symbol: "BNB", Name: "BNB", Emoji: "🟡"},
	{ID: "ripple", Symbol: "XRP", Name: "Ripple", Emoji: "🔵"},
	{ID: "solana", Symbol: "SOL", Name: "Solana", Emoji: "🟣"},
	{ID: "dogecoin", Symbol: "DOGE", Name: "Dogecoin", Emoji: "🐕"},
}

type Config struct {
	BotToken  string        // توکن گرفته شده از BotFather
	ChannelID string        // @yourchannel یا -100xxxxxxxxx
	Interval  time.Duration // فاصله ارسال - پیش‌فرض 1 دقیقه
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

	return &Config{
		BotToken:  token,
		ChannelID: channel,
		Interval:  interval,
	}, nil
}

// ساختار پاسخ CoinGecko برای هر ارز
type priceInfo struct {
	USD       float64 `json:"usd"`
	Change24h float64 `json:"usd_24h_change"`
}

// fetchPrices قیمت همه ارزها را با یک درخواست از CoinGecko می‌گیرد
func fetchPrices(ctx context.Context, client *http.Client) (map[string]priceInfo, error) {
	ids := make([]string, len(coins))
	for i, c := range coins {
		ids[i] = c.ID
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

// fetchUSDInToman قیمت دلار آمریکا به تومان را از Nobitex (نرخ USDT/IRT)
// به‌عنوان معیار بازار آزاد ایران می‌گیرد. پاسخ Nobitex بر حسب ریال است؛
// با تقسیم بر ۱۰ به تومان تبدیل می‌کنیم.
func fetchUSDInToman(ctx context.Context, client *http.Client) (float64, error) {
	const endpoint = "https://api.nobitex.ir/v2/orderbook/USDTIRT"
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
		return 0, fmt.Errorf("کد وضعیت Nobitex %d: %s", resp.StatusCode, string(body))
	}

	var data struct {
		Status         string `json:"status"`
		LastTradePrice string `json:"lastTradePrice"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return 0, fmt.Errorf("پارس پاسخ Nobitex شکست خورد: %w", err)
	}
	if data.LastTradePrice == "" {
		return 0, fmt.Errorf("قیمت دلار از Nobitex دریافت نشد")
	}
	rial, err := strconv.ParseFloat(data.LastTradePrice, 64)
	if err != nil {
		return 0, fmt.Errorf("قیمت دلار نامعتبر: %w", err)
	}
	return rial / 10, nil
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

// runCycle یک چرخه کامل: دریافت قیمت + ارسال به کانال
func runCycle(ctx context.Context, client *http.Client, cfg *Config) {
	// timeout مستقل برای هر چرخه تا اگر شبکه کند بود، چرخه بعدی قفل نشود
	cycleCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	prices, err := fetchPrices(cycleCtx, client)
	if err != nil {
		log.Printf("❌ خطای دریافت قیمت: %v", err)
		return
	}

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

func main() {
	cfg, err := loadConfig()
	if err != nil {
		log.Fatalf("خطای تنظیمات: %v", err)
	}

	client := &http.Client{Timeout: 20 * time.Second}

	// graceful shutdown با Ctrl+C یا SIGTERM
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	log.Printf("🚀 ربات شروع به کار کرد - بازه ارسال: %s - کانال: %s", cfg.Interval, cfg.ChannelID)

	// اولین پیام را بلافاصله بفرست (بدون انتظار برای ticker)
	runCycle(ctx, client, cfg)

	ticker := time.NewTicker(cfg.Interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			log.Println("🛑 سیگنال خاتمه دریافت شد، خروج تمیز...")
			return
		case <-ticker.C:
			runCycle(ctx, client, cfg)
		}
	}
}
