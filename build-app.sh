#!/bin/bash
set -e

DIR="$( cd "$( dirname "${BASH_SOURCE[0]}" )" && pwd )"
APP_NAME="MacSync.app"
APP_DIR="$DIR/$APP_NAME"

echo "🔨 MacSync.app Oluşturuluyor..."

# 0. Eski süreçleri durdur
killall MacSync 2>/dev/null || true
killall mac-sync 2>/dev/null || true
sleep 0.5

# 1. Klasör yapısını oluştur
rm -rf "$APP_DIR"
mkdir -p "$APP_DIR/Contents/MacOS"
mkdir -p "$APP_DIR/Contents/Resources"

# 2. Go daemon derle
echo "📦 Go Daemon derleniyor..."
cd "$DIR/mac-daemon"
go build -o mac-sync ./cmd/mac-sync
cd "$DIR"

# 3. Mediactl derle
echo "🎵 Mediactl derleniyor..."
swiftc -O -o "$DIR/mac-daemon/mediactl" "$DIR/mac-daemon/mediactl_src.swift"
cp "$DIR/mac-daemon/mediactl" "$DIR/mediactl"

# 4. Swift Menü Çubuğu ikilisini derle
echo "🍏 Swift Menu Bar App derleniyor..."
swiftc -O -o "$APP_DIR/Contents/MacOS/MacSync" "$DIR/mac-app/src/main.swift"

# 5. Bağımlılıkları ve İkonu Resources içine kopyala
cp "$DIR/mac-daemon/mac-sync" "$APP_DIR/Contents/Resources/mac-sync"
cp "$DIR/mediactl" "$APP_DIR/Contents/Resources/mediactl"
if [ -f "$DIR/AppIcon.icns" ]; then
    cp "$DIR/AppIcon.icns" "$APP_DIR/Contents/Resources/AppIcon.icns"
fi

# 6. Info.plist oluştur (LSUIElement = true ile menü çubuğuna yerleşir)
cat << 'EOF' > "$APP_DIR/Contents/Info.plist"
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>CFBundleExecutable</key>
    <string>MacSync</string>
    <key>CFBundleIdentifier</key>
    <string>com.sync.mac</string>
    <key>CFBundleName</key>
    <string>MacSync</string>
    <key>CFBundleIconFile</key>
    <string>AppIcon</string>
    <key>CFBundlePackageType</key>
    <string>APPL</string>
    <key>CFBundleShortVersionString</key>
    <string>1.0</string>
    <key>LSUIElement</key>
    <true/>
    <key>NSHighResolutionCapable</key>
    <true/>
    <key>NSAppleEventsUsageDescription</key>
    <string>Mac Sync telefon bildirim ve medya kontrolleri için izin gerektirir.</string>
    <key>NSAccessibilityUsageDescription</key>
    <string>Mac Sync Mac üzerindeki medya oynatımını ve ses düzeyini yönetmek için erişilebilirlik iznine ihtiyaç duyar.</string>
</dict>
</plist>
EOF

chmod +x "$APP_DIR/Contents/MacOS/MacSync"
chmod +x "$APP_DIR/Contents/Resources/mac-sync"
chmod +x "$APP_DIR/Contents/Resources/mediactl"

# 7. Ad-hoc imzalama (macOS TCC / Erişilebilirlik izninin kalıcı ve kararlı olması için)
codesign --force --deep --sign - "$APP_DIR" 2>/dev/null || true

echo "✅ $APP_NAME başarıyla hazırlandı: $APP_DIR"
