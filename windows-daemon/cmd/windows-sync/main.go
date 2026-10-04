package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"log"
	"mime/multipart"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
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
	// 1. Komut Satırı Argümanları Kontrolü
	for i, arg := range os.Args {
		if (arg == "--send-file" || arg == "-send-file") && i+1 < len(os.Args) {
			filePath := os.Args[i+1]
			uploadFileFromCLI(filePath)
			return
		}
		if arg == "--app" || arg == "--app-mode" {
			windows.OpenAppMode(fmt.Sprintf("http://localhost:%d", defaultWSPort))
			return
		}
	}

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
		OnOpenAppMode: func() {
			windows.OpenAppMode(fmt.Sprintf("http://localhost:%d", defaultWSPort))
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
	fmt.Println("[Tamam] Android-Windows Sync kapatıldı.")
}

func uploadFileFromCLI(filePath string) {
	file, err := os.Open(filePath)
	if err != nil {
		fmt.Printf("❌ Dosya açılamadı: %v\n", err)
		return
	}
	defer file.Close()

	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	part, err := writer.CreateFormFile("file", filepath.Base(filePath))
	if err != nil {
		fmt.Printf("❌ Form hatası: %v\n", err)
		return
	}
	_, err = io.Copy(part, file)
	if err != nil {
		fmt.Printf("❌ Kopyalama hatası: %v\n", err)
		return
	}
	_ = writer.Close()

	client := &http.Client{Timeout: 30 * time.Second}
	req, err := http.NewRequest("POST", fmt.Sprintf("http://localhost:%d/file/upload", defaultWSPort), body)
	if err != nil {
		fmt.Printf("❌ İstek hatası: %v\n", err)
		return
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())

	resp, err := client.Do(req)
	if err != nil {
		_ = windows.ShowToast("Android Sync", "Servis çalışmıyor! Lütfen önce Android Sync servisini başlatın.", "Hata")
		fmt.Printf("❌ Servis çalışmıyor: %v\n", err)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusOK {
		_ = windows.ShowToast("Android Sync", filepath.Base(filePath)+" telefona başarıyla gönderildi.", "Dosya Aktarımı")
		fmt.Printf("✅ Dosya telefona gönderildi: %s\n", filePath)
	} else {
		fmt.Printf("❌ Sunucu hatası: %d\n", resp.StatusCode)
	}
}
