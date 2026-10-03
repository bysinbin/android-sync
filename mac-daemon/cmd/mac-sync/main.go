package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/user/android-mac-sync/mac-daemon/internal/discovery"
	"github.com/user/android-mac-sync/mac-daemon/internal/macos"
	"github.com/user/android-mac-sync/mac-daemon/internal/protocol"
	"github.com/user/android-mac-sync/mac-daemon/internal/server"
)

const (
	defaultWSPort = 42424
)

func main() {
	hostname, err := os.Hostname()
	if err != nil {
		hostname = "Mac"
	}
	serverName := fmt.Sprintf("%s (Sync)", hostname)

	fmt.Println("==================================================")
	fmt.Printf("🍏 Android-Mac Sync Daemon Başlatılıyor...\n")
	fmt.Printf("   Sunucu Adı    : %s\n", serverName)
	fmt.Printf("   WebSocket Port: %d\n", defaultWSPort)
	fmt.Printf("   Keşif Portu   : %d (UDP)\n", discovery.DiscoveryPort)
	fmt.Println("==================================================")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// 1. Pano Yöneticisi ve İzleyici
	clipManager := macos.NewClipboardManager()

	// 2. WebSocket Sunucusu
	syncServer := server.NewSyncServer(defaultWSPort, clipManager)

	// Mac panosu değiştiğinde Android'e ilet
	go clipManager.StartWatcher(ctx, func(text string) {
		msg, err := protocol.NewMessage(protocol.EventClipboard, protocol.ClipboardPayload{
			Text:      text,
			Timestamp: time.Now().UnixMilli(),
		})
		if err == nil {
			log.Printf("[Pano] Mac'ten kopyalandı, telefona iletiliyor (%d bayt)", len(text))
			syncServer.Broadcast(msg)
		}
	})

	// 3. UDP Keşif ve Beacon Başlat
	discovery.StartBroadcaster(ctx, serverName, defaultWSPort)
	log.Printf("[Keşif] UDP Broadcast yayını başlatıldı (Port %d)", discovery.DiscoveryPort)

	// 4. WebSocket Sunucusunu Başlat
	go func() {
		if err := syncServer.Start(ctx); err != nil {
			log.Fatalf("[Hata] WebSocket sunucusu durdu: %v", err)
		}
	}()

	// 4b. Mac Medya Takipçisini Başlat (MediaRemote + AppleScript)
	macos.StartMediaTracker(ctx, syncServer.UpdateMacMedia)
	log.Printf("[Medya] Mac sistem medya takipçisi başlatıldı.")

	// 5. Kapatma Sinyallerini Yakala
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)

	<-sigChan
	fmt.Println("\n[Bilgi] Kapatma sinyali alındı. Servisler sonlandırılıyor...")
	cancel()
	time.Sleep(500 * time.Millisecond)
	fmt.Println("[Tamam] Android-Mac Sync Daemon kapatıldı.")
}
