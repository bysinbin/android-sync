package server

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"math/rand"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"windows-sync/internal/protocol"
	"windows-sync/internal/windows"
)

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool {
		return true // Allow LAN connections
	},
	ReadBufferSize:  1024 * 32,
	WriteBufferSize: 1024 * 32,
}

type DaemonConfig struct {
	MachineID  string `json:"machine_id"`
	ServerName string `json:"server_name"`
	AuthToken  string `json:"auth_token"`
	DeviceName string `json:"device_name"`
	PairedAt   int64  `json:"paired_at"`
}

type SyncServer struct {
	port            int
	serverName      string
	machineID       string
	pairingPIN      string
	isPaired        bool
	configPath      string
	configMu        sync.RWMutex
	config          DaemonConfig
	clipManager     *windows.ClipboardManager
	trayManager     *windows.TrayManager
	clientsMu       sync.RWMutex
	clients         map[*websocket.Conn]bool
	lastDeviceInfo  *protocol.DeviceInfoPayload
	lastPCMedia     *protocol.MediaInfoPayload
	lastPhoneMedia  *protocol.MediaInfoPayload
	notifications   []protocol.NotificationPayload
	notifsMu        sync.RWMutex
	lastCallState   *protocol.CallStatePayload
	callMu          sync.RWMutex
	activeCallStart time.Time
	smsMessages     []protocol.SmsMessage
	smsMu           sync.RWMutex
}

func getDaemonConfigPath() string {
	appData := os.Getenv("APPDATA")
	if appData != "" {
		dir := filepath.Join(appData, "AndroidSync")
		_ = os.MkdirAll(dir, 0755)
		return filepath.Join(dir, "daemon_config.json")
	}
	return "daemon_config.json"
}

func generateNewPIN() string {
	r := rand.New(rand.NewSource(time.Now().UnixNano()))
	return fmt.Sprintf("%06d", r.Intn(1000000))
}

func NewSyncServer(port int, serverName string, clipManager *windows.ClipboardManager, trayManager *windows.TrayManager) *SyncServer {
	cfgPath := getDaemonConfigPath()
	var cfg DaemonConfig
	if data, err := os.ReadFile(cfgPath); err == nil {
		_ = json.Unmarshal(data, &cfg)
	}

	hostname, _ := os.Hostname()
	if cfg.MachineID == "" {
		r := rand.New(rand.NewSource(time.Now().UnixNano()))
		cfg.MachineID = fmt.Sprintf("win-%s-%x", hostname, r.Int63()&0xFFFFFF)
	}
	cfg.ServerName = serverName

	s := &SyncServer{
		port:          port,
		serverName:    serverName,
		machineID:     cfg.MachineID,
		pairingPIN:    generateNewPIN(),
		isPaired:      cfg.AuthToken != "",
		configPath:    cfgPath,
		config:        cfg,
		clipManager:   clipManager,
		trayManager:   trayManager,
		clients:       make(map[*websocket.Conn]bool),
		notifications: make([]protocol.NotificationPayload, 0, 50),
		smsMessages:   make([]protocol.SmsMessage, 0, 100),
	}
	s.saveConfig()
	return s
}

func (s *SyncServer) saveConfig() {
	s.configMu.Lock()
	defer s.configMu.Unlock()
	data, err := json.MarshalIndent(s.config, "", "  ")
	if err == nil {
		_ = os.WriteFile(s.configPath, data, 0644)
	}
}

func (s *SyncServer) saveAuthToken(token, deviceName string) {
	s.configMu.Lock()
	s.config.AuthToken = token
	s.config.DeviceName = deviceName
	s.config.PairedAt = time.Now().Unix()
	s.isPaired = true
	s.configMu.Unlock()
	s.saveConfig()
}

func (s *SyncServer) ResetPairing() {
	s.configMu.Lock()
	s.config.AuthToken = ""
	s.config.DeviceName = ""
	s.config.PairedAt = 0
	s.isPaired = false
	s.pairingPIN = generateNewPIN()
	s.configMu.Unlock()
	s.saveConfig()
	// Notify phone to unpair
	msg, _ := protocol.NewMessage(protocol.EventUnpair, map[string]string{"client_id": s.machineID})
	s.Broadcast(msg)
}


func getLocalIP() string {
	addrs, err := net.InterfaceAddrs()
	if err == nil {
		for _, addr := range addrs {
			if ipnet, ok := addr.(*net.IPNet); ok && !ipnet.IP.IsLoopback() && ipnet.IP.To4() != nil {
				return ipnet.IP.String()
			}
		}
	}
	return "127.0.0.1"
}

func (s *SyncServer) addNotification(n protocol.NotificationPayload) {
	s.notifsMu.Lock()
	defer s.notifsMu.Unlock()
	s.notifications = append([]protocol.NotificationPayload{n}, s.notifications...)
	if len(s.notifications) > 50 {
		s.notifications = s.notifications[:50]
	}
}

func (s *SyncServer) Start(ctx context.Context) error {
	mux := http.NewServeMux()
	mux.HandleFunc("/ws", s.handleWebSocket)

	// Status JSON API
	mux.HandleFunc("/status", func(w http.ResponseWriter, r *http.Request) {
		s.clientsMu.RLock()
		count := len(s.clients)
		dev := s.lastDeviceInfo
		pcMedia := s.lastPCMedia
		phoneMedia := s.lastPhoneMedia
		s.clientsMu.RUnlock()

		s.notifsMu.RLock()
		notifsCopy := make([]protocol.NotificationPayload, len(s.notifications))
		copy(notifsCopy, s.notifications)
		s.notifsMu.RUnlock()

		s.callMu.RLock()
		callState := s.lastCallState
		callStart := s.activeCallStart
		s.callMu.RUnlock()

		s.smsMu.RLock()
		smsCount := len(s.smsMessages)
		s.smsMu.RUnlock()

		lastClip, _ := s.clipManager.GetClipboard()

		var callDurationSec int64 = 0
		if callState != nil && callState.State == "OFFHOOK" && !callStart.IsZero() {
			callDurationSec = int64(time.Since(callStart).Seconds())
		}

		s.configMu.RLock()
		isPaired := s.isPaired
		pin := s.pairingPIN
		mID := s.machineID
		pairedDev := s.config.DeviceName
		s.configMu.RUnlock()

		w.Header().Set("Content-Type", "application/json")
		statusResp := map[string]any{
			"status":             "OK",
			"clients_count":      count,
			"connected":          count > 0,
			"local_ip":           getLocalIP(),
			"port":               s.port,
			"notifications":      notifsCopy,
			"clipboard":          lastClip,
			"sms_count":          smsCount,
			"call_duration_sec":  callDurationSec,
			"is_paired":          isPaired,
			"pairing_pin":        pin,
			"machine_id":         mID,
			"paired_device_name": pairedDev,
		}
		if dev != nil && count > 0 {
			statusResp["device_name"] = dev.DeviceName
			statusResp["model"] = dev.Model
			statusResp["battery_level"] = dev.BatteryLevel
			statusResp["is_charging"] = dev.IsCharging
		}
		if pcMedia != nil {
			statusResp["pc_media"] = pcMedia
		}
		if phoneMedia != nil {
			statusResp["phone_media"] = phoneMedia
		}
		if callState != nil {
			statusResp["call_state"] = callState
		}
		_ = json.NewEncoder(w).Encode(statusResp)
	})

	// Pair Reset API
	mux.HandleFunc("/pair/reset", func(w http.ResponseWriter, r *http.Request) {
		s.ResetPairing()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"success":     true,
			"pairing_pin": s.pairingPIN,
		})
	})

	// Call Action API (Answer, Reject, Hangup, Dial, Speaker, Mute)
	mux.HandleFunc("/call/action", func(w http.ResponseWriter, r *http.Request) {
		action := r.URL.Query().Get("action")
		number := r.URL.Query().Get("number")
		valStr := r.URL.Query().Get("value")

		var valPtr *bool
		if valStr != "" {
			b := valStr == "true" || valStr == "1"
			valPtr = &b
		}
		var numPtr *string
		if number != "" {
			numPtr = &number
		}

		log.Printf("[Çağrı] Komut iletiliyor: %s (Numara: %s)", action, number)
		s.SendCallAction(action, numPtr, valPtr)

		if action == "ANSWER" {
			s.callMu.Lock()
			s.activeCallStart = time.Now()
			if s.lastCallState != nil {
				s.lastCallState.State = "OFFHOOK"
			}
			s.callMu.Unlock()
		} else if action == "REJECT" || action == "HANGUP" {
			s.callMu.Lock()
			if s.lastCallState != nil {
				s.lastCallState.State = "IDLE"
			}
			s.activeCallStart = time.Time{}
			s.callMu.Unlock()
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "action": action})
	})

	// SMS List API
	mux.HandleFunc("/sms/list", func(w http.ResponseWriter, r *http.Request) {
		s.smsMu.RLock()
		msgsCopy := make([]protocol.SmsMessage, len(s.smsMessages))
		copy(msgsCopy, s.smsMessages)
		s.smsMu.RUnlock()

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"messages": msgsCopy,
			"count":    len(msgsCopy),
		})
	})

	// SMS Sync Request API
	mux.HandleFunc("/sms/sync", func(w http.ResponseWriter, r *http.Request) {
		s.RequestSmsSync()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "status": "sync_requested"})
	})

	// SMS Send API
	mux.HandleFunc("/sms/send", func(w http.ResponseWriter, r *http.Request) {
		recipient := r.URL.Query().Get("recipient")
		body := r.URL.Query().Get("body")

		if r.Method == http.MethodPost {
			_ = r.ParseForm()
			if rRec := r.FormValue("recipient"); rRec != "" {
				recipient = rRec
			}
			if rBody := r.FormValue("body"); rBody != "" {
				body = rBody
			}
		}

		if recipient == "" || body == "" {
			http.Error(w, "recipient and body required", http.StatusBadRequest)
			return
		}

		log.Printf("[SMS] Gönderiliyor: -> %s: %s", recipient, body)
		s.SendSms(recipient, body)

		// Optimistic add to local SMS cache
		newMsg := protocol.SmsMessage{
			ID:         fmt.Sprintf("local_%d", time.Now().UnixMilli()),
			ThreadID:   0,
			Address:    recipient,
			Body:       body,
			Timestamp:  time.Now().UnixMilli(),
			IsIncoming: false,
			Read:       true,
		}
		s.smsMu.Lock()
		s.smsMessages = append([]protocol.SmsMessage{newMsg}, s.smsMessages...)
		s.smsMu.Unlock()

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "recipient": recipient})
	})

	// Phone command endpoint
	mux.HandleFunc("/phone/command", func(w http.ResponseWriter, r *http.Request) {
		action := r.URL.Query().Get("action")
		if action == "" {
			http.Error(w, "missing action", http.StatusBadRequest)
			return
		}
		pctStr := r.URL.Query().Get("percent")
		var pct float64 = 0
		if pctStr != "" {
			_, _ = fmt.Sscanf(pctStr, "%f", &pct)
		}
		log.Printf("[Server] Telefondan işlem istendi: %s (Percent: %.1f)", action, pct)
		s.SendPhoneCommand(action, pct)
		w.Header().Set("Content-Type", "text/plain")
		fmt.Fprintf(w, "OK\n")
	})

	// PC media command endpoint
	mux.HandleFunc("/pc/command", func(w http.ResponseWriter, r *http.Request) {
		action := r.URL.Query().Get("action")
		if action == "" {
			http.Error(w, "missing action", http.StatusBadRequest)
			return
		}
		pctStr := r.URL.Query().Get("percent")
		if action == "SEEK_PERCENT" || (pctStr != "" && action == "SEEK") {
			var pct float64
			_, _ = fmt.Sscanf(pctStr, "%f", &pct)
			_ = windows.ExecuteMediaSeekPercent(pct)
		} else if action == "SEEK_FORWARD" {
			_ = windows.ExecuteMediaSeekRelative(15)
		} else if action == "SEEK_BACKWARD" {
			_ = windows.ExecuteMediaSeekRelative(-15)
		} else {
			_ = windows.ExecuteMediaAction(action)
		}
		w.Header().Set("Content-Type", "text/plain")
		fmt.Fprintf(w, "OK\n")
	})

	// Send clipboard text to phone
	mux.HandleFunc("/clipboard/send", func(w http.ResponseWriter, r *http.Request) {
		text := r.URL.Query().Get("text")
		if r.Method == http.MethodPost {
			_ = r.ParseForm()
			if pText := r.FormValue("text"); pText != "" {
				text = pText
			}
		}
		if text != "" {
			_ = s.clipManager.SetClipboard(text)
			msg, _ := protocol.NewMessage(protocol.EventClipboard, protocol.ClipboardPayload{
				Text:      text,
				Timestamp: time.Now().UnixMilli(),
			})
			s.Broadcast(msg)
			log.Printf("[Pano] Web arayüzünden panoya ve telefona iletildi (%d bayt)", len(text))
		}
		w.Header().Set("Content-Type", "text/plain")
		fmt.Fprintf(w, "OK\n")
	})

	// APK download endpoint
	mux.HandleFunc("/download", func(w http.ResponseWriter, r *http.Request) {
		candidates := []string{
			"MacSync.apk",
			"../MacSync.apk",
			"android-app/app/build/outputs/apk/debug/app-debug.apk",
			"../android-app/app/build/outputs/apk/debug/app-debug.apk",
			"AndroidSync.apk",
		}
		for _, path := range candidates {
			if _, err := os.Stat(path); err == nil {
				w.Header().Set("Content-Type", "application/vnd.android.package-archive")
				w.Header().Set("Content-Disposition", "attachment; filename=\"AndroidSync.apk\"")
				http.ServeFile(w, r, path)
				return
			}
		}
		http.Error(w, "AndroidSync.apk henüz derlenmedi veya bulunamadı", http.StatusNotFound)
	})

	// Web Dashboard
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprint(w, dashboardHTML)
	})

	httpServer := &http.Server{
		Addr:    fmt.Sprintf(":%d", s.port),
		Handler: mux,
	}

	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = httpServer.Shutdown(shutdownCtx)
	}()

	log.Printf("[Server] WebSocket ve HTTP sunucusu :%d/ portunda dinlemede", s.port)
	if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return err
	}
	return nil
}

func (s *SyncServer) handleWebSocket(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("[Server] WebSocket yükseltme hatası: %v", err)
		return
	}
	defer conn.Close()

	s.clientsMu.Lock()
	s.clients[conn] = true
	s.clientsMu.Unlock()

	log.Printf("[Server] 📱 Android cihaz bağlandı: %s", conn.RemoteAddr())
	if s.trayManager != nil {
		s.trayManager.UpdateStatus("Android Cihaz Bağlandı 🟢")
	}

	// Welcome greeting
	welcome, _ := protocol.NewMessage(protocol.EventDeviceInfo, map[string]string{
		"server_name": "Windows PC",
		"status":      "connected",
	})
	_ = conn.WriteJSON(welcome)

	// Send current PC media if available
	s.clientsMu.RLock()
	curPCMedia := s.lastPCMedia
	s.clientsMu.RUnlock()
	if curPCMedia != nil {
		if initialMedia, err := protocol.NewMessage(protocol.EventMediaInfo, curPCMedia); err == nil {
			_ = conn.WriteJSON(initialMedia)
		}
	}

	// 1. Güvenlik & Eşleştirme İsteği Gönder
	s.configMu.RLock()
	token := s.config.AuthToken
	pin := s.pairingPIN
	mID := s.machineID
	sName := s.serverName
	isPaired := s.isPaired
	s.configMu.RUnlock()

	authReq, _ := protocol.NewMessage(protocol.EventAuthRequest, protocol.AuthRequestPayload{
		ClientID:   mID,
		ClientName: sName,
		OS:         "windows",
		AuthToken:  token,
		PairingPin: pin,
	})
	_ = conn.WriteJSON(authReq)

	if isPaired {
		// Zaten eşleşmişse SMS senkronizasyonu başlat
		s.RequestSmsSync()
	} else {
		log.Printf("[Güvenlik] ⚠️ Cihaz henüz eşleşmemiş. Eşleştirme Kodu: %s", pin)
	}

	defer func() {
		s.clientsMu.Lock()
		delete(s.clients, conn)
		remaining := len(s.clients)
		s.clientsMu.Unlock()
		log.Printf("[Server] 📱 Android cihaz ayrıldı: %s", conn.RemoteAddr())
		if remaining == 0 && s.trayManager != nil {
			s.trayManager.UpdateStatus("Bağlantı Aranıyor 🟡")
		}
	}()

	for {
		var msg protocol.Message
		err := conn.ReadJSON(&msg)
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
				log.Printf("[Server] Okuma hatası: %v", err)
			}
			break
		}

		s.processMessage(&msg)
	}
}

func (s *SyncServer) processMessage(msg *protocol.Message) {
	switch msg.Event {
	case protocol.EventAuthResponse:
		var p protocol.AuthResponsePayload
		if err := json.Unmarshal(msg.Payload, &p); err == nil {
			if p.Status == "AUTHORIZED" {
				s.configMu.Lock()
				s.isPaired = true
				s.configMu.Unlock()
				log.Printf("[Güvenlik] 🔒 Yetkilendirme başarılı! (%s)", p.ClientName)
				s.RequestSmsSync()
			} else {
				s.configMu.Lock()
				s.isPaired = false
				s.configMu.Unlock()
				log.Printf("[Güvenlik] ⚠️ Cihaz henüz eşleşmemiş! Eşleştirme Kodu: %s", s.pairingPIN)
			}
		}

	case protocol.EventPairConfirm:
		var p protocol.PairConfirmPayload
		if err := json.Unmarshal(msg.Payload, &p); err == nil {
			if p.Approved {
				s.saveAuthToken(p.AuthToken, p.DeviceName)
				log.Printf("[Güvenlik] 📱 Yeni eşleştirme onaylandı: %s (Token güvenle kaydedildi)", p.DeviceName)
				s.RequestSmsSync()
			} else {
				s.configMu.Lock()
				s.isPaired = false
				s.configMu.Unlock()
				log.Printf("[Güvenlik] ❌ Eşleştirme isteği kullanıcı tarafından reddedildi.")
			}
		}

	case protocol.EventUnpair:
		s.ResetPairing()
		log.Printf("[Güvenlik] 🔌 Eşleştirme kaldırıldı.")

	case protocol.EventDeviceInfo:
		var p protocol.DeviceInfoPayload
		if err := json.Unmarshal(msg.Payload, &p); err == nil {
			s.clientsMu.Lock()
			s.lastDeviceInfo = &p
			s.clientsMu.Unlock()
			log.Printf("[Cihaz] 📱 %s (%s) - Pil: %%%d (Şarjda: %v)", p.DeviceName, p.Model, p.BatteryLevel, p.IsCharging)
			if s.trayManager != nil {
				s.trayManager.UpdateStatus(fmt.Sprintf("%s (%%%d 🔋)", p.Model, p.BatteryLevel))
			}
		}

	case protocol.EventNotification, protocol.EventPCNotification:
		var p protocol.NotificationPayload
		if err := json.Unmarshal(msg.Payload, &p); err == nil {
			log.Printf("[Bildirim] [%s] %s: %s", p.AppName, p.Title, p.Text)
			s.addNotification(p)
			_ = windows.ShowToast(p.Title, p.Text, p.AppName)
		}

	case protocol.EventCallState:
		var p protocol.CallStatePayload
		if err := json.Unmarshal(msg.Payload, &p); err == nil {
			s.callMu.Lock()
			s.lastCallState = &p
			if p.State == "OFFHOOK" && s.activeCallStart.IsZero() {
				s.activeCallStart = time.Now()
			} else if p.State == "IDLE" {
				s.activeCallStart = time.Time{}
			}
			s.callMu.Unlock()

			log.Printf("[Arama] Durum: %s, Numara: %s, Kişi: %s", p.State, p.PhoneNumber, p.CallerName)
			if p.State == "RINGING" {
				windows.PauseAllMedia() // Telefon çalınca PC medyasını otomatik duraklat
				_ = windows.ShowCallAlert(p.CallerName, p.PhoneNumber)
			} else if p.State == "IDLE" || p.State == "OFFHOOK" {
				windows.DismissCallAlert()
			}
		}

	case protocol.EventSmsSyncResponse:
		var p protocol.SmsSyncPayload
		if err := json.Unmarshal(msg.Payload, &p); err == nil {
			s.smsMu.Lock()
			s.smsMessages = p.Messages
			s.smsMu.Unlock()
			log.Printf("[SMS] %d adet SMS mesajı telefondan senkronize edildi", len(p.Messages))
		}

	case protocol.EventSmsNewMessage:
		var msgItem protocol.SmsMessage
		if err := json.Unmarshal(msg.Payload, &msgItem); err == nil {
			s.smsMu.Lock()
			s.smsMessages = append([]protocol.SmsMessage{msgItem}, s.smsMessages...)
			s.smsMu.Unlock()

			senderName := msgItem.ContactName
			if senderName == "" {
				senderName = msgItem.Address
			}
			log.Printf("[SMS Yeni] [%s]: %s", senderName, msgItem.Body)
			_ = windows.ShowToast("Yeni Mesaj: "+senderName, msgItem.Body, "SMS")
		}

	case protocol.EventSmsSentStatus:
		var status protocol.SmsSentStatusPayload
		if err := json.Unmarshal(msg.Payload, &status); err == nil {
			log.Printf("[SMS Durumu] Gönderim: %v -> %s", status.Success, status.Recipient)
		}

	case protocol.EventMediaInfo:
		var p protocol.MediaInfoPayload
		if err := json.Unmarshal(msg.Payload, &p); err == nil {
			p.Source = "phone"
			s.clientsMu.Lock()
			s.lastPhoneMedia = &p
			s.clientsMu.Unlock()
			log.Printf("[Medya] 📱 Telefondaki Medya: %s - %s", p.Title, p.Artist)
		}

	case protocol.EventMediaCommand:
		var p protocol.MediaCommandPayload
		if err := json.Unmarshal(msg.Payload, &p); err == nil {
			log.Printf("[Medya Komutu] %s (Percent: %.1f)", p.Action, p.Percent)
			if p.Action == "SEEK_PERCENT" {
				_ = windows.ExecuteMediaSeekPercent(p.Percent)
			} else if p.Action == "SEEK_FORWARD" {
				_ = windows.ExecuteMediaSeekRelative(15)
			} else if p.Action == "SEEK_BACKWARD" {
				_ = windows.ExecuteMediaSeekRelative(-15)
			} else {
				_ = windows.ExecuteMediaAction(p.Action)
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

// SendCallAction sends answer/reject/hangup/dial commands to phone.
func (s *SyncServer) SendCallAction(action string, number *string, value *bool) {
	msg, err := protocol.NewMessage(protocol.EventCallAction, protocol.CallActionPayload{
		Action: action,
		Number: number,
		Value:  value,
	})
	if err == nil {
		s.Broadcast(msg)
	}
}

// SendSms sends a new SMS message request to phone.
func (s *SyncServer) SendSms(recipient, body string) {
	msg, err := protocol.NewMessage(protocol.EventSmsSend, protocol.SmsSendPayload{
		Recipient: recipient,
		Body:      body,
	})
	if err == nil {
		s.Broadcast(msg)
	}
}

// RequestSmsSync requests the latest SMS inbox and threads from phone.
func (s *SyncServer) RequestSmsSync() {
	msg, err := protocol.NewMessage(protocol.EventSmsSyncRequest, map[string]string{"type": "full"})
	if err == nil {
		s.Broadcast(msg)
	}
}

// SendPhoneCommand sends a command to all connected Android phones (media, volume, ring).
func (s *SyncServer) SendPhoneCommand(action string, percent ...float64) {
	pct := 0.0
	if len(percent) > 0 {
		pct = percent[0]
	}
	msg, err := protocol.NewMessage(protocol.EventPhoneCommand, protocol.PhoneCommandPayload{
		Action:  action,
		Percent: pct,
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
			log.Printf("[Server] Broadcast hatası: %v", err)
		}
	}
}

// UpdatePCMedia updates stored PC media and broadcasts to Android phone.
func (s *SyncServer) UpdatePCMedia(info protocol.MediaInfoPayload) {
	if info.Title == "" && info.Artist == "" {
		return
	}
	info.Source = "windows"
	s.clientsMu.Lock()
	prev := s.lastPCMedia
	s.lastPCMedia = &info
	s.clientsMu.Unlock()

	diffTrack := prev == nil || prev.Title != info.Title || prev.Artist != info.Artist || prev.IsPlaying != info.IsPlaying
	diffPos := prev == nil || (info.PositionMs/1000) != (prev.PositionMs/1000)

	if diffTrack {
		log.Printf("[Medya] 💻 PC'den medya: %s - %s (Çalıyor: %v)", info.Title, info.Artist, info.IsPlaying)
	}

	if diffTrack || (info.IsPlaying && diffPos) {
		if msg, err := protocol.NewMessage(protocol.EventMediaInfo, info); err == nil {
			s.Broadcast(msg)
		}
	}
}

// BroadcastPCNotification broadcasts an incoming Windows notification to connected phones and updates dashboard.
func (s *SyncServer) BroadcastPCNotification(p protocol.NotificationPayload) {
	log.Printf("[PC Bildirimi] 💻 [%s] %s: %s -> Telefona iletiliyor", p.AppName, p.Title, p.Text)
	s.addNotification(p)
	if msg, err := protocol.NewMessage(protocol.EventPCNotification, p); err == nil {
		s.Broadcast(msg)
	}
}

const dashboardHTML = `<!DOCTYPE html>
<html lang="tr">
<head>
    <meta charset="UTF-8">
    <meta name="viewport" content="width=device-width, initial-scale=1.0">
    <title>Android Sync | Windows Kontrol Paneli</title>
    <link rel="preconnect" href="https://fonts.googleapis.com">
    <link rel="preconnect" href="https://fonts.gstatic.com" crossorigin>
    <link href="https://fonts.googleapis.com/css2?family=Plus+Jakarta+Sans:wght@300;400;500;600;700;800&family=JetBrains+Mono:wght@400;500&display=swap" rel="stylesheet">
    <style>
        :root {
            --bg-base: #080B11;
            --bg-card: rgba(18, 24, 38, 0.72);
            --bg-card-hover: rgba(26, 35, 56, 0.82);
            --bg-input: rgba(12, 16, 26, 0.85);
            --border-card: rgba(255, 255, 255, 0.08);
            --border-glow: rgba(56, 189, 248, 0.35);
            --text-primary: #F8FAFC;
            --text-secondary: #94A3B8;
            --text-muted: #64748B;
            --accent-blue: #38BDF8;
            --accent-indigo: #6366F1;
            --accent-green: #10B981;
            --accent-red: #F43F5E;
            --accent-yellow: #F59E0B;
            --glow-cyan: rgba(56, 189, 248, 0.25);
            --radius-xl: 24px;
            --radius-lg: 16px;
            --radius-md: 12px;
            --radius-sm: 8px;
        }

        * { box-sizing: border-box; margin: 0; padding: 0; font-family: 'Plus Jakarta Sans', sans-serif; }

        body {
            background-color: var(--bg-base);
            background-image: 
                radial-gradient(circle at 10% 10%, rgba(56, 189, 248, 0.09) 0%, transparent 45%),
                radial-gradient(circle at 90% 90%, rgba(99, 102, 241, 0.09) 0%, transparent 45%);
            color: var(--text-primary);
            min-height: 100vh;
            display: flex;
            overflow-x: hidden;
        }

        /* App Container & Sidebar */
        .app-layout {
            display: flex;
            width: 100vw;
            height: 100vh;
            overflow: hidden;
        }

        .sidebar {
            width: 260px;
            background: rgba(13, 17, 27, 0.95);
            border-right: 1px solid var(--border-card);
            backdrop-filter: blur(24px);
            display: flex;
            flex-direction: column;
            padding: 24px 16px;
            gap: 24px;
            flex-shrink: 0;
        }

        .brand-box {
            display: flex;
            align-items: center;
            gap: 12px;
            padding: 4px 8px;
        }

        .brand-logo {
            width: 42px;
            height: 42px;
            background: linear-gradient(135deg, #0284C7, #6366F1);
            border-radius: var(--radius-md);
            display: flex;
            align-items: center;
            justify-content: center;
            font-size: 22px;
            box-shadow: 0 4px 16px var(--glow-cyan);
        }

        .brand-text h2 {
            font-size: 16px;
            font-weight: 800;
            background: linear-gradient(to right, #F8FAFC, #94A3B8);
            -webkit-background-clip: text;
            -webkit-text-fill-color: transparent;
        }

        .brand-text span {
            font-size: 11px;
            color: var(--text-muted);
            font-weight: 600;
        }

        /* Nav menu */
        .nav-list {
            display: flex;
            flex-direction: column;
            gap: 6px;
            list-style: none;
            flex: 1;
        }

        .nav-item {
            display: flex;
            align-items: center;
            gap: 12px;
            padding: 12px 16px;
            border-radius: var(--radius-md);
            color: var(--text-secondary);
            font-size: 14px;
            font-weight: 600;
            cursor: pointer;
            transition: all 0.2s ease;
            position: relative;
        }

        .nav-item:hover {
            color: var(--text-primary);
            background: rgba(255, 255, 255, 0.05);
        }

        .nav-item.active {
            color: #38BDF8;
            background: rgba(56, 189, 248, 0.12);
            border: 1px solid rgba(56, 189, 248, 0.25);
        }

        .nav-badge {
            margin-left: auto;
            background: rgba(56, 189, 248, 0.2);
            color: #38BDF8;
            font-size: 11px;
            font-weight: 700;
            padding: 2px 8px;
            border-radius: 99px;
        }

        /* Main Content */
        .main-content {
            flex: 1;
            display: flex;
            flex-direction: column;
            height: 100vh;
            overflow-y: auto;
            background: transparent;
        }

        /* Topbar */
        .topbar {
            height: 70px;
            border-bottom: 1px solid var(--border-card);
            background: rgba(13, 17, 27, 0.65);
            backdrop-filter: blur(20px);
            padding: 0 32px;
            display: flex;
            align-items: center;
            justify-content: space-between;
            position: sticky;
            top: 0;
            z-index: 10;
        }

        .page-title {
            font-size: 18px;
            font-weight: 700;
            color: var(--text-primary);
        }

        .topbar-actions {
            display: flex;
            align-items: center;
            gap: 16px;
        }

        .status-pill {
            display: flex;
            align-items: center;
            gap: 8px;
            background: rgba(16, 185, 129, 0.12);
            border: 1px solid rgba(16, 185, 129, 0.3);
            color: var(--accent-green);
            padding: 6px 14px;
            border-radius: 9999px;
            font-size: 12px;
            font-weight: 700;
        }

        .status-pill.disconnected {
            background: rgba(245, 158, 11, 0.12);
            border-color: rgba(245, 158, 11, 0.3);
            color: var(--accent-yellow);
        }

        .status-dot {
            width: 8px;
            height: 8px;
            border-radius: 50%;
            background-color: currentColor;
            box-shadow: 0 0 10px currentColor;
            animation: pulse 2s infinite;
        }

        @keyframes pulse {
            0%, 100% { opacity: 1; transform: scale(1); }
            50% { opacity: 0.4; transform: scale(0.9); }
        }

        /* Tab Panes */
        .tab-content {
            padding: 32px;
            flex: 1;
            display: none;
        }

        .tab-content.active {
            display: block;
        }

        /* Cards */
        .card {
            background: var(--bg-card);
            border: 1px solid var(--border-card);
            backdrop-filter: blur(24px);
            border-radius: var(--radius-xl);
            padding: 24px;
            box-shadow: 0 12px 36px rgba(0, 0, 0, 0.25);
            transition: all 0.25s ease;
        }

        .card:hover {
            border-color: var(--border-glow);
        }

        /* Buttons */
        .btn {
            background: rgba(255, 255, 255, 0.08);
            border: 1px solid rgba(255, 255, 255, 0.12);
            color: var(--text-primary);
            padding: 10px 18px;
            border-radius: var(--radius-md);
            font-size: 13px;
            font-weight: 600;
            cursor: pointer;
            transition: all 0.2s ease;
            display: inline-flex;
            align-items: center;
            justify-content: center;
            gap: 8px;
            text-decoration: none;
        }

        .btn:hover {
            background: rgba(255, 255, 255, 0.16);
            transform: translateY(-1px);
        }

        .btn-primary {
            background: linear-gradient(135deg, #0284C7, #2563EB);
            border: none;
            color: white;
            box-shadow: 0 4px 16px var(--glow-cyan);
        }

        .btn-primary:hover {
            background: linear-gradient(135deg, #0369A1, #1D4ED8);
        }

        .btn-success {
            background: linear-gradient(135deg, #059669, #10B981);
            border: none;
            color: white;
        }

        .btn-danger {
            background: linear-gradient(135deg, #E11D48, #F43F5E);
            border: none;
            color: white;
        }

        /* Incoming Call Modal Overlay */
        .call-overlay {
            position: fixed;
            top: 24px;
            right: 24px;
            z-index: 1000;
            background: rgba(18, 24, 38, 0.95);
            border: 2px solid #38BDF8;
            border-radius: var(--radius-xl);
            padding: 24px;
            box-shadow: 0 20px 50px rgba(0, 0, 0, 0.6), 0 0 30px var(--glow-cyan);
            backdrop-filter: blur(30px);
            width: 360px;
            display: none;
            animation: slideIn 0.3s ease-out;
        }

        @keyframes slideIn {
            from { transform: translateY(-30px); opacity: 0; }
            to { transform: translateY(0); opacity: 1; }
        }

        .call-avatar {
            width: 64px;
            height: 64px;
            border-radius: 50%;
            background: linear-gradient(135deg, #38BDF8, #6366F1);
            display: flex;
            align-items: center;
            justify-content: center;
            font-size: 28px;
            margin: 0 auto 12px;
            animation: callPulse 1.5s infinite;
        }

        @keyframes callPulse {
            0%, 100% { box-shadow: 0 0 0 0 rgba(56, 189, 248, 0.6); }
            50% { box-shadow: 0 0 0 16px rgba(56, 189, 248, 0); }
        }

        .call-caller {
            text-align: center;
            font-size: 18px;
            font-weight: 700;
        }

        .call-number {
            text-align: center;
            font-size: 13px;
            color: var(--text-secondary);
            margin-top: 4px;
            margin-bottom: 20px;
        }

        .call-buttons {
            display: flex;
            gap: 12px;
        }

        /* Active Call Card */
        .active-call-box {
            background: linear-gradient(135deg, rgba(16, 185, 129, 0.1), rgba(6, 182, 212, 0.1));
            border: 1px solid rgba(16, 185, 129, 0.3);
            border-radius: var(--radius-lg);
            padding: 20px;
            display: none;
            margin-bottom: 24px;
        }

        /* Messenger SMS Layout */
        .chat-container {
            display: flex;
            height: calc(100vh - 140px);
            background: var(--bg-card);
            border: 1px solid var(--border-card);
            border-radius: var(--radius-xl);
            overflow: hidden;
        }

        .chat-sidebar {
            width: 320px;
            border-right: 1px solid var(--border-card);
            display: flex;
            flex-direction: column;
            background: rgba(13, 17, 27, 0.5);
        }

        .chat-search-bar {
            padding: 16px;
            border-bottom: 1px solid var(--border-card);
            display: flex;
            gap: 8px;
        }

        .search-input {
            width: 100%;
            background: var(--bg-input);
            border: 1px solid var(--border-card);
            border-radius: var(--radius-md);
            padding: 10px 14px;
            color: var(--text-primary);
            font-size: 13px;
            outline: none;
        }

        .search-input:focus {
            border-color: var(--accent-blue);
        }

        .threads-list {
            flex: 1;
            overflow-y: auto;
            list-style: none;
        }

        .thread-item {
            padding: 14px 18px;
            border-bottom: 1px solid rgba(255, 255, 255, 0.04);
            cursor: pointer;
            transition: all 0.2s;
            display: flex;
            align-items: center;
            gap: 12px;
        }

        .thread-item:hover {
            background: rgba(255, 255, 255, 0.05);
        }

        .thread-item.active {
            background: rgba(56, 189, 248, 0.12);
            border-left: 3px solid var(--accent-blue);
        }

        .thread-avatar {
            width: 42px;
            height: 42px;
            border-radius: 50%;
            background: linear-gradient(135deg, #4F46E5, #06B6D4);
            display: flex;
            align-items: center;
            justify-content: center;
            font-size: 16px;
            font-weight: 700;
            flex-shrink: 0;
        }

        .thread-info {
            flex: 1;
            overflow: hidden;
        }

        .thread-title-row {
            display: flex;
            justify-content: space-between;
            align-items: center;
            margin-bottom: 4px;
        }

        .thread-name {
            font-size: 14px;
            font-weight: 600;
            color: var(--text-primary);
            white-space: nowrap;
            overflow: hidden;
            text-overflow: ellipsis;
        }

        .thread-time {
            font-size: 11px;
            color: var(--text-muted);
        }

        .thread-preview {
            font-size: 12px;
            color: var(--text-secondary);
            white-space: nowrap;
            overflow: hidden;
            text-overflow: ellipsis;
        }

        /* Active Chat Pane */
        .chat-main {
            flex: 1;
            display: flex;
            flex-direction: column;
            background: rgba(18, 24, 38, 0.6);
        }

        .chat-header {
            height: 64px;
            border-bottom: 1px solid var(--border-card);
            padding: 0 24px;
            display: flex;
            align-items: center;
            justify-content: space-between;
        }

        .chat-recipient-info {
            display: flex;
            align-items: center;
            gap: 12px;
        }

        .chat-messages {
            flex: 1;
            padding: 24px;
            overflow-y: auto;
            display: flex;
            flex-direction: column;
            gap: 12px;
        }

        .msg-bubble {
            max-width: 65%;
            padding: 12px 16px;
            border-radius: var(--radius-lg);
            font-size: 14px;
            line-height: 1.5;
            position: relative;
            word-break: break-word;
        }

        .msg-bubble.incoming {
            align-self: flex-start;
            background: rgba(255, 255, 255, 0.07);
            border: 1px solid var(--border-card);
            color: var(--text-primary);
            border-bottom-left-radius: 4px;
        }

        .msg-bubble.outgoing {
            align-self: flex-end;
            background: linear-gradient(135deg, #0284C7, #2563EB);
            color: white;
            border-bottom-right-radius: 4px;
            box-shadow: 0 4px 14px rgba(2, 132, 199, 0.3);
        }

        .msg-meta {
            font-size: 10px;
            color: rgba(255, 255, 255, 0.6);
            margin-top: 4px;
            text-align: right;
        }

        .chat-input-bar {
            padding: 16px 24px;
            border-top: 1px solid var(--border-card);
            display: flex;
            gap: 12px;
            align-items: center;
        }

        .chat-input {
            flex: 1;
            background: var(--bg-input);
            border: 1px solid var(--border-card);
            border-radius: var(--radius-md);
            padding: 12px 18px;
            color: var(--text-primary);
            font-size: 14px;
            outline: none;
        }

        .chat-input:focus {
            border-color: var(--accent-blue);
        }

        /* Overview Grid */
        .overview-grid {
            display: grid;
            grid-template-columns: repeat(12, 1fr);
            gap: 24px;
        }

        .col-4 { grid-column: span 4; }
        .col-6 { grid-column: span 6; }
        .col-8 { grid-column: span 8; }
        .col-12 { grid-column: span 12; }

        @media (max-width: 1024px) {
            .col-4, .col-6, .col-8 { grid-column: span 12; }
            .sidebar { width: 80px; }
            .brand-text, .nav-item span { display: none; }
        }
    </style>
</head>
<body>
    <div class="app-layout">
        <!-- Sidebar Navigation -->
        <aside class="sidebar">
            <div class="brand-box">
                <div class="brand-logo">⚡</div>
                <div class="brand-text">
                    <h2>Android Sync</h2>
                    <span>WINDOWS DESKTOP</span>
                </div>
            </div>

            <ul class="nav-list">
                <li class="nav-item active" onclick="switchTab('overview')">
                    <span>⚡</span> <span>Genel Bakış</span>
                </li>
                <li class="nav-item" onclick="switchTab('calls')">
                    <span>📞</span> <span>Telefon &amp; Arama</span>
                    <span class="nav-badge" id="callNavBadge" style="display:none;">Çalıyor</span>
                </li>
                <li class="nav-item" onclick="switchTab('sms')">
                    <span>💬</span> <span>Mesajlar (SMS)</span>
                    <span class="nav-badge" id="smsNavBadge">0</span>
                </li>
                <li class="nav-item" onclick="switchTab('media')">
                    <span>🎵</span> <span>Medya &amp; Ses</span>
                </li>
                <li class="nav-item" onclick="switchTab('clipboard')">
                    <span>📋</span> <span>Ortak Pano</span>
                </li>
                <li class="nav-item" onclick="switchTab('notifications')">
                    <span>🔔</span> <span>Bildirimler</span>
                </li>
            </ul>

            <div style="margin-top: auto; padding: 12px; background: rgba(255,255,255,0.03); border-radius: var(--radius-md); font-size: 11px; color: var(--text-muted); text-align: center;">
                v2.0 • Windows 10/11 Yerel Motor
            </div>
        </aside>

        <!-- Main Content -->
        <main class="main-content">
            <!-- Topbar -->
            <header class="topbar">
                <div class="page-title" id="pageTitle">Genel Bakış</div>
                <div class="topbar-actions">
                    <button class="btn" onclick="ringPhone()" id="btnQuickRing">🔔 Telefonumu Çaldır</button>
                    <div id="pairingBadge" class="status-pill disconnected" style="margin-right:4px;">
                        <span>🔒</span> <span id="pairingText">Eşleşme Bekleniyor</span>
                    </div>
                    <div id="statusBadge" class="status-pill disconnected">
                        <div class="status-dot"></div>
                        <span id="statusText">Cihaz Aranıyor 🟡</span>
                    </div>
                </div>
            </header>

            <!-- Pairing Banner (Visible if not paired) -->
            <div id="pairingBanner" style="display:none; margin: 0 32px 20px; padding: 14px 20px; background: linear-gradient(135deg, rgba(245, 158, 11, 0.15), rgba(239, 68, 68, 0.15)); border: 1px solid rgba(245, 158, 11, 0.35); border-radius: var(--radius-lg); align-items: center; justify-content: space-between;">
                <div style="display:flex; align-items:center; gap: 14px;">
                    <span style="font-size: 26px;">🔒</span>
                    <div>
                        <div style="font-weight: 700; font-size: 14px; color: #F59E0B;">Güvenli Eşleştirme Bekleniyor</div>
                        <div style="font-size: 12px; color: var(--text-secondary); margin-top:2px;">Telefonunuzdaki onay penceresinde bu 6 haneli kodu kontrol edin: <strong id="bannerPairingPin" style="letter-spacing: 2px; font-size: 15px; color: #38BDF8; background: rgba(56,189,248,0.15); padding: 2px 8px; border-radius: 6px;">--- ---</strong></div>
                    </div>
                </div>
                <button class="btn btn-secondary" style="font-size: 11px; padding: 6px 12px;" onclick="resetPairing()">🔄 Yeni Kod Üret</button>
            </div>


            <!-- Floating Incoming Call Overlay -->
            <div id="incomingCallOverlay" class="call-overlay">
                <div class="call-avatar">📞</div>
                <div class="call-caller" id="incomingCallerName">Bilinmeyen Numara</div>
                <div class="call-number" id="incomingCallerNumber">Gelen Çağrı...</div>
                <div class="call-buttons">
                    <button class="btn btn-success" style="flex:1;" onclick="callAction('ANSWER')">📞 Cevapla</button>
                    <button class="btn btn-danger" style="flex:1;" onclick="callAction('REJECT')">❌ Reddet</button>
                </div>
            </div>

            <!-- TAB 1: Genel Bakış -->
            <div id="tab-overview" class="tab-content active">
                <!-- Active Call Bar if in call -->
                <div id="activeCallBar" class="active-call-box">
                    <div style="display:flex; justify-content:space-between; align-items:center;">
                        <div>
                            <div style="font-size:16px; font-weight:700; color:var(--accent-green);" id="activeCallTitle">🟢 Aktif Görüşme Devam Ediyor</div>
                            <div style="font-size:13px; color:var(--text-secondary); margin-top:2px;" id="activeCallTimer">Süre: 00:00</div>
                        </div>
                        <div style="display:flex; gap:10px;">
                            <button class="btn" onclick="callAction('SET_SPEAKER')">🔊 Hoparlör</button>
                            <button class="btn" onclick="callAction('SET_MUTE')">🎙️ Sessize Al</button>
                            <button class="btn btn-danger" onclick="callAction('HANGUP')">❌ Kapat</button>
                        </div>
                    </div>
                </div>

                <div class="overview-grid">
                    <!-- Device Card -->
                    <div class="card col-4">
                        <div style="display:flex; align-items:center; gap:16px; margin-bottom:20px;">
                            <div style="width:56px; height:56px; background:rgba(56,189,248,0.12); border-radius:50%; display:flex; align-items:center; justify-content:center; font-size:28px;">📱</div>
                            <div>
                                <h3 id="devName" style="font-size:18px; font-weight:800;">Bağlantı Aranıyor...</h3>
                                <p id="devSub" style="font-size:12px; color:var(--text-muted);">Wi-Fi eşleşmesi bekleniyor</p>
                            </div>
                        </div>
                        <div style="background:rgba(0,0,0,0.25); border-radius:var(--radius-md); padding:16px; margin-bottom:20px;">
                            <div style="display:flex; justify-content:space-between; font-size:13px; margin-bottom:8px;">
                                <span>Pil Durumu</span>
                                <strong id="devBattery">--%</strong>
                            </div>
                            <div style="width:100%; height:8px; background:rgba(255,255,255,0.1); border-radius:99px; overflow:hidden;">
                                <div id="devBatteryBar" style="width:0%; height:100%; background:linear-gradient(90deg, #10B981, #38BDF8); transition:width 0.5s;"></div>
                            </div>
                        </div>
                        <div style="display:flex; gap:8px;">
                            <button class="btn" style="flex:1;" onclick="sendPhoneCmd('VOLUME_UP')">🔊 Ses +</button>
                            <button class="btn" style="flex:1;" onclick="sendPhoneCmd('VOLUME_DOWN')">🔉 Ses -</button>
                            <button class="btn" style="flex:1;" onclick="sendPhoneCmd('MUTE')">🔇 Sessiz</button>
                        </div>
                    </div>

                    <!-- Media Card (Dual PC & Phone) -->
                    <div class="card col-8">
                        <div style="display:flex; justify-content:space-between; align-items:center; margin-bottom:14px;">
                            <h3 style="font-size:16px; font-weight:700;">🎵 Medya &amp; Süre Kontrolü (Tüm Cihazlar)</h3>
                            <button class="btn" style="padding:4px 10px; font-size:11px;" onclick="switchTab('media')">Detaylı Medya 🎧</button>
                        </div>

                        <div style="display:grid; grid-template-columns: 1fr 1fr; gap:16px;">
                            <!-- PC Media Box -->
                            <div style="background:rgba(0,0,0,0.3); border:1px solid rgba(56,189,248,0.25); border-radius:var(--radius-md); padding:16px;">
                                <div style="display:flex; justify-content:space-between; align-items:center; margin-bottom:8px;">
                                    <span style="font-size:11px; font-weight:800; color:var(--accent-blue);">💻 BU BİLGİSAYAR</span>
                                    <span id="pcMediaTag" style="font-size:10px; background:rgba(56,189,248,0.15); color:var(--accent-blue); padding:2px 6px; border-radius:4px; font-weight:700;">HAZIR</span>
                                </div>
                                <div style="display:flex; align-items:center; gap:10px; margin-bottom:10px;">
                                    <div style="width:40px; height:40px; border-radius:8px; background:linear-gradient(135deg, #0284C7, #38BDF8); display:flex; align-items:center; justify-content:center; font-size:18px;">🎵</div>
                                    <div style="flex:1; overflow:hidden;">
                                        <div id="pcTrackTitle" style="font-size:13px; font-weight:700; white-space:nowrap; overflow:hidden; text-overflow:ellipsis;">PC Medyası Yok</div>
                                        <div id="pcTrackArtist" style="font-size:11px; color:var(--text-secondary); white-space:nowrap; overflow:hidden; text-overflow:ellipsis;">Spotify / YouTube açın</div>
                                    </div>
                                </div>
                                <div style="margin-bottom:10px;">
                                    <input type="range" id="pcMediaSeek" min="0" max="100" value="0" style="width:100%; accent-color:#38BDF8; cursor:pointer;" onmousedown="isSeekingMedia=true" ontouchstart="isSeekingMedia=true" oninput="updateSeekTimePreview(this.value)" onchange="seekPCMedia(this.value)">
                                    <div style="display:flex; justify-content:space-between; font-size:10px; color:var(--text-secondary); margin-top:2px;">
                                        <span id="pcMediaCurTime">00:00</span>
                                        <span id="pcMediaTotalTime">00:00</span>
                                    </div>
                                </div>
                                <div style="display:flex; justify-content:center; gap:6px; flex-wrap:wrap;">
                                    <button class="btn" style="padding:6px 10px; font-size:11px;" onclick="seekRelative(-15)" title="15 Saniye Geri">⏪ 15s</button>
                                    <button class="btn" style="padding:6px 10px; font-size:11px;" onclick="sendMediaCmd('PREV')" title="Önceki Parça">⏮</button>
                                    <button class="btn btn-primary" style="padding:6px 14px; font-size:11px;" onclick="sendMediaCmd('PLAY_PAUSE')">⏯ Oynat</button>
                                    <button class="btn" style="padding:6px 10px; font-size:11px;" onclick="sendMediaCmd('NEXT')" title="Sonraki Parça">⏭</button>
                                    <button class="btn" style="padding:6px 10px; font-size:11px;" onclick="seekRelative(15)" title="15 Saniye İleri">⏩ 15s</button>
                                </div>
                            </div>

                            <!-- Phone Media Box -->
                            <div style="background:rgba(0,0,0,0.3); border:1px solid rgba(16,185,129,0.25); border-radius:var(--radius-md); padding:16px;">
                                <div style="display:flex; justify-content:space-between; align-items:center; margin-bottom:8px;">
                                    <span style="font-size:11px; font-weight:800; color:var(--accent-green);">📱 TELEFON</span>
                                    <span id="phoneMediaTag" style="font-size:10px; background:rgba(16,185,129,0.15); color:var(--accent-green); padding:2px 6px; border-radius:4px; font-weight:700;">HAZIR</span>
                                </div>
                                <div style="display:flex; align-items:center; gap:10px; margin-bottom:10px;">
                                    <div style="width:40px; height:40px; border-radius:8px; background:linear-gradient(135deg, #10B981, #059669); display:flex; align-items:center; justify-content:center; font-size:18px;">🎧</div>
                                    <div style="flex:1; overflow:hidden;">
                                        <div id="phoneTrackTitle" style="font-size:13px; font-weight:700; white-space:nowrap; overflow:hidden; text-overflow:ellipsis;">Telefon Medyası Yok</div>
                                        <div id="phoneTrackArtist" style="font-size:11px; color:var(--text-secondary); white-space:nowrap; overflow:hidden; text-overflow:ellipsis;">Telefonda müzik açın</div>
                                    </div>
                                </div>
                                <div style="margin-bottom:10px;">
                                    <input type="range" id="phoneMediaSeek" min="0" max="100" value="0" style="width:100%; accent-color:#10B981; cursor:pointer;" onmousedown="isSeekingPhoneMedia=true" ontouchstart="isSeekingPhoneMedia=true" oninput="updatePhoneSeekTimePreview(this.value)" onchange="seekPhoneMedia(this.value)">
                                    <div style="display:flex; justify-content:space-between; font-size:10px; color:var(--text-secondary); margin-top:2px;">
                                        <span id="phoneMediaCurTime">00:00</span>
                                        <span id="phoneMediaTotalTime">00:00</span>
                                    </div>
                                </div>
                                <div style="display:flex; justify-content:center; gap:6px; flex-wrap:wrap;">
                                    <button class="btn" style="padding:6px 10px; font-size:11px;" onclick="seekPhoneRelative(-15)" title="Telefonda 15s Geri">⏪ 15s</button>
                                    <button class="btn" style="padding:6px 10px; font-size:11px;" onclick="sendPhoneCmd('PREV')" title="Telefonda Önceki">⏮</button>
                                    <button class="btn btn-primary" style="padding:6px 14px; font-size:11px; background:#10B981;" onclick="sendPhoneCmd('PLAY_PAUSE')">⏯ Oynat</button>
                                    <button class="btn" style="padding:6px 10px; font-size:11px;" onclick="sendPhoneCmd('NEXT')" title="Telefonda Sonraki">⏭</button>
                                    <button class="btn" style="padding:6px 10px; font-size:11px;" onclick="seekPhoneRelative(15)" title="Telefonda 15s İleri">⏩ 15s</button>
                                </div>
                            </div>
                        </div>
                    </div>

                    <!-- Quick Notifications on Overview -->
                    <div class="card col-6">
                        <div style="display:flex; justify-content:space-between; align-items:center; margin-bottom:14px;">
                            <h3 style="font-size:15px; font-weight:700;">🔔 Son Bildirimler</h3>
                            <button class="btn" style="padding:4px 10px; font-size:12px;" onclick="switchTab('notifications')">Tümü</button>
                        </div>
                        <div id="quickNotifList" style="display:flex; flex-direction:column; gap:8px;">
                            <p style="color:var(--text-muted); font-size:13px;">Gelen bildirim bulunamadı.</p>
                        </div>
                    </div>

                    <!-- Quick SMS -->
                    <div class="card col-6">
                        <div style="display:flex; justify-content:space-between; align-items:center; margin-bottom:14px;">
                            <h3 style="font-size:15px; font-weight:700;">💬 Son Mesajlar</h3>
                            <button class="btn" style="padding:4px 10px; font-size:12px;" onclick="switchTab('sms')">Tümü</button>
                        </div>
                        <div id="quickSmsList" style="display:flex; flex-direction:column; gap:8px;">
                            <p style="color:var(--text-muted); font-size:13px;">Mesajlar senkronize ediliyor...</p>
                        </div>
                    </div>

                    <!-- Quick Clipboard -->
                    <div class="card col-12">
                        <div style="display:flex; justify-content:space-between; align-items:center; margin-bottom:14px;">
                            <h3 style="font-size:15px; font-weight:700;">📋 Ortak Pano (Clipboard)</h3>
                            <button class="btn" style="padding:4px 10px; font-size:12px;" onclick="copyClip()">Kopyala</button>
                        </div>
                        <div id="quickClipText" style="background:rgba(0,0,0,0.3); padding:14px; border-radius:var(--radius-sm); font-family:'JetBrains Mono',monospace; font-size:12px; min-height:60px; max-height:100px; overflow-y:auto; word-break:break-all;">Pano boş...</div>
                    </div>
                </div>
            </div>

            <!-- TAB 2: Telefon & Aramalar -->
            <div id="tab-calls" class="tab-content">
                <div class="overview-grid">
                    <div class="card col-6">
                        <h3 style="font-size:18px; font-weight:700; margin-bottom:16px;">📞 Hızlı Arama &amp; Tuş Takımı</h3>
                        <div style="display:flex; gap:10px; margin-bottom:20px;">
                            <input type="text" id="dialInput" class="search-input" placeholder="Aranacak numara veya kişi..." style="font-size:16px; font-family:'JetBrains Mono',monospace;">
                            <button class="btn btn-success" onclick="dialNumber()">📞 Ara</button>
                        </div>
                        <div style="display:grid; grid-template-columns:repeat(3, 1fr); gap:12px; max-width:320px; margin:0 auto 20px;">
                            <button class="btn" style="font-size:18px; padding:16px;" onclick="addDial('1')">1</button>
                            <button class="btn" style="font-size:18px; padding:16px;" onclick="addDial('2')">2</button>
                            <button class="btn" style="font-size:18px; padding:16px;" onclick="addDial('3')">3</button>
                            <button class="btn" style="font-size:18px; padding:16px;" onclick="addDial('4')">4</button>
                            <button class="btn" style="font-size:18px; padding:16px;" onclick="addDial('5')">5</button>
                            <button class="btn" style="font-size:18px; padding:16px;" onclick="addDial('6')">6</button>
                            <button class="btn" style="font-size:18px; padding:16px;" onclick="addDial('7')">7</button>
                            <button class="btn" style="font-size:18px; padding:16px;" onclick="addDial('8')">8</button>
                            <button class="btn" style="font-size:18px; padding:16px;" onclick="addDial('9')">9</button>
                            <button class="btn" style="font-size:18px; padding:16px;" onclick="addDial('*')">*</button>
                            <button class="btn" style="font-size:18px; padding:16px;" onclick="addDial('0')">0</button>
                            <button class="btn" style="font-size:18px; padding:16px;" onclick="addDial('#')">#</button>
                        </div>
                    </div>

                    <div class="card col-6">
                        <h3 style="font-size:18px; font-weight:700; margin-bottom:16px;">🎙️ Bilgisayar Üzerinden Konuşma</h3>
                        <p style="font-size:13px; color:var(--text-secondary); line-height:1.6; margin-bottom:16px;">
                            Telefonunuz çaldığında doğrudan bilgisayarınızın mikrofonu ve hoparlörü üzerinden konuşabilirsiniz.
                        </p>
                        <div style="background:rgba(56,189,248,0.08); border:1px solid rgba(56,189,248,0.25); border-radius:var(--radius-md); padding:16px; margin-bottom:16px;">
                            <div style="font-weight:700; font-size:14px; color:var(--accent-blue); margin-bottom:4px;">🎧 Bluetooth Hands-Free Modu</div>
                            <div style="font-size:12px; color:var(--text-secondary);">Windows ayarlarından telefonunuzu Bluetooth ile eşleştirdiğinizde ses 0 gecikmeyle PC mikrofon &amp; kulaklığınıza aktarılır.</div>
                        </div>
                        <div style="background:rgba(16,185,129,0.08); border:1px solid rgba(16,185,129,0.25); border-radius:var(--radius-md); padding:16px;">
                            <div style="font-weight:700; font-size:14px; color:var(--accent-green); margin-bottom:4px;">🌐 Wi-Fi Doğrudan Hoparlör Modu</div>
                            <div style="font-size:12px; color:var(--text-secondary);">Arama kabul edildiğinde telefon otomatik olarak yüksek sesli hoparlör moduna geçer.</div>
                        </div>
                    </div>
                </div>
            </div>

            <!-- TAB 3: Mesajlar (SMS Messenger) -->
            <div id="tab-sms" class="tab-content">
                <div class="chat-container">
                    <!-- Conversations Sidebar -->
                    <div class="chat-sidebar">
                        <div class="chat-search-bar">
                            <input type="text" id="smsSearch" class="search-input" placeholder="Sohbet ara..." oninput="filterSms()">
                            <button class="btn btn-primary" style="padding:8px 12px;" onclick="newChatModal()">+</button>
                        </div>
                        <ul id="threadsList" class="threads-list">
                            <li style="padding:20px; text-align:center; color:var(--text-muted); font-size:13px;">Mesajlar yükleniyor...</li>
                        </ul>
                    </div>

                    <!-- Active Chat Area -->
                    <div class="chat-main">
                        <div class="chat-header">
                            <div class="chat-recipient-info">
                                <div class="thread-avatar" id="activeChatAvatar">👤</div>
                                <div>
                                    <div style="font-weight:700; font-size:15px;" id="activeChatName">Sohbet Seçin</div>
                                    <div style="font-size:12px; color:var(--text-muted);" id="activeChatNumber">SMS ile Senkron</div>
                                </div>
                            </div>
                            <button class="btn" onclick="dialActiveChat()">📞 Ara</button>
                        </div>

                        <div id="chatMessages" class="chat-messages">
                            <div style="margin:auto; text-align:center; color:var(--text-muted); font-size:14px;">
                                Sol listeden bir konuşma seçin veya yeni mesaj oluşturun.
                            </div>
                        </div>

                        <div class="chat-input-bar">
                            <input type="text" id="chatInput" class="chat-input" placeholder="Bir mesaj yazın (Enter ile gönder)..." onkeypress="if(event.key==='Enter') sendChatMsg()">
                            <button class="btn btn-primary" onclick="sendChatMsg()">Gönder 🚀</button>
                        </div>
                    </div>
                </div>
            </div>

            <!-- TAB 4: Medya & Ses -->
            <div id="tab-media" class="tab-content">
                <div class="overview-grid">
                    <!-- PC Media Card (Full) -->
                    <div class="card col-6">
                        <div style="display:flex; justify-content:space-between; align-items:center; margin-bottom:16px;">
                            <h3 style="font-size:17px; font-weight:700;">💻 Bu Bilgisayarda Çalan Medya (Windows)</h3>
                            <span id="fullPCTag" style="font-size:11px; background:rgba(56,189,248,0.15); color:var(--accent-blue); padding:3px 8px; border-radius:4px; font-weight:700;">HAZIR</span>
                        </div>
                        <div style="display:flex; align-items:center; gap:20px; background:rgba(0,0,0,0.3); padding:20px; border-radius:var(--radius-lg); margin-bottom:16px;">
                            <div style="width:72px; height:72px; border-radius:var(--radius-md); background:linear-gradient(135deg, #0284C7, #38BDF8); display:flex; align-items:center; justify-content:center; font-size:32px; box-shadow:0 6px 24px rgba(56,189,248,0.25);">🎵</div>
                            <div style="flex:1; overflow:hidden;">
                                <div style="font-size:11px; font-weight:700; color:var(--accent-blue); text-transform:uppercase; margin-bottom:4px;">WINDOWS GSMTC MEDYA</div>
                                <h3 id="fullPCTitle" style="font-size:18px; font-weight:800; white-space:nowrap; overflow:hidden; text-overflow:ellipsis;">PC Medyası Yok</h3>
                                <p id="fullPCArtist" style="font-size:13px; color:var(--text-secondary); margin-top:2px; white-space:nowrap; overflow:hidden; text-overflow:ellipsis;">Spotify, YouTube veya müzik açın</p>
                            </div>
                        </div>

                        <!-- Scrubber in Full Media Tab -->
                        <div style="margin-bottom:16px; background:rgba(0,0,0,0.2); padding:14px; border-radius:var(--radius-md);">
                            <input type="range" id="fullMediaSeek" min="0" max="100" value="0" style="width:100%; accent-color:#38BDF8; cursor:pointer;" onmousedown="isSeekingMedia=true" ontouchstart="isSeekingMedia=true" oninput="updateSeekTimePreview(this.value)" onchange="seekPCMedia(this.value)">
                            <div style="display:flex; justify-content:space-between; font-size:11px; color:var(--text-secondary); margin-top:4px;">
                                <span id="fullMediaCurTime">00:00</span>
                                <span id="fullMediaTotalTime">00:00</span>
                            </div>
                        </div>

                        <div style="display:flex; justify-content:center; gap:8px; flex-wrap:wrap; margin-bottom:12px;">
                            <button class="btn" style="padding:10px 18px; font-size:13px;" onclick="seekRelative(-15)">⏪ 15s Geri</button>
                            <button class="btn" style="padding:10px 18px; font-size:13px;" onclick="sendMediaCmd('PREV')">⏮ Önceki</button>
                            <button class="btn btn-primary" style="padding:10px 24px; font-size:14px;" onclick="sendMediaCmd('PLAY_PAUSE')">⏯ Oynat / Duraklat</button>
                            <button class="btn" style="padding:10px 18px; font-size:13px;" onclick="sendMediaCmd('NEXT')">⏭ Sonraki</button>
                            <button class="btn" style="padding:10px 18px; font-size:13px;" onclick="seekRelative(15)">⏩ 15s İleri</button>
                        </div>
                        <div style="display:flex; justify-content:center; gap:10px;">
                            <button class="btn" style="padding:8px 16px; font-size:12px;" onclick="sendMediaCmd('VOLUME_DOWN')">🔉 PC Ses -</button>
                            <button class="btn" style="padding:8px 16px; font-size:12px;" onclick="sendMediaCmd('VOLUME_UP')">🔊 PC Ses +</button>
                            <button class="btn" style="padding:8px 16px; font-size:12px;" onclick="sendMediaCmd('MUTE')">🔇 Sessiz</button>
                        </div>
                    </div>

                    <!-- Phone Media Card (Full) -->
                    <div class="card col-6">
                        <div style="display:flex; justify-content:space-between; align-items:center; margin-bottom:16px;">
                            <h3 style="font-size:17px; font-weight:700;">📱 Telefonda Çalan Medya (Android)</h3>
                            <span id="fullPhoneTag" style="font-size:11px; background:rgba(16,185,129,0.15); color:var(--accent-green); padding:3px 8px; border-radius:4px; font-weight:700;">HAZIR</span>
                        </div>
                        <div style="display:flex; align-items:center; gap:20px; background:rgba(0,0,0,0.3); padding:20px; border-radius:var(--radius-lg); margin-bottom:16px;">
                            <div style="width:72px; height:72px; border-radius:var(--radius-md); background:linear-gradient(135deg, #10B981, #059669); display:flex; align-items:center; justify-content:center; font-size:32px; box-shadow:0 6px 24px rgba(16,185,129,0.25);">📱</div>
                            <div style="flex:1; overflow:hidden;">
                                <div style="font-size:11px; font-weight:700; color:var(--accent-green); text-transform:uppercase; margin-bottom:4px;">ANDROİD OYNATICI</div>
                                <h3 id="fullPhoneTitle" style="font-size:18px; font-weight:800; white-space:nowrap; overflow:hidden; text-overflow:ellipsis;">Telefon Medyası Yok</h3>
                                <p id="fullPhoneArtist" style="font-size:13px; color:var(--text-secondary); margin-top:2px; white-space:nowrap; overflow:hidden; text-overflow:ellipsis;">Telefonda müzik veya podcast açın</p>
                            </div>
                        </div>

                        <!-- Scrubber in Full Phone Media Tab -->
                        <div style="margin-bottom:16px; background:rgba(0,0,0,0.2); padding:14px; border-radius:var(--radius-md);">
                            <input type="range" id="fullPhoneMediaSeek" min="0" max="100" value="0" style="width:100%; accent-color:#10B981; cursor:pointer;" onmousedown="isSeekingPhoneMedia=true" ontouchstart="isSeekingPhoneMedia=true" oninput="updatePhoneSeekTimePreview(this.value)" onchange="seekPhoneMedia(this.value)">
                            <div style="display:flex; justify-content:space-between; font-size:11px; color:var(--text-secondary); margin-top:4px;">
                                <span id="fullPhoneMediaCurTime">00:00</span>
                                <span id="fullPhoneMediaTotalTime">00:00</span>
                            </div>
                        </div>

                        <div style="display:flex; justify-content:center; gap:8px; flex-wrap:wrap; margin-bottom:12px;">
                            <button class="btn" style="padding:10px 18px; font-size:13px;" onclick="seekPhoneRelative(-15)">⏪ 15s Geri</button>
                            <button class="btn" style="padding:10px 18px; font-size:13px;" onclick="sendPhoneCmd('PREV')">⏮ Önceki Parça</button>
                            <button class="btn btn-primary" style="padding:10px 24px; font-size:14px; background:#10B981;" onclick="sendPhoneCmd('PLAY_PAUSE')">⏯ Oynat / Duraklat</button>
                            <button class="btn" style="padding:10px 18px; font-size:13px;" onclick="sendPhoneCmd('NEXT')">⏭ Sonraki Parça</button>
                            <button class="btn" style="padding:10px 18px; font-size:13px;" onclick="seekPhoneRelative(15)">⏩ 15s İleri</button>
                        </div>
                        <div style="display:flex; justify-content:center; gap:10px;">
                            <button class="btn" style="padding:8px 16px; font-size:12px;" onclick="sendPhoneCmd('VOLUME_DOWN')">🔉 Telefon Ses -</button>
                            <button class="btn" style="padding:8px 16px; font-size:12px;" onclick="sendPhoneCmd('VOLUME_UP')">🔊 Telefon Ses +</button>
                            <button class="btn" style="padding:8px 16px; font-size:12px;" onclick="sendPhoneCmd('MUTE')">🔇 Telefon Sessiz</button>
                        </div>
                    </div>
                </div>
            </div>

            <!-- TAB 5: Ortak Pano -->
            <div id="tab-clipboard" class="tab-content">
                <div class="overview-grid">
                    <div class="card col-12">
                        <h3 style="font-size:18px; font-weight:700; margin-bottom:16px;">📋 Evrensel Ortak Pano (Mesh Clipboard)</h3>
                        <p style="font-size:13px; color:var(--text-secondary); margin-bottom:20px;">
                            Bilgisayarınızda veya telefonunuzda bir metni kopyaladığınız anda tüm cihazlarınızın panosu senkronize edilir.
                        </p>
                        <div id="fullClipBox" style="background:rgba(0,0,0,0.4); border:1px solid var(--border-card); border-radius:var(--radius-md); padding:20px; font-family:'JetBrains Mono',monospace; font-size:14px; min-height:140px; margin-bottom:20px; white-space:pre-wrap; word-break:break-all;">Pano boş...</div>
                        <div style="display:flex; gap:12px;">
                            <input type="text" id="customClipInput" class="search-input" placeholder="Telefona ve diğer cihazlara metin gönder...">
                            <button class="btn btn-primary" onclick="sendCustomClipText()">Panoya Aktar</button>
                            <button class="btn" onclick="copyFullClip()">PC Panosuna Kopyala</button>
                        </div>
                    </div>
                </div>
            </div>

            <!-- TAB 6: Bildirimler -->
            <div id="tab-notifications" class="tab-content">
                <div class="card col-12">
                    <div style="display:flex; justify-content:space-between; align-items:center; margin-bottom:20px;">
                        <h3 style="font-size:18px; font-weight:700;">🔔 Canlı Bildirim Akışı</h3>
                        <span id="notifBadgeFull" style="font-size:12px; color:var(--text-muted);">0 Bildirim</span>
                    </div>
                    <div id="fullNotifList" style="display:flex; flex-direction:column; gap:12px;">
                        <p style="color:var(--text-muted); font-size:14px; text-align:center; padding:40px;">Henüz gelen bildirim yok.</p>
                    </div>
                </div>
            </div>
        </main>
    </div>

    <script>
        let allSmsMessages = [];
        let activeThreadAddress = null;
        let isPhoneRinging = false;
        let isSeekingMedia = false;
        let currentMediaDuration = 0;

        function switchTab(tabId) {
            document.querySelectorAll('.tab-content').forEach(function(el) { el.classList.remove('active'); });
            document.querySelectorAll('.nav-item').forEach(function(el) { el.classList.remove('active'); });

            const targetTab = document.getElementById('tab-' + tabId);
            if (targetTab) targetTab.classList.add('active');

            const navIdx = ['overview', 'calls', 'sms', 'media', 'clipboard', 'notifications'].indexOf(tabId);
            const navItems = document.querySelectorAll('.nav-item');
            if (navIdx >= 0 && navItems[navIdx]) navItems[navIdx].classList.add('active');

            const titles = {
                'overview': 'Genel Bakış',
                'calls': 'Telefon & Aramalar',
                'sms': 'Mesajlar (SMS)',
                'media': 'Medya & Ses',
                'clipboard': 'Ortak Pano',
                'notifications': 'Canlı Bildirimler'
            };
            document.getElementById('pageTitle').innerText = titles[tabId] || 'Genel Bakış';

            if (tabId === 'sms') {
                loadSmsList();
            }
        }

        async function updateStatus() {
            try {
                const res = await fetch('/status');
                const data = await res.json();

                // Status Pill
                const badge = document.getElementById('statusBadge');
                const badgeText = document.getElementById('statusText');
                if (data.connected) {
                    badge.className = 'status-pill';
                    badgeText.innerText = (data.clients_count > 1 ? data.clients_count + ' Cihaz Bağlandı 🟢' : 'Bağlandı 🟢');
                } else {
                    badge.className = 'status-pill disconnected';
                    badgeText.innerText = 'Cihaz Aranıyor 🟡';
                }

                // Pairing Status
                const pBadge = document.getElementById('pairingBadge');
                const pText = document.getElementById('pairingText');
                const pBanner = document.getElementById('pairingBanner');
                const pPin = document.getElementById('bannerPairingPin');
                if (data.is_paired) {
                    pBadge.className = 'status-pill';
                    pText.innerText = '🔒 Güvenli (Eşleşti)';
                    pBanner.style.display = 'none';
                } else {
                    pBadge.className = 'status-pill disconnected';
                    pText.innerText = '⚠️ Eşleşme Bekleniyor';
                    if (data.connected) {
                        pBanner.style.display = 'flex';
                        pPin.innerText = (data.pairing_pin || '--- ---');
                    } else {
                        pBanner.style.display = 'none';
                    }
                }

                // Device Info
                if (data.device_name || data.model) {
                    document.getElementById('devName').innerText = (data.model || data.device_name);
                    document.getElementById('devSub').innerText = (data.is_charging ? '⚡ Şarj Oluyor' : 'Pilde Çalışıyor');
                    document.getElementById('devBattery').innerText = (data.battery_level >= 0 ? data.battery_level + '%' : '--%');
                    document.getElementById('devBatteryBar').style.width = (data.battery_level >= 0 ? data.battery_level + '%' : '0%');
                }

                // Call State
                if (data.call_state) {
                    handleCallState(data.call_state, data.call_duration_sec);
                }

                // 1. PC Media Update
                const pcMedia = data.pc_media;
                if (pcMedia && (pcMedia.title || pcMedia.artist)) {
                    const pcTitle = pcMedia.title || 'Bilinmeyen Parça';
                    const pcArtist = pcMedia.artist || 'Bilinmeyen Sanatçı';
                    const pcPlayingStr = pcMedia.is_playing ? 'OYNATILIYOR 🟢' : 'DURAKLATILDI ⏸';

                    const pcTrackTitleEl = document.getElementById('pcTrackTitle');
                    if (pcTrackTitleEl) pcTrackTitleEl.innerText = pcTitle;
                    const pcTrackArtistEl = document.getElementById('pcTrackArtist');
                    if (pcTrackArtistEl) pcTrackArtistEl.innerText = pcArtist;
                    const fullPCTitleEl = document.getElementById('fullPCTitle');
                    if (fullPCTitleEl) fullPCTitleEl.innerText = pcTitle;
                    const fullPCArtistEl = document.getElementById('fullPCArtist');
                    if (fullPCArtistEl) fullPCArtistEl.innerText = pcArtist;

                    const pcMediaTagEl = document.getElementById('pcMediaTag');
                    if (pcMediaTagEl) pcMediaTagEl.innerText = pcPlayingStr;
                    const fullPCTagEl = document.getElementById('fullPCTag');
                    if (fullPCTagEl) fullPCTagEl.innerText = pcPlayingStr;

                    currentMediaDuration = pcMedia.duration_ms || 0;
                    const curTimeStr = formatMs(pcMedia.position_ms || 0);
                    const totalTimeStr = formatMs(pcMedia.duration_ms || 0);

                    const pcCur = document.getElementById('pcMediaCurTime');
                    if (pcCur) pcCur.innerText = curTimeStr;
                    const pcTotal = document.getElementById('pcMediaTotalTime');
                    if (pcTotal) pcTotal.innerText = totalTimeStr;

                    const fullCur = document.getElementById('fullMediaCurTime');
                    if (fullCur) fullCur.innerText = curTimeStr;
                    const fullTotal = document.getElementById('fullMediaTotalTime');
                    if (fullTotal) fullTotal.innerText = totalTimeStr;

                    if (!isSeekingMedia && pcMedia.duration_ms > 0) {
                        const pct = Math.min(100, Math.max(0, (pcMedia.position_ms / pcMedia.duration_ms) * 100));
                        const seekEl = document.getElementById('pcMediaSeek');
                        if (seekEl) seekEl.value = pct;
                        const fullSeekEl = document.getElementById('fullMediaSeek');
                        if (fullSeekEl) fullSeekEl.value = pct;
                    }
                } else {
                    const pcTrackTitleEl = document.getElementById('pcTrackTitle');
                    if (pcTrackTitleEl) pcTrackTitleEl.innerText = 'PC\'de Çalan Medya Yok';
                    const pcTrackArtistEl = document.getElementById('pcTrackArtist');
                    if (pcTrackArtistEl) pcTrackArtistEl.innerText = 'Tarayıcı veya oynatıcı açın';
                    const pcMediaTagEl = document.getElementById('pcMediaTag');
                    if (pcMediaTagEl) pcMediaTagEl.innerText = 'BEKLEMEDE ⚪';
                    const fullPCTagEl = document.getElementById('fullPCTag');
                    if (fullPCTagEl) fullPCTagEl.innerText = 'BEKLEMEDE ⚪';
                }

                // 2. Phone Media Update
                const phoneMedia = data.phone_media;
                if (phoneMedia && (phoneMedia.title || phoneMedia.artist)) {
                    const phoneTitle = phoneMedia.title || 'Bilinmeyen Şarkı';
                    const phoneArtist = phoneMedia.artist || phoneMedia.album || 'Bilinmeyen Sanatçı';
                    const phonePlayingStr = phoneMedia.is_playing ? 'OYNATILIYOR 🟢' : 'DURAKLATILDI ⏸';

                    const phoneTrackTitleEl = document.getElementById('phoneTrackTitle');
                    if (phoneTrackTitleEl) phoneTrackTitleEl.innerText = phoneTitle;
                    const phoneTrackArtistEl = document.getElementById('phoneTrackArtist');
                    if (phoneTrackArtistEl) phoneTrackArtistEl.innerText = phoneArtist;
                    const fullPhoneTitleEl = document.getElementById('fullPhoneTitle');
                    if (fullPhoneTitleEl) fullPhoneTitleEl.innerText = phoneTitle;
                    const fullPhoneArtistEl = document.getElementById('fullPhoneArtist');
                    if (fullPhoneArtistEl) fullPhoneArtistEl.innerText = phoneArtist;

                    const phoneMediaTagEl = document.getElementById('phoneMediaTag');
                    if (phoneMediaTagEl) phoneMediaTagEl.innerText = phonePlayingStr;
                    const phoneStatusBadgeEl = document.getElementById('phoneMediaStatusBadge');
                    if (phoneStatusBadgeEl) phoneStatusBadgeEl.innerText = phonePlayingStr;
                    const fullPhoneTagEl = document.getElementById('fullPhoneTag');
                    if (fullPhoneTagEl) fullPhoneTagEl.innerText = phonePlayingStr;
                    const fullPhoneStatusEl = document.getElementById('fullPhoneStatus');
                    if (fullPhoneStatusEl) fullPhoneStatusEl.innerText = phonePlayingStr;

                    currentPhoneMediaDuration = phoneMedia.duration_ms || 0;
                    const phoneCurTimeStr = formatMs(phoneMedia.position_ms || 0);
                    const phoneTotalTimeStr = formatMs(phoneMedia.duration_ms || 0);

                    const phoneCur = document.getElementById('phoneMediaCurTime');
                    if (phoneCur) phoneCur.innerText = phoneCurTimeStr;
                    const phoneTotal = document.getElementById('phoneMediaTotalTime');
                    if (phoneTotal) phoneTotal.innerText = phoneTotalTimeStr;

                    const fullPhoneCur = document.getElementById('fullPhoneMediaCurTime');
                    if (fullPhoneCur) fullPhoneCur.innerText = phoneCurTimeStr;
                    const fullPhoneTotal = document.getElementById('fullPhoneMediaTotalTime');
                    if (fullPhoneTotal) fullPhoneTotal.innerText = phoneTotalTimeStr;

                    if (!isSeekingPhoneMedia && phoneMedia.duration_ms > 0) {
                        const pct = Math.min(100, Math.max(0, (phoneMedia.position_ms / phoneMedia.duration_ms) * 100));
                        const seekEl = document.getElementById('phoneMediaSeek');
                        if (seekEl) seekEl.value = pct;
                        const fullSeekEl = document.getElementById('fullPhoneMediaSeek');
                        if (fullSeekEl) fullSeekEl.value = pct;
                    }
                } else {
                    const phoneTrackTitleEl = document.getElementById('phoneTrackTitle');
                    if (phoneTrackTitleEl) phoneTrackTitleEl.innerText = 'Telefonda Çalan Medya Yok';
                    const phoneTrackArtistEl = document.getElementById('phoneTrackArtist');
                    if (phoneTrackArtistEl) phoneTrackArtistEl.innerText = 'Telefonda müzik açın';
                    const phoneMediaTagEl = document.getElementById('phoneMediaTag');
                    if (phoneMediaTagEl) phoneMediaTagEl.innerText = 'BEKLEMEDE ⚪';
                    const phoneStatusBadgeEl = document.getElementById('phoneMediaStatusBadge');
                    if (phoneStatusBadgeEl) phoneStatusBadgeEl.innerText = 'BEKLEMEDE ⚪';
                    const fullPhoneTagEl = document.getElementById('fullPhoneTag');
                    if (fullPhoneTagEl) fullPhoneTagEl.innerText = 'BEKLEMEDE ⚪';
                    const fullPhoneStatusEl = document.getElementById('fullPhoneStatus');
                    if (fullPhoneStatusEl) fullPhoneStatusEl.innerText = 'BEKLEMEDE ⚪';
                }

                // Clipboard
                if (data.clipboard) {
                    document.getElementById('quickClipText').innerText = data.clipboard;
                    document.getElementById('fullClipBox').innerText = data.clipboard;
                }

                // Notifications (Both Full Tab and Overview Tab)
                if (data.notifications && data.notifications.length > 0) {
                    document.getElementById('notifBadgeFull').innerText = data.notifications.length + ' Bildirim';
                    const listEl = document.getElementById('fullNotifList');
                    if (listEl) {
                        listEl.innerHTML = data.notifications.map(function(n) {
                            return '<div style="background:rgba(255,255,255,0.03); border:1px solid rgba(255,255,255,0.06); border-radius:10px; padding:14px;">' +
                                '<div style="display:flex; justify-content:space-between; margin-bottom:4px;">' +
                                    '<strong style="color:var(--accent-blue); font-size:13px;">' + escapeHtml(n.app_name || 'Uygulama') + '</strong>' +
                                    '<span style="font-size:11px; color:var(--text-muted);">' + formatTime(n.timestamp) + '</span>' +
                                '</div>' +
                                '<div style="font-weight:700; font-size:14px;">' + escapeHtml(n.title || '') + '</div>' +
                                '<div style="font-size:13px; color:var(--text-secondary); margin-top:2px;">' + escapeHtml(n.text || '') + '</div>' +
                            '</div>';
                        }).join('');
                    }

                    // Render Quick Notifications on Overview
                    const quickNotifsEl = document.getElementById('quickNotifList');
                    if (quickNotifsEl) {
                        quickNotifsEl.innerHTML = data.notifications.slice(0, 3).map(function(n) {
                            return '<div style="background:rgba(255,255,255,0.03); border:1px solid rgba(255,255,255,0.06); border-radius:8px; padding:10px; cursor:pointer;" onclick="switchTab(\'notifications\')">' +
                                '<div style="display:flex; justify-content:space-between; margin-bottom:2px;">' +
                                    '<strong style="color:var(--accent-blue); font-size:12px;">' + escapeHtml(n.app_name || 'Uygulama') + '</strong>' +
                                    '<span style="font-size:10px; color:var(--text-muted);">' + formatTime(n.timestamp) + '</span>' +
                                '</div>' +
                                '<div style="font-weight:700; font-size:13px;">' + escapeHtml(n.title || '') + '</div>' +
                                '<div style="font-size:12px; color:var(--text-secondary); margin-top:2px; white-space:nowrap; overflow:hidden; text-overflow:ellipsis;">' + escapeHtml(n.text || '') + '</div>' +
                            '</div>';
                        }).join('');
                    }
                }

                // SMS badge
                if (data.sms_count) {
                    document.getElementById('smsNavBadge').innerText = data.sms_count;
                }
            } catch (e) {
                console.error(e);
            }
        }

        function handleCallState(call, durationSec) {
            const overlay = document.getElementById('incomingCallOverlay');
            const activeBar = document.getElementById('activeCallBar');
            const callBadge = document.getElementById('callNavBadge');

            if (call.state === 'RINGING') {
                overlay.style.display = 'block';
                activeBar.style.display = 'none';
                callBadge.style.display = 'inline-block';
                callBadge.innerText = 'Çalıyor';
                document.getElementById('incomingCallerName').innerText = call.caller_name || call.phone_number || 'Bilinmeyen Arayan';
                document.getElementById('incomingCallerNumber').innerText = call.phone_number ? 'Gelen Çağrı: ' + call.phone_number : 'Gelen Çağrı...';
            } else if (call.state === 'OFFHOOK') {
                overlay.style.display = 'none';
                activeBar.style.display = 'block';
                callBadge.style.display = 'inline-block';
                callBadge.innerText = 'Konuşuluyor';
                const mins = Math.floor((durationSec || 0) / 60).toString().padStart(2, '0');
                const secs = Math.floor((durationSec || 0) % 60).toString().padStart(2, '0');
                document.getElementById('activeCallTimer').innerText = 'Süre: ' + mins + ':' + secs;
                document.getElementById('activeCallTitle').innerText = '🟢 ' + (call.caller_name || call.phone_number || 'Arama');
            } else {
                overlay.style.display = 'none';
                activeBar.style.display = 'none';
                callBadge.style.display = 'none';
            }
        }

        async function callAction(action, number, value) {
            try {
                let url = '/call/action?action=' + encodeURIComponent(action);
                if (number) url += '&number=' + encodeURIComponent(number);
                if (value !== undefined) url += '&value=' + encodeURIComponent(value);
                await fetch(url);
                updateStatus();
            } catch (e) {
                console.error(e);
            }
        }

        function addDial(digit) {
            const input = document.getElementById('dialInput');
            input.value += digit;
        }

        function dialNumber() {
            const num = document.getElementById('dialInput').value.trim();
            if (num) {
                callAction('DIAL', num);
            }
        }

        function dialActiveChat() {
            if (activeThreadAddress) {
                callAction('DIAL', activeThreadAddress);
            }
        }

        // SMS Functions
        async function loadSmsList() {
            try {
                const res = await fetch('/sms/list');
                const data = await res.json();
                allSmsMessages = data.messages || [];
                renderSmsThreads();
                renderQuickSms();
            } catch (e) {
                console.error(e);
            }
        }

        function renderQuickSms() {
            const el = document.getElementById('quickSmsList');
            if (!allSmsMessages.length) {
                el.innerHTML = '<p style="color:var(--text-muted); font-size:13px;">Gelen mesaj bulunamadı.</p>';
                return;
            }
            el.innerHTML = allSmsMessages.slice(0, 3).map(function(m) {
                return '<div style="background:rgba(255,255,255,0.03); border-radius:8px; padding:10px; cursor:pointer;" onclick="switchTab(\'sms\')">' +
                    '<div style="display:flex; justify-content:space-between; font-size:12px; margin-bottom:2px;">' +
                        '<strong style="color:var(--accent-blue);">' + escapeHtml(m.contact_name || m.address) + '</strong>' +
                        '<span style="color:var(--text-muted); font-size:10px;">' + formatTime(m.timestamp) + '</span>' +
                    '</div>' +
                    '<div style="font-size:12px; color:var(--text-secondary); white-space:nowrap; overflow:hidden; text-overflow:ellipsis;">' + escapeHtml(m.body) + '</div>' +
                '</div>';
            }).join('');
        }

        function renderSmsThreads() {
            const threadsMap = {};
            allSmsMessages.forEach(function(m) {
                const key = m.address || 'Bilinmeyen';
                if (!threadsMap[key]) {
                    threadsMap[key] = {
                        address: key,
                        name: m.contact_name || key,
                        latestMsg: m.body,
                        timestamp: m.timestamp,
                        messages: []
                    };
                }
                threadsMap[key].messages.push(m);
            });

            const threads = Object.values(threadsMap).sort(function(a,b) { return b.timestamp - a.timestamp; });
            const listEl = document.getElementById('threadsList');
            if (!threads.length) {
                listEl.innerHTML = '<li style="padding:20px; text-align:center; color:var(--text-muted); font-size:13px;">Mesaj kutusu boş.</li>';
                return;
            }

            listEl.innerHTML = threads.map(function(t) {
                const initial = (t.name || t.address).charAt(0).toUpperCase();
                const activeClass = (activeThreadAddress === t.address ? ' active' : '');
                return '<li class="thread-item' + activeClass + '" onclick="selectThread(\'' + escapeHtml(t.address) + '\')">' +
                    '<div class="thread-avatar">' + initial + '</div>' +
                    '<div class="thread-info">' +
                        '<div class="thread-title-row">' +
                            '<span class="thread-name">' + escapeHtml(t.name) + '</span>' +
                            '<span class="thread-time">' + formatTime(t.timestamp) + '</span>' +
                        '</div>' +
                        '<div class="thread-preview">' + escapeHtml(t.latestMsg) + '</div>' +
                    '</div>' +
                '</li>';
            }).join('');

            if (!activeThreadAddress && threads.length > 0) {
                selectThread(threads[0].address);
            }
        }

        function selectThread(address) {
            activeThreadAddress = address;
            document.querySelectorAll('.thread-item').forEach(function(el) { el.classList.remove('active'); });

            const threadMsgs = allSmsMessages.filter(function(m) { return m.address === address; })
                .sort(function(a,b) { return a.timestamp - b.timestamp; });

            const contactName = (threadMsgs[0] && threadMsgs[0].contact_name) ? threadMsgs[0].contact_name : address;
            document.getElementById('activeChatName').innerText = contactName;
            document.getElementById('activeChatNumber').innerText = address;
            document.getElementById('activeChatAvatar').innerText = contactName.charAt(0).toUpperCase();

            const chatEl = document.getElementById('chatMessages');
            chatEl.innerHTML = threadMsgs.map(function(m) {
                const bubbleClass = m.is_incoming ? 'incoming' : 'outgoing';
                return '<div class="msg-bubble ' + bubbleClass + '">' +
                    '<div>' + escapeHtml(m.body) + '</div>' +
                    '<div class="msg-meta">' + formatTime(m.timestamp) + (!m.is_incoming ? ' ✓' : '') + '</div>' +
                '</div>';
            }).join('');

            chatEl.scrollTop = chatEl.scrollHeight;
        }

        async function sendChatMsg() {
            const input = document.getElementById('chatInput');
            const body = input.value.trim();
            if (!body || !activeThreadAddress) return;

            input.value = '';
            try {
                await fetch('/sms/send', {
                    method: 'POST',
                    headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
                    body: 'recipient=' + encodeURIComponent(activeThreadAddress) + '&body=' + encodeURIComponent(body)
                });
                setTimeout(loadSmsList, 1000);
            } catch (e) {
                console.error(e);
            }
        }

        function newChatModal() {
            const num = prompt('SMS göndermek istediğiniz telefon numarasını girin:');
            if (num && num.trim()) {
                activeThreadAddress = num.trim();
                document.getElementById('activeChatName').innerText = num.trim();
                document.getElementById('activeChatNumber').innerText = num.trim();
                document.getElementById('chatMessages').innerHTML = '<div style="margin:auto; text-align:center; color:var(--text-muted); font-size:14px;">Yeni konuşma başlatıldı. İlk mesajınızı yazın.</div>';
                document.getElementById('chatInput').focus();
            }
        }

        function filterSms() {
            const q = document.getElementById('smsSearch').value.toLowerCase();
            document.querySelectorAll('.thread-item').forEach(function(el) {
                const text = el.innerText.toLowerCase();
                el.style.display = text.includes(q) ? 'flex' : 'none';
            });
        }

        function updateSeekTimePreview(val) {
            if (currentMediaDuration > 0) {
                const cur = (val / 100) * currentMediaDuration;
                const str = formatMs(cur);
                const pcCur = document.getElementById('pcMediaCurTime');
                if (pcCur) pcCur.innerText = str;
                const fullCur = document.getElementById('fullMediaCurTime');
                if (fullCur) fullCur.innerText = str;
            }
        }

        async function seekPCMedia(val) {
            try {
                await fetch('/pc/command?action=SEEK_PERCENT&percent=' + encodeURIComponent(val));
            } catch (e) {}
            setTimeout(function() { isSeekingMedia = false; }, 800);
        }

        async function seekRelative(deltaSec) {
            try {
                const action = deltaSec > 0 ? 'SEEK_FORWARD' : 'SEEK_BACKWARD';
                await fetch('/pc/command?action=' + action);
            } catch (e) {}
        }

        let isSeekingPhoneMedia = false;
        let currentPhoneMediaDuration = 0;

        function updatePhoneSeekTimePreview(val) {
            if (currentPhoneMediaDuration > 0) {
                const cur = (val / 100) * currentPhoneMediaDuration;
                const str = formatMs(cur);
                const phoneCur = document.getElementById('phoneMediaCurTime');
                if (phoneCur) phoneCur.innerText = str;
                const fullPhoneCur = document.getElementById('fullPhoneMediaCurTime');
                if (fullPhoneCur) fullPhoneCur.innerText = str;
            }
        }

        async function seekPhoneMedia(val) {
            try {
                await fetch('/phone/command?action=SEEK_PERCENT&percent=' + encodeURIComponent(val));
            } catch (e) {}
            setTimeout(function() { isSeekingPhoneMedia = false; }, 800);
        }

        async function seekPhoneRelative(deltaSec) {
            try {
                const action = deltaSec > 0 ? 'SEEK_FORWARD' : 'SEEK_BACKWARD';
                await fetch('/phone/command?action=' + action);
            } catch (e) {}
        }

        async function sendMediaCmd(action) {
            try {
                await fetch('/pc/command?action=' + encodeURIComponent(action));
            } catch (e) {}
        }

        function formatMs(ms) {
            if (!ms || ms <= 0) return '00:00';
            const totalSec = Math.floor(ms / 1000);
            const m = Math.floor(totalSec / 60);
            const s = totalSec % 60;
            return m.toString().padStart(2, '0') + ':' + s.toString().padStart(2, '0');
        }

        async function sendPhoneCmd(action) {
            try {
                await fetch('/phone/command?action=' + encodeURIComponent(action));
            } catch (e) {}
        }

        async function ringPhone() {
            isPhoneRinging = !isPhoneRinging;
            await sendPhoneCmd(isPhoneRinging ? 'RING' : 'STOP_RING');
        }

        async function sendCustomClipText() {
            const input = document.getElementById('customClipInput');
            const val = input.value.trim();
            if (!val) return;
            try {
                await fetch('/clipboard/send?text=' + encodeURIComponent(val));
                input.value = '';
                updateStatus();
            } catch (e) {}
        }

        function copyClip() {
            const text = document.getElementById('quickClipText').innerText;
            if (text) navigator.clipboard.writeText(text);
        }

        function copyFullClip() {
            const text = document.getElementById('fullClipBox').innerText;
            if (text) navigator.clipboard.writeText(text);
        }

        async function resetPairing() {
            try {
                const res = await fetch('/pair/reset');
                const d = await res.json();
                alert('Yeni eşleştirme PIN kodu üretildi: ' + d.pairing_pin + '\nTelefonunuzdaki onay penceresinde bu kodu doğrulayın.');
                updateStatus();
            } catch (e) {
                alert('Hata: ' + e.message);
            }
        }


        function escapeHtml(str) {
            return String(str).replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;').replace(/"/g, '&quot;');
        }

        function formatTime(ts) {
            if (!ts) return '';
            const d = new Date(ts);
            return d.getHours().toString().padStart(2, '0') + ':' + d.getMinutes().toString().padStart(2, '0');
        }

        setInterval(updateStatus, 1500);
        updateStatus();
        loadSmsList();
    </script>
</body>
</html>
`
