#!/usr/bin/env bash
set -euo pipefail

cd "$(dirname "$0")"

echo "📥 در حال گرفتن آخرین تغییرات..."
git pull

echo "🔨 در حال بیلد کردن..."
go build -o /tmp/crypto-bot .

echo "📦 جایگزینی فایل اجرایی..."
sudo mv /tmp/crypto-bot /usr/local/bin/crypto-bot

echo "🔄 ری‌استارت سرویس..."
sudo systemctl restart crypto-bot

echo "✅ وضعیت سرویس:"
sudo systemctl status crypto-bot --no-pager
