package server

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"log"
	"math"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/user/android-mac-sync/mac-daemon/internal/macos"
	"github.com/user/android-mac-sync/mac-daemon/internal/protocol"
)

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool {
		return true // Allow LAN connections
	},
	ReadBufferSize:  1024 * 32,
	WriteBufferSize: 1024 * 32,
}

// AudioHub manages live audio distribution between Mac and Android.
type AudioHub struct {
	mu        sync.RWMutex
	listeners map[chan []byte]bool
}

func NewAudioHub() *AudioHub {
	return &AudioHub{
		listeners: make(map[chan []byte]bool),
	}
}

func (h *AudioHub) Subscribe() chan []byte {
	h.mu.Lock()
	defer h.mu.Unlock()
	ch := make(chan []byte, 64)
	h.listeners[ch] = true
	return ch
}

func (h *AudioHub) Unsubscribe(ch chan []byte) {
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.listeners, ch)
	close(ch)
}

func (h *AudioHub) Broadcast(chunk []byte) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	for ch := range h.listeners {
		select {
		case ch <- chunk:
		default:
		}
	}
}

func makeWavHeader(sampleRate int, bitsPerSample int, channels int) []byte {
	h := make([]byte, 44)
	copy(h[0:], "RIFF")
	binary.LittleEndian.PutUint32(h[4:], 0x7ffffff0)
	copy(h[8:], "WAVE")
	copy(h[12:], "fmt ")
	binary.LittleEndian.PutUint32(h[16:], 16)
	binary.LittleEndian.PutUint16(h[20:], 1)
	binary.LittleEndian.PutUint16(h[22:], uint16(channels))
	binary.LittleEndian.PutUint32(h[24:], uint32(sampleRate))
	byteRate := sampleRate * channels * (bitsPerSample / 8)
	binary.LittleEndian.PutUint32(h[28:], uint32(byteRate))
	blockAlign := channels * (bitsPerSample / 8)
	binary.LittleEndian.PutUint16(h[32:], uint16(blockAlign))
	binary.LittleEndian.PutUint16(h[34:], uint16(bitsPerSample))
	copy(h[36:], "data")
	binary.LittleEndian.PutUint32(h[40:], 0x7ffff000)
	return h
}

type SyncServer struct {
	port           int
	clipManager    *macos.ClipboardManager
	clientsMu      sync.RWMutex
	clients        map[*websocket.Conn]bool
	lastDeviceInfo *protocol.DeviceInfoPayload
	lastMacMedia   *protocol.MediaInfoPayload
	lastPhoneMedia *protocol.MediaInfoPayload
	audioHub       *AudioHub
}

func NewSyncServer(port int, clipManager *macos.ClipboardManager) *SyncServer {
	return &SyncServer{
		port:        port,
		clipManager: clipManager,
		clients:     make(map[*websocket.Conn]bool),
		audioHub:    NewAudioHub(),
	}
}

func (s *SyncServer) Start(ctx context.Context) error {
	mux := http.NewServeMux()
	mux.HandleFunc("/ws", s.handleWebSocket)
	mux.HandleFunc("/status", func(w http.ResponseWriter, r *http.Request) {
		s.clientsMu.RLock()
		count := len(s.clients)
		dev := s.lastDeviceInfo
		macMedia := s.lastMacMedia
		phoneMedia := s.lastPhoneMedia
		s.clientsMu.RUnlock()

		w.Header().Set("Content-Type", "application/json")
		statusResp := map[string]any{
			"status":        "OK",
			"clients_count": count,
			"clients_text":  fmt.Sprintf("clients: %d", count),
			"connected":     count > 0,
		}
		if dev != nil && count > 0 {
			statusResp["device_name"] = dev.DeviceName
			statusResp["model"] = dev.Model
			statusResp["battery_level"] = dev.BatteryLevel
			statusResp["is_charging"] = dev.IsCharging
		}
		if macMedia != nil {
			statusResp["mac_media"] = macMedia
		}
		if phoneMedia != nil {
			statusResp["phone_media"] = phoneMedia
		}
		_ = json.NewEncoder(w).Encode(statusResp)
	})

	mux.HandleFunc("/media/mac_update", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		var info protocol.MediaInfoPayload
		if err := json.NewDecoder(r.Body).Decode(&info); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		s.UpdateMacMedia(info)
		w.Header().Set("Content-Type", "text/plain")
		fmt.Fprintf(w, "OK\n")
	})

	mux.HandleFunc("/phone/command", func(w http.ResponseWriter, r *http.Request) {
		action := r.URL.Query().Get("action")
		if action == "" {
			http.Error(w, "missing action", http.StatusBadRequest)
			return
		}
		percentStr := r.URL.Query().Get("percent")
		var percent float64
		if percentStr != "" {
			_, _ = fmt.Sscanf(percentStr, "%f", &percent)
		}
		log.Printf("[Server] Telefondan işlem istendi: %s (Yüzde: %.1f)", action, percent)
		if action == "SEEK_PERCENT" {
			s.SendPhoneCommandWithPercent(action, percent)
		} else {
			s.SendPhoneCommand(action)
		}
		w.Header().Set("Content-Type", "text/plain")
		fmt.Fprintf(w, "OK\n")
	})

	// Live audio streaming from Mac to Android
	mux.HandleFunc("/audio/stream", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "audio/x-wav")
		w.Header().Set("Connection", "keep-alive")
		w.Header().Set("Cache-Control", "no-cache")
		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "Streaming unsupported", http.StatusInternalServerError)
			return
		}

		header := makeWavHeader(44100, 16, 2)
		_, _ = w.Write(header)
		flusher.Flush()

		ch := s.audioHub.Subscribe()
		defer s.audioHub.Unsubscribe(ch)

		for {
			select {
			case <-r.Context().Done():
				return
			case chunk, ok := <-ch:
				if !ok {
					return
				}
				if _, err := w.Write(chunk); err != nil {
					return
				}
				flusher.Flush()
			}
		}
	})

	// Feed audio into MacSync audio hub
	mux.HandleFunc("/audio/feed", func(w http.ResponseWriter, r *http.Request) {
		buf := make([]byte, 4096)
		for {
			n, err := r.Body.Read(buf)
			if n > 0 {
				chunk := make([]byte, n)
				copy(chunk, buf[:n])
				s.audioHub.Broadcast(chunk)
			}
			if err != nil {
				break
			}
		}
		w.WriteHeader(http.StatusOK)
	})

	mux.HandleFunc("/download", func(w http.ResponseWriter, r *http.Request) {
		// Look for MacSync.apk
		candidates := []string{"MacSync.apk", "../MacSync.apk", "android-app/app/build/outputs/apk/debug/app-debug.apk", "../android-app/app/build/outputs/apk/debug/app-debug.apk"}
		for _, path := range candidates {
			if _, err := os.Stat(path); err == nil {
				w.Header().Set("Content-Type", "application/vnd.android.package-archive")
				w.Header().Set("Content-Disposition", "attachment; filename=\"MacSync.apk\"")
				http.ServeFile(w, r, path)
				return
			}
		}
		http.Error(w, "MacSync.apk bulunamadı", http.StatusNotFound)
	})

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		html := `<!DOCTYPE html>
<html>
<head>
    <meta name="viewport" content="width=device-width, initial-scale=1">
    <title>Mac Sync</title>
    <style>
        body { font-family: -apple-system, sans-serif; background: #0D1117; color: #F0F6FC; display: flex; justify-content: center; align-items: center; min-height: 100vh; margin: 0; padding: 20px; box-sizing: border-box; }
        .card { background: #161B22; border: 1px solid #30363D; border-radius: 16px; padding: 30px; text-align: center; max-width: 380px; width: 100%; box-shadow: 0 8px 24px rgba(0,0,0,0.5); }
        h1 { margin-top: 0; font-size: 24px; }
        p { color: #8B949E; font-size: 14px; line-height: 1.5; }
        .btn { display: inline-block; background: #238636; color: white; text-decoration: none; padding: 14px 28px; border-radius: 10px; font-weight: bold; font-size: 16px; margin-top: 20px; transition: background 0.2s; }
        .btn:hover { background: #2ea043; }
        .status { margin-top: 20px; font-size: 12px; color: #58A6FF; }
    </style>
</head>
<body>
    <div class="card">
        <h1>🍏 Mac Sync</h1>
        <p>Android cihazınız için güncel uygulamayı indirin ve yükleyin.</p>
        <a href="/download" class="btn">📲 MacSync.apk İndir</a>
        <div class="status">Sunucu Aktif &amp; Bağlantıya Hazır</div>
    </div>
</body>
</html>`
		fmt.Fprint(w, html)
	})

	httpServer := &http.Server{
		Addr:    fmt.Sprintf(":%d", s.port),
		Handler: mux,
	}

	// Background poller for Mac's active media from MacSync.app (Port 42426)
	go func() {
		client := &http.Client{Timeout: 900 * time.Millisecond}
		ticker := time.NewTicker(1500 * time.Millisecond)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				resp, err := client.Get("http://127.0.0.1:42426/current_media")
				if err == nil && resp.StatusCode == http.StatusOK {
					var info protocol.MediaInfoPayload
					if err := json.NewDecoder(resp.Body).Decode(&info); err == nil && info.Title != "" {
						_ = resp.Body.Close()
						s.UpdateMacMedia(info)
					} else {
						_ = resp.Body.Close()
					}
				}
			}
		}
	}()

	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = httpServer.Shutdown(shutdownCtx)
	}()

	log.Printf("[Server] WebSocket listening on :%d/ws", s.port)
	if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return err
	}
	return nil
}

func (s *SyncServer) handleWebSocket(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("[Server] WebSocket upgrade error: %v", err)
		return
	}
	defer conn.Close()

	s.clientsMu.Lock()
	s.clients[conn] = true
	s.clientsMu.Unlock()

	log.Printf("[Server] 📱 Android client connected from %s", conn.RemoteAddr())

	// Welcome greeting
	welcome, _ := protocol.NewMessage(protocol.EventDeviceInfo, map[string]string{
		"server_name": "macOS Daemon",
		"status":      "connected",
	})
	_ = conn.WriteJSON(welcome)

	// Send current Mac media info if playing or available
	s.clientsMu.RLock()
	curMacMedia := s.lastMacMedia
	s.clientsMu.RUnlock()
	if curMacMedia != nil {
		if initialMedia, err := protocol.NewMessage(protocol.EventMediaInfo, curMacMedia); err == nil {
			_ = conn.WriteJSON(initialMedia)
		}
	}

	defer func() {
		s.clientsMu.Lock()
		delete(s.clients, conn)
		s.clientsMu.Unlock()
		log.Printf("[Server] 📱 Android client disconnected: %s", conn.RemoteAddr())
	}()

	for {
		var msg protocol.Message
		err := conn.ReadJSON(&msg)
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
				log.Printf("[Server] Read error: %v", err)
			}
			break
		}

		s.processMessage(&msg)
	}
}

func (s *SyncServer) processMessage(msg *protocol.Message) {
	switch msg.Event {
	case protocol.EventDeviceInfo:
		var p protocol.DeviceInfoPayload
		if err := json.Unmarshal(msg.Payload, &p); err == nil {
			s.clientsMu.Lock()
			s.lastDeviceInfo = &p
			s.clientsMu.Unlock()
			log.Printf("[Device] 📱 %s (%s) - Pil: %%%d (Şarjda: %v)", p.DeviceName, p.Model, p.BatteryLevel, p.IsCharging)
		}

	case protocol.EventNotification:
		var p protocol.NotificationPayload
		if err := json.Unmarshal(msg.Payload, &p); err == nil {
			log.Printf("[Bildirim] [%s] %s: %s", p.AppName, p.Title, p.Text)
			_ = macos.ShowNotification(p.AppName, p.Title, p.Text, "Glass")
		}

	case protocol.EventCallState:
		var p protocol.CallStatePayload
		if err := json.Unmarshal(msg.Payload, &p); err == nil {
			log.Printf("[Arama] Durum: %s, Numara: %s, Kişi: %s", p.State, p.PhoneNumber, p.CallerName)
			if p.State == "RINGING" {
				macos.PauseAllMedia() // Telefon çalınca Mac medyasını duraklat
				_ = macos.ShowCallAlert(p.CallerName, p.PhoneNumber)
			} else if p.State == "IDLE" || p.State == "OFFHOOK" {
				macos.DismissCallAlert()
			}
		}

	case protocol.EventMediaInfo:
		var p protocol.MediaInfoPayload
		if err := json.Unmarshal(msg.Payload, &p); err == nil {
			p.Source = "phone"
			s.clientsMu.Lock()
			s.lastPhoneMedia = &p
			s.clientsMu.Unlock()
			log.Printf("[Medya] 📱 Telefondaki Medya: %s - %s (Süre: %d/%d ms, %%%0.1f)", p.Title, p.Artist, p.PositionMs, p.DurationMs, p.Percent)
		}

	case protocol.EventMediaCommand:
		var p protocol.MediaCommandPayload
		if err := json.Unmarshal(msg.Payload, &p); err == nil {
			log.Printf("[Medya Komutu] %s (Percent: %.1f)", p.Action, p.Percent)
			if p.Action == "SEEK_PERCENT" {
				_ = macos.ExecuteMediaSeekPercent(p.Percent)
			} else {
				_ = macos.ExecuteMediaAction(p.Action)
			}
		}

	case protocol.EventClipboard:
		var p protocol.ClipboardPayload
		if err := json.Unmarshal(msg.Payload, &p); err == nil {
			log.Printf("[Pano] Telefondan metin alındı (%d bayt)", len(p.Text))
			_ = s.clipManager.SetClipboard(p.Text)
		}

	case protocol.EventPing:
		resp, _ := protocol.NewMessage(protocol.EventPong, map[string]int64{"time": time.Now().UnixMilli()})
		s.Broadcast(resp)

	default:
		log.Printf("[Server] Bilinmeyen event: %s", msg.Event)
	}
}

// SendPhoneCommand sends a command to all connected Android phones (media, volume, ring).
func (s *SyncServer) SendPhoneCommand(action string) {
	msg, err := protocol.NewMessage(protocol.EventPhoneCommand, protocol.PhoneCommandPayload{Action: action})
	if err == nil {
		s.Broadcast(msg)
	}
}

// SendPhoneCommandWithPercent sends a seek percent command to all connected Android phones.
func (s *SyncServer) SendPhoneCommandWithPercent(action string, percent float64) {
	msg, err := protocol.NewMessage(protocol.EventPhoneCommand, protocol.PhoneCommandPayload{
		Action:  action,
		Percent: percent,
	})
	if err == nil {
		s.Broadcast(msg)
	}
}

// Broadcast sends a message to all connected Android clients.
func (s *SyncServer) Broadcast(msg *protocol.Message) {
	s.clientsMu.RLock()
	defer s.clientsMu.RUnlock()

	for conn := range s.clients {
		if err := conn.WriteJSON(msg); err != nil {
			log.Printf("[Server] Broadcast error to %v: %v", conn.RemoteAddr(), err)
		}
	}
}

// UpdateMacMedia updates the stored Mac media and broadcasts to Android if changed.
func (s *SyncServer) UpdateMacMedia(info protocol.MediaInfoPayload) {
	if info.Title == "" && info.Artist == "" {
		return
	}
	info.Source = "mac"
	s.clientsMu.Lock()
	prev := s.lastMacMedia
	s.lastMacMedia = &info
	s.clientsMu.Unlock()

	// Broadcast if changed or playing and percent moved >= 0.5%
	if prev == nil || prev.Title != info.Title || prev.Artist != info.Artist || prev.IsPlaying != info.IsPlaying || math.Abs(prev.Percent-info.Percent) >= 0.5 {
		if prev == nil || prev.Title != info.Title || prev.Artist != info.Artist {
			log.Printf("[Medya] 🍏 Mac'ten medya: %s - %s (Süre: %d/%d ms, %%%0.1f)", info.Title, info.Artist, info.PositionMs, info.DurationMs, info.Percent)
		}
		if msg, err := protocol.NewMessage(protocol.EventMediaInfo, info); err == nil {
			s.Broadcast(msg)
		}
	}
}

