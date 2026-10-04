package main

import (
	"context"
	"encoding/base64"
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
	"syscall"
	"time"

	"windows-sync/internal/discovery"
	"windows-sync/internal/protocol"
	"windows-sync/internal/server"
	"windows-sync/internal/windows"
)

const (
	defaultWSPort = 42424
)

func main() {
	// Zaten çalışan bir servis örneği var mı kontrol et
	if conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", defaultWSPort), 400*time.Millisecond); err == nil {
		conn.Close()
		fmt.Println("==================================================")
		fmt.Println("ℹ️  [BİLGİ] Android-Windows Sync zaten arka planda çalışıyor!")
		fmt.Printf("🌐 Kontrol Paneli açılıyor: http://localhost:%d\n", defaultWSPort)
		fmt.Println("==================================================")
		windows.OpenURL(fmt.Sprintf("http://localhost:%d", defaultWSPort))
		return
	}

	hostname, err := os.Hostname()
	if err != nil {
		hostname = "Windows-PC"
	}
	serverName := fmt.Sprintf("%s (Windows Sync)", hostname)

	fmt.Println("==================================================")
	fmt.Printf("🪟 Android-Windows Sync Daemon Başlatılıyor...\n")
	fmt.Printf("   Sunucu Adı    : %s\n", serverName)
	fmt.Printf("   WebSocket Port: %d\n", defaultWSPort)
	fmt.Printf("   Keşif Portu   : %d (UDP)\n", discovery.DiscoveryPort)
	fmt.Printf("   Kontrol Paneli: http://localhost:%d\n", defaultWSPort)
	fmt.Println("==================================================")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// 1. Pano Yöneticisi
	clipManager := windows.NewClipboardManager()

	// 2. Tray Yöneticisi ve WebSocket Sunucusu Hazırlığı
	var syncServer *server.SyncServer

	trayManager := windows.NewTrayManager(windows.TrayCallbacks{
		OnOpenUI: func() {
			windows.OpenURL(fmt.Sprintf("http://localhost:%d", defaultWSPort))
		},
		OnRingPhone: func() {
			if syncServer != nil {
				syncServer.SendPhoneCommand("RING")
			}
		},
		OnStopRingPhone: func() {
			if syncServer != nil {
				syncServer.SendPhoneCommand("STOP_RING")
			}
		},
		OnPlayPause: func() {
			if syncServer != nil {
				syncServer.SendPhoneCommand("PLAY_PAUSE")
			}
		},
		OnNextPhone: func() {
			if syncServer != nil {
				syncServer.SendPhoneCommand("NEXT")
			}
		},
		OnSendClipboard: func() {
			if syncServer != nil {
				if imgBytes, err := clipManager.GetClipboardImage(); err == nil && len(imgBytes) > 0 {
					b64 := base64.StdEncoding.EncodeToString(imgBytes)
					msg, _ := protocol.NewMessage(protocol.EventClipboard, protocol.ClipboardPayload{
						Type:        "image",
						ImageBase64: b64,
						MimeType:    "image/png",
						Timestamp:   time.Now().UnixMilli(),
					})
					syncServer.Broadcast(msg)
					log.Printf("[Tray] 🖼 Bilgisayar görsel panosu telefona aktarıldı (%d bayt)", len(imgBytes))
				} else if clip, err := clipManager.GetClipboard(); err == nil && clip != "" {
					msg, _ := protocol.NewMessage(protocol.EventClipboard, protocol.ClipboardPayload{
						Type:      "text",
						Text:      clip,
						Timestamp: time.Now().UnixMilli(),
					})
					syncServer.Broadcast(msg)
					log.Printf("[Tray] Bilgisayar metin panosu telefona aktarıldı (%d bayt)", len(clip))
				}
			}
		},
		OnRescan: func() {
			discovery.StartBroadcaster(ctx, serverName, defaultWSPort)
			log.Println("[Tray] Ağ keşif yayını tazelendi.")
		},
		OnExit: func() {
			cancel()
		},
	})

	syncServer = server.NewSyncServer(defaultWSPort, serverName, clipManager, trayManager)

	// 3. Windows Görev Çubuğu (Tray) Başlat
	trayManager.Start()

	// 4. Windows Panosu değiştiğinde Android telefona ilet (Metin ve Görseller)
	go clipManager.StartWatcher(ctx, func(item windows.ClipItem) {
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
				log.Printf("[Pano] 🖼 Windows'tan görsel kopyalandı, telefona iletiliyor (%d bayt)", len(item.ImageBase64))
			} else {
				log.Printf("[Pano] Windows'tan metin kopyalandı, telefona iletiliyor (%d bayt)", len(item.Text))
			}
			syncServer.Broadcast(msg)
		}
	})

	// 5. UDP Keşif ve Beacon Başlat (Port 42425)
	discovery.StartBroadcaster(ctx, serverName, defaultWSPort)
	log.Printf("[Keşif] UDP Broadcast yayını başlatıldı (Port %d)", discovery.DiscoveryPort)

	// 6. WebSocket ve Web Sunucusunu Başlat (Port 42424)
	go func() {
		if err := syncServer.Start(ctx); err != nil {
			log.Fatalf("[Hata] Sunucu durdu: %v", err)
		}
	}()

	// 7. Windows Medya Takipçisini Başlat (GSMTC)
	windows.StartMediaTracker(ctx, syncServer.UpdatePCMedia)
	log.Printf("[Medya] Windows medya takipçisi başlatıldı.")

	// 8. Windows Bildirim Dinleyicisini Başlat (Gelen bildirimleri telefona ilet)
	windows.StartWindowsNotificationListener(ctx, syncServer.BroadcastPCNotification)
	log.Printf("[Bildirim] Windows gelen bildirim dinleyicisi başlatıldı.")

	// Başlatma bildirimi göster
	_ = windows.ShowToast("Android-Windows Sync", "Servis başarıyla başlatıldı ve bağlantıya hazır.", "Sync")

	// 8. Kapatma Sinyallerini Yakala
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)

	select {
	case <-sigChan:
	case <-ctx.Done():
	}

	fmt.Println("\n[Bilgi] Kapatma sinyali alındı. Servisler sonlandırılıyor...")
	trayManager.Stop()
	cancel()
	time.Sleep(500 * time.Millisecond)
	fmt.Println("[Tamam] Android-Windows Sync kapatıldı.")
}
