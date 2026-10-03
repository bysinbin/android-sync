#!/bin/bash
PLIST_PATH="$HOME/Library/LaunchAgents/com.sync.mac.plist"
launchctl unload "$PLIST_PATH" 2>/dev/null
rm -f "$PLIST_PATH"
echo "🛑 Mac Sync arka plan servisi kaldırıldı ve durduruldu."
