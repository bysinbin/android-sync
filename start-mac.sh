#!/bin/bash
DIR="$( cd "$( dirname "${BASH_SOURCE[0]}" )" && pwd )"
echo "🚀 Mac Sync Daemon Başlatılıyor..."
cd "$DIR/mac-daemon" && ./mac-sync
