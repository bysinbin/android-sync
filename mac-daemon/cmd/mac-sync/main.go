package main

import (
	"context"
	"fmt"
	"log"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"syscall"
	"time"

	"mac-sync/internal/discovery"
	"mac-sync/internal/macos"
	"mac-sync/internal/protocol"
	"mac-sync/internal/server"
)

const (
	defaultWSPort = 42424
)

func main() {
	// Port kontrolü: Eğer zaten arka planda çalışıyorsa hata fırlatmak yerine tarayıcıyı aç
	conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", defaultWSPort), 300*time.Millisecond)
	if err == nil {
		conn.Close()
		fmt.Println("==================================================")
		fmt.Printf("ℹ️  Android-Mac Sync zaten çalışıyor (Port %d aktif)!\n", defaultWSPort)
		fmt.Printf("🌐 Kontrol Paneli açılıyor: http://localhost:%d\n", defaultWSPort)
		fmt.Println("==================================================")
		_ = exec.Command("open", fmt.Sprintf("http://localhost:%d", defaultWSPort)).Start()
		return
	}

	hostname, err := os.Hostname()
	if err != nil {
		hostname = "Mac"
	}
	serverName := fmt.Sprintf("%s (Mac Sync)", hostname)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// 1. Pano Yöneticisi ve İzleyici
	clipManager := macos.NewClipboardManager()

	// 2. WebSocket Sunucusu
	syncServer := server.NewSyncServer(defaultWSPort, serverName, clipManager)

	fmt.Println("==================================================")
	fmt.Printf("🍏 Android-Mac Sync Daemon Başlatılıyor...\n")
	fmt.Printf("   Sunucu Adı    : %s\n", serverName)
	fmt.Printf("   Eşleştirme PIN: %s\n", syncServer.GetPairingPIN())
	fmt.Printf("   WebSocket Port: %d\n", defaultWSPort)
	fmt.Printf("   Keşif Portu   : %d (UDP)\n", discovery.DiscoveryPort)
	fmt.Printf("   Kontrol Paneli: http://localhost:%d\n", defaultWSPort)
	fmt.Println("==================================================")


	// Mac panosu değiştiğinde Android'e ilet (Metin ve Görseller)
	go clipManager.StartWatcher(ctx, func(item macos.ClipItem) {
		payload := protocol.ClipboardPayload{
			Text:        item.Text,
			Type:        item.Type,
			ImageBase64: item.ImageBase64,
			MimeType:    item.MimeType,
			Timestamp:   time.Now().UnixMilli(),
		}
		msg, err := protocol.NewMessage(protocol.EventClipboard, payload)
		if err == nil {
			if item.Type == "image" {
				log.Printf("[Pano] 🖼 Mac'ten görsel kopyalandı, telefona iletiliyor (%d bayt)", len(item.ImageBase64))
			} else {
				log.Printf("[Pano] Mac'ten metin kopyalandı, telefona iletiliyor (%d bayt)", len(item.Text))
			}
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

	// 4c. Mac Bildirim Dinleyicisini Başlat (Gelen bildirimleri telefona ilet)
	macos.StartMacNotificationListener(ctx, syncServer.BroadcastMacNotification)
	log.Printf("[Bildirim] Mac gelen bildirim dinleyicisi başlatıldı.")

	// 5. Kapatma Sinyallerini Yakala
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)

	<-sigChan
	fmt.Println("\n[Bilgi] Kapatma sinyali alındı. Servisler sonlandırılıyor...")
	cancel()
	time.Sleep(500 * time.Millisecond)
	fmt.Println("[Tamam] Android-Mac Sync Daemon kapatıldı.")
}
