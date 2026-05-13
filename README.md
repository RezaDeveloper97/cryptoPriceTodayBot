# ربات قیمت ارز دیجیتال

ربات تلگرام به زبان Go که هر دقیقه قیمت چند ارز دیجیتال (BTC, ETH, USDT, BNB, XRP, SOL, DOGE) رو از CoinGecko می‌گیره و در کانال تلگرام می‌فرسته.

## ویژگی‌ها
- **بدون وابستگی خارجی** - فقط stdlib گو
- Graceful shutdown با `Ctrl+C` و `SIGTERM`
- نمایش تغییرات ۲۴ ساعته با ایموجی 🟢/🔴
- زمان به وقت تهران
- بازه ارسال قابل تنظیم
- Timeout مستقل برای هر چرخه

## راه‌اندازی

### ۱. ساخت ربات
1. به [@BotFather](https://t.me/BotFather) برو
2. دستور `/newbot` بزن و توکن رو بگیر
3. ربات رو به کانالت **ادمین** کن (با دسترسی Post Messages)

### ۲. گرفتن آی‌دی کانال
- **کانال عمومی:** `@your_channel`
- **کانال خصوصی:** پیامی از کانال رو فوروارد کن به [@userinfobot](https://t.me/userinfobot)، آی‌دی شکل `-100xxxxxxxxxx` رو می‌ده

### ۳. اجرا
```bash
export TELEGRAM_BOT_TOKEN="123456:ABC..."
export TELEGRAM_CHANNEL_ID="@your_channel"
export INTERVAL="1m"   # اختیاری

go run main.go
```

یا با build:
```bash
go build -o crypto-bot .
./crypto-bot
```

## اضافه کردن ارز جدید
در `main.go` به اسلایس `coins` اضافه کن. `ID` رو از [coingecko.com](https://www.coingecko.com) (صفحه هر ارز، فیلد API id) بگیر:

```go
{ID: "cardano", Symbol: "ADA", Name: "کاردانو"},
```

## اجرا به عنوان سرویس (systemd)
فایل `/etc/systemd/system/crypto-bot.service`:

```ini
[Unit]
Description=Crypto Price Telegram Bot
After=network.target

[Service]
Type=simple
User=www-data
WorkingDirectory=/opt/crypto-bot
Environment="TELEGRAM_BOT_TOKEN=..."
Environment="TELEGRAM_CHANNEL_ID=@your_channel"
Environment="INTERVAL=1m"
ExecStart=/opt/crypto-bot/crypto-bot
Restart=always
RestartSec=10

[Install]
WantedBy=multi-user.target
```

```bash
sudo systemctl daemon-reload
sudo systemctl enable --now crypto-bot
sudo journalctl -u crypto-bot -f
```

## نکات
- CoinGecko در پلن رایگان حدود ۳۰ درخواست در دقیقه می‌ده، که برای بازه ۱ دقیقه‌ای کاملاً کافیه
- اگه می‌خوای پیام‌ها رو ادیت کنی به جای ارسال جدید، تابع `editMessageText` تلگرام رو ببین و `message_id` آخرین پیام رو نگه دار
- برای پایداری بیشتر، می‌تونی retry با backoff به `runCycle` اضافه کنی
