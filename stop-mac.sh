#!/bin/bash
# Android-Mac Sync Durdurma Scripti

echo "=================================================="
echo "🛑 Android-Mac Sync Servisleri Durduruluyor..."
echo "=================================================="

# 1. mac-sync arka plan sürecini sonlandır
pkill -f "mac-sync" 2>/dev/null && echo "✓ mac-sync sonlandırıldı" || echo "- mac-sync çalışmıyordu"

# 2. Menü çubuğu Swift uygulamasını sonlandır
pkill -f "MacSync" 2>/dev/null && echo "✓ MacSync menü çubuğu uygulaması sonlandırıldı" || echo "- MacSync uygulaması çalışmıyordu"

echo "=================================================="
echo "✅ Tüm Mac servisleri temiz bir şekilde kapatıldı."
echo "=================================================="
