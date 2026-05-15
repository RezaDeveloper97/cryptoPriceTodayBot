#!/usr/bin/env bash
# setup-bot.sh — آماده‌سازی ربات تلگرام برای حالت polling
#
# این ربات از long-polling استفاده می‌کند، نه webhook. یعنی نیازی به
# تنظیم webhook نیست؛ ولی اگر قبلاً برای این توکن webhook ست کرده باشی،
# تلگرام اجازه‌ی getUpdates نمی‌دهد (خطای 409). این اسکریپت وضعیت
# فعلی را نشان می‌دهد و در صورت نیاز webhook را پاک می‌کند.
#
# استفاده:
#   ./setup-bot.sh                 # نمایش اطلاعات ربات و وضعیت webhook
#   ./setup-bot.sh --clear         # پاک کردن webhook (با تایید)
#   ./setup-bot.sh --clear --yes   # پاک کردن webhook بدون تایید
#
# منبع توکن (به ترتیب اولویت):
#   1. متغیر محیطی TELEGRAM_BOT_TOKEN
#   2. فایل .env در همین پوشه
#   3. فایل /etc/crypto-bot.env (محل deploy)

set -euo pipefail

cd "$(dirname "$0")"

CLEAR=0
YES=0
for arg in "$@"; do
  case "$arg" in
    --clear) CLEAR=1 ;;
    --yes|-y) YES=1 ;;
    -h|--help)
      sed -n '2,18p' "$0"; exit 0 ;;
    *)
      echo "❌ گزینه ناشناخته: $arg" >&2; exit 1 ;;
  esac
done

# پیدا کردن توکن
TOKEN="${TELEGRAM_BOT_TOKEN:-}"
if [ -z "$TOKEN" ] && [ -f .env ]; then
  TOKEN=$(grep -E '^TELEGRAM_BOT_TOKEN=' .env | head -n1 | cut -d= -f2- | tr -d '"' | tr -d "'")
fi
if [ -z "$TOKEN" ] && [ -f /etc/crypto-bot.env ]; then
  TOKEN=$(sudo grep -E '^TELEGRAM_BOT_TOKEN=' /etc/crypto-bot.env | head -n1 | cut -d= -f2- | tr -d '"' | tr -d "'")
fi
if [ -z "$TOKEN" ]; then
  echo "❌ TELEGRAM_BOT_TOKEN پیدا نشد. آن را در .env یا متغیر محیطی بگذار." >&2
  exit 1
fi

API="https://api.telegram.org/bot${TOKEN}"

# نیاز به ابزارهای پایه
for cmd in curl python3; do
  command -v "$cmd" >/dev/null 2>&1 || { echo "❌ نیاز به $cmd داری" >&2; exit 1; }
done

# helper برای استخراج فیلد از JSON با python (بدون نیاز به jq)
json_get() {
  python3 -c "import json,sys; d=json.load(sys.stdin);
keys='$1'.split('.')
cur=d
for k in keys:
    if isinstance(cur, dict) and k in cur:
        cur = cur[k]
    else:
        cur = ''
        break
print(cur if cur != '' else '')" 2>/dev/null || echo ""
}

echo "🔍 در حال گرفتن اطلاعات ربات..."
ME=$(curl -fsS "${API}/getMe") || { echo "❌ تماس با تلگرام شکست خورد. توکن معتبر است؟" >&2; exit 1; }

OK=$(echo "$ME" | json_get "ok")
if [ "$OK" != "True" ]; then
  echo "❌ تلگرام پاسخ منفی داد:" >&2
  echo "$ME" >&2
  exit 1
fi

USERNAME=$(echo "$ME" | json_get "result.username")
FIRST=$(echo "$ME" | json_get "result.first_name")
BOT_ID=$(echo "$ME" | json_get "result.id")

echo
echo "✅ اطلاعات ربات:"
echo "   نام:       $FIRST"
echo "   شناسه:     $BOT_ID"
echo "   نام کاربری: @$USERNAME"
echo
echo "💡 برای اینکه دکمه «🔁 تبدیل» زیر پست‌های قیمت ظاهر شود، در .env بگذار:"
echo "   TELEGRAM_BOT_USERNAME=$USERNAME"
echo

echo "🔍 در حال بررسی وضعیت webhook..."
WH=$(curl -fsS "${API}/getWebhookInfo")
WH_URL=$(echo "$WH" | json_get "result.url")
PENDING=$(echo "$WH" | json_get "result.pending_update_count")

if [ -z "$WH_URL" ]; then
  echo "✅ هیچ webhookی ست نشده — polling آماده است."
  echo "   آپدیت‌های منتظر: ${PENDING:-0}"
  exit 0
fi

echo "⚠️  یک webhook ست شده:"
echo "   آدرس:              $WH_URL"
echo "   آپدیت‌های منتظر:    ${PENDING:-0}"
echo
echo "تا وقتی webhook فعال است، ربات نمی‌تواند با polling کار کند (خطای 409)."

if [ "$CLEAR" -ne 1 ]; then
  echo
  echo "💡 برای پاک کردن webhook اجرا کن:"
  echo "   ./setup-bot.sh --clear"
  exit 0
fi

if [ "$YES" -ne 1 ]; then
  printf "❓ مطمئنی webhook پاک شود؟ [y/N] "
  read -r ans
  case "$ans" in
    y|Y|yes|YES) ;;
    *) echo "لغو شد."; exit 0 ;;
  esac
fi

echo "🧹 در حال پاک کردن webhook..."
DEL=$(curl -fsS "${API}/deleteWebhook?drop_pending_updates=false")
DEL_OK=$(echo "$DEL" | json_get "ok")
if [ "$DEL_OK" = "True" ]; then
  echo "✅ webhook پاک شد. حالا polling کار خواهد کرد."
  echo "   اگر سرویس crypto-bot در حال اجرا است، نیازی به ری‌استارت نیست —"
  echo "   حلقه‌ی polling به‌محض دفعه‌ی بعد به‌طور خودکار وصل می‌شود."
else
  echo "❌ پاک کردن شکست خورد:" >&2
  echo "$DEL" >&2
  exit 1
fi
