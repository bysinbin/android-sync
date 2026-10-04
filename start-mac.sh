#!/bin/bash
# Android-Mac Sync Başlatma Scripti
set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$SCRIPT_DIR"

echo "=================================================="
echo "🍏 Android-Mac Sync Başlatılıyor..."
echo "=================================================="

# 1. mac-sync binary kontrolü / hazırlığı
if [ ! -f "$SCRIPT_DIR/mac-daemon/mac-sync" ]; then
    ARCH=$(uname -m)
    if [ "$ARCH" = "arm64" ] && [ -f "$SCRIPT_DIR/mac-daemon/mac-sync-arm64" ]; then
        echo "⚙️  Apple Silicon (arm64) ikili dosyası kopyalanıyor..."
        cp "$SCRIPT_DIR/mac-daemon/mac-sync-arm64" "$SCRIPT_DIR/mac-daemon/mac-sync"
    elif [ "$ARCH" = "x86_64" ] && [ -f "$SCRIPT_DIR/mac-daemon/mac-sync-amd64" ]; then
        echo "⚙️  Intel Mac (amd64) ikili dosyası kopyalanıyor..."
        cp "$SCRIPT_DIR/mac-daemon/mac-sync-amd64" "$SCRIPT_DIR/mac-daemon/mac-sync"
    else
        echo "⚙️  mac-sync yerel ortamda derleniyor..."
        cd "$SCRIPT_DIR/mac-daemon"
        go build -o mac-sync ./cmd/mac-sync
        cd "$SCRIPT_DIR"
    fi
fi

chmod +x "$SCRIPT_DIR/mac-daemon/mac-sync" 2>/dev/null || true

# 2. Daemon'u başlat
echo "🚀 Mac Daemon başlatılıyor..."
"$SCRIPT_DIR/mac-daemon/mac-sync" &
DAEMON_PID=$!
echo "   Daemon PID: $DAEMON_PID"

sleep 1

# 3. Kontrol Panelini Tarayıcıda Aç
open "http://localhost:42424" 2>/dev/null || true

echo "=================================================="
echo "✅ Android-Mac Sync Servisi Çalışıyor!"
echo "🌐 Kontrol Paneli: http://localhost:42424"
echo "🛑 Durdurmak için: ./stop-mac.sh veya kill $DAEMON_PID"
echo "=================================================="
