package server

import (
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"math/rand"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"mac-sync/internal/macos"
	"mac-sync/internal/protocol"
)

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool {
		return true // LAN bağlantılarına izin ver
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
	clipManager     *macos.ClipboardManager
	clientsMu       sync.RWMutex
	clients         map[*websocket.Conn]bool
	lastDeviceInfo  *protocol.DeviceInfoPayload
	lastMacMedia    *protocol.MediaInfoPayload
	lastPhoneMedia  *protocol.MediaInfoPayload
	notifications   []protocol.NotificationPayload
	notifsMu        sync.RWMutex
	lastCallState   *protocol.CallStatePayload
	callMu          sync.RWMutex
	activeCallStart time.Time
	smsMessages      []protocol.SmsMessage
	smsMu            sync.RWMutex
	transferredFiles []TransferredFile
	filesMu          sync.RWMutex
	sharedFilesDir   string
	audioHub         *AudioHub
	contacts         []protocol.ContactItem
	contactsMu       sync.RWMutex
	photos           []protocol.PhotoItem
	photosMu         sync.RWMutex
}

type TransferredFile struct {
	ID        string `json:"id"`
	FileName  string `json:"file_name"`
	FileSize  int64  `json:"file_size"`
	Path      string `json:"path"`
	Direction string `json:"direction"` // "incoming" or "outgoing"
	Timestamp int64  `json:"timestamp"`
}

func getDownloadsDir() string {
	home, err := os.UserHomeDir()
	if err == nil && home != "" {
		dir := filepath.Join(home, "Downloads", "AndroidSync")
		_ = os.MkdirAll(dir, 0755)
		return dir
	}
	dir := filepath.Join(".", "Downloads")
	_ = os.MkdirAll(dir, 0755)
	return dir
}

func getSharedFilesDir() string {
	home, err := os.UserHomeDir()
	if err == nil && home != "" {
		dir := filepath.Join(home, "Library", "Application Support", "MacSync", "shared_files")
		_ = os.MkdirAll(dir, 0755)
		return dir
	}
	dir := filepath.Join(".", "shared_files")
	_ = os.MkdirAll(dir, 0755)
	return dir
}

func getDaemonConfigPath() string {
	home, err := os.UserHomeDir()
	if err == nil && home != "" {
		dir := filepath.Join(home, "Library", "Application Support", "MacSync")
		_ = os.MkdirAll(dir, 0755)
		return filepath.Join(dir, "daemon_config.json")
	}
	return "daemon_config.json"
}

func generateNewPIN() string {
	r := rand.New(rand.NewSource(time.Now().UnixNano()))
	return fmt.Sprintf("%06d", r.Intn(1000000))
}

func NewSyncServer(port int, serverName string, clipManager *macos.ClipboardManager) *SyncServer {
	cfgPath := getDaemonConfigPath()
	var cfg DaemonConfig
	if data, err := os.ReadFile(cfgPath); err == nil {
		_ = json.Unmarshal(data, &cfg)
	}

	hostname, _ := os.Hostname()
	if cfg.MachineID == "" {
		r := rand.New(rand.NewSource(time.Now().UnixNano()))
		cfg.MachineID = fmt.Sprintf("mac-%s-%x", hostname, r.Int63()&0xFFFFFF)
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
		clients:          make(map[*websocket.Conn]bool),
		notifications:    make([]protocol.NotificationPayload, 0, 50),
		smsMessages:      make([]protocol.SmsMessage, 0, 100),
		transferredFiles: make([]TransferredFile, 0, 50),
		sharedFilesDir:   getSharedFilesDir(),
		audioHub:         NewAudioHub(),
		contacts:         make([]protocol.ContactItem, 0, 100),
		photos:           make([]protocol.PhotoItem, 0, 50),
	}
	s.saveConfig()
	return s
}

func (s *SyncServer) GetPairingPIN() string {
	s.configMu.RLock()
	defer s.configMu.RUnlock()
	return s.pairingPIN
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
	// Telefona unpair bildirimi gönder
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
		macMedia := s.lastMacMedia
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

		lastClip, _ := macos.GetClipboard()
		var lastClipImg string
		if imgBytes, err := macos.GetClipboardImage(); err == nil && len(imgBytes) > 0 {
			if len(imgBytes) <= 3*1024*1024 {
				lastClipImg = base64.StdEncoding.EncodeToString(imgBytes)
			}
		}

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
			"clipboard_image":    lastClipImg,
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
		if macMedia != nil {
			statusResp["mac_media"] = macMedia
			statusResp["pc_media"] = macMedia // dashboard uyumluluğu için
		}
		if phoneMedia != nil {
			statusResp["phone_media"] = phoneMedia
		}
		if callState != nil {
			statusResp["call_state"] = callState
		}

		s.filesMu.RLock()
		filesCopy := make([]TransferredFile, len(s.transferredFiles))
		copy(filesCopy, s.transferredFiles)
		s.filesMu.RUnlock()

		s.contactsMu.RLock()
		contactsCount := len(s.contacts)
		s.contactsMu.RUnlock()

		s.photosMu.RLock()
		photosCount := len(s.photos)
		s.photosMu.RUnlock()

		statusResp["files_count"] = len(filesCopy)
		statusResp["transferred_files"] = filesCopy
		statusResp["downloads_dir"] = getDownloadsDir()
		statusResp["contacts_count"] = contactsCount
		statusResp["photos_count"] = photosCount

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

		log.Printf("[Çağrı] Mac'ten çağrı komutu iletiliyor: %s (Numara: %s)", action, number)
		s.SendCallAction(action, numPtr, valPtr)

		if action == "ANSWER" {
			s.callMu.Lock()
			s.activeCallStart = time.Now()
			if s.lastCallState != nil {
				s.lastCallState.State = "OFFHOOK"
			}
			s.callMu.Unlock()
			macos.DismissCallAlert()
		} else if action == "REJECT" || action == "HANGUP" {
			s.callMu.Lock()
			if s.lastCallState != nil {
				s.lastCallState.State = "IDLE"
			}
			s.activeCallStart = time.Time{}
			s.callMu.Unlock()
			macos.DismissCallAlert()
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

		log.Printf("[SMS] Mac üzerinden SMS gönderiliyor -> %s: %s", recipient, body)
		s.SendSms(recipient, body)

		newMsg := protocol.SmsMessage{
			ID:         fmt.Sprintf("mac_local_%d", time.Now().UnixMilli()),
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

	// Media update from Mac tracker
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

	// Mac media command endpoint (also aliased to /pc/command and /mac/command for compatibility)
	handleMediaCmd := func(w http.ResponseWriter, r *http.Request) {
		action := r.URL.Query().Get("action")
		if action == "" {
			http.Error(w, "missing action", http.StatusBadRequest)
			return
		}
		pctStr := r.URL.Query().Get("percent")
		if action == "SEEK_PERCENT" || (pctStr != "" && action == "SEEK") {
			var pct float64
			_, _ = fmt.Sscanf(pctStr, "%f", &pct)
			_ = macos.ExecuteMediaSeekPercent(pct)
		} else if action == "SEEK_FORWARD" {
			_ = macos.ExecuteMediaSeekRelative(15)
		} else if action == "SEEK_BACKWARD" {
			_ = macos.ExecuteMediaSeekRelative(-15)
		} else {
			_ = macos.ExecuteMediaAction(action)
		}
		w.Header().Set("Content-Type", "text/plain")
		fmt.Fprintf(w, "OK\n")
	}

	mux.HandleFunc("/media/command", handleMediaCmd)
	mux.HandleFunc("/pc/command", handleMediaCmd)
	mux.HandleFunc("/mac/command", handleMediaCmd)

	// IPC Notification endpoint from MacSync.app or external scripts
	mux.HandleFunc("/mac/notification", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "POST required", http.StatusMethodNotAllowed)
			return
		}
		var p protocol.NotificationPayload
		if err := json.NewDecoder(r.Body).Decode(&p); err == nil && (p.Title != "" || p.Text != "") {
			if p.AppName == "" {
				p.AppName = "Mac"
			}
			if p.ID == "" {
				p.ID = fmt.Sprintf("mac_%d", time.Now().UnixMilli())
			}
			p.Timestamp = time.Now().UnixMilli()
			s.BroadcastMacNotification(p)
			w.WriteHeader(http.StatusOK)
			return
		}
		http.Error(w, "invalid payload", http.StatusBadRequest)
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
			log.Printf("[Pano] Web arayüzünden Mac panosuna ve telefona iletildi (%d bayt)", len(text))
		}
		w.Header().Set("Content-Type", "text/plain")
		fmt.Fprintf(w, "OK\n")
	})

	// File Upload from Phone to Mac
	mux.HandleFunc("/file/upload", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "POST required", http.StatusMethodNotAllowed)
			return
		}
		if err := r.ParseMultipartForm(1024 * 1024 * 1024); err != nil {
			http.Error(w, "Form parse hatası: "+err.Error(), http.StatusBadRequest)
			return
		}

		destDir := getDownloadsDir()
		files := r.MultipartForm.File["file"]
		if len(files) == 0 {
			http.Error(w, "Dosya bulunamadı", http.StatusBadRequest)
			return
		}

		var uploadedList []TransferredFile
		for _, fileHeader := range files {
			src, err := fileHeader.Open()
			if err != nil {
				continue
			}

			cleanName := filepath.Base(fileHeader.Filename)
			dstPath := filepath.Join(destDir, cleanName)
			if _, err := os.Stat(dstPath); err == nil {
				ext := filepath.Ext(cleanName)
				base := strings.TrimSuffix(cleanName, ext)
				dstPath = filepath.Join(destDir, fmt.Sprintf("%s_%d%s", base, time.Now().UnixMilli()%10000, ext))
			}

			dst, err := os.Create(dstPath)
			if err != nil {
				src.Close()
				continue
			}

			written, err := io.Copy(dst, src)
			src.Close()
			dst.Close()

			if err == nil {
				fInfo := TransferredFile{
					ID:        fmt.Sprintf("file_%d", time.Now().UnixMilli()),
					FileName:  filepath.Base(dstPath),
					FileSize:  written,
					Path:      dstPath,
					Direction: "incoming",
					Timestamp: time.Now().UnixMilli(),
				}
				s.filesMu.Lock()
				s.transferredFiles = append([]TransferredFile{fInfo}, s.transferredFiles...)
				if len(s.transferredFiles) > 100 {
					s.transferredFiles = s.transferredFiles[:100]
				}
				s.filesMu.Unlock()

				uploadedList = append(uploadedList, fInfo)
				sizeMB := float64(written) / (1024 * 1024)
				_ = macos.ShowNotification("📁 Yeni Dosya Alındı: "+fInfo.FileName, fmt.Sprintf("%.2f MB - İndirilenler klasörüne kaydedildi.", sizeMB), "Android Sync", "Glass")
				log.Printf("[Dosya] Mac'e dosya başarıyla alındı: %s (%.2f MB)", dstPath, sizeMB)
			}
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"success": true,
			"files":   uploadedList,
		})
	})

	// File Send from Mac to Phone (via Web Dashboard)
	mux.HandleFunc("/file/send_to_phone", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "POST required", http.StatusMethodNotAllowed)
			return
		}
		if err := r.ParseMultipartForm(1024 * 1024 * 1024); err != nil {
			http.Error(w, "Form parse hatası: "+err.Error(), http.StatusBadRequest)
			return
		}

		files := r.MultipartForm.File["file"]
		if len(files) == 0 {
			http.Error(w, "Dosya belirtilmedi", http.StatusBadRequest)
			return
		}

		sharedDir := getSharedFilesDir()
		var notifiedList []protocol.FileAvailablePayload

		for _, fh := range files {
			src, err := fh.Open()
			if err != nil {
				continue
			}

			fileID := fmt.Sprintf("share_%d", time.Now().UnixMilli())
			cleanName := filepath.Base(fh.Filename)
			dstPath := filepath.Join(sharedDir, fmt.Sprintf("%s_%s", fileID, cleanName))

			dst, err := os.Create(dstPath)
			if err != nil {
				src.Close()
				continue
			}

			written, err := io.Copy(dst, src)
			src.Close()
			dst.Close()

			if err == nil {
				fInfo := TransferredFile{
					ID:        fileID,
					FileName:  cleanName,
					FileSize:  written,
					Path:      dstPath,
					Direction: "outgoing",
					Timestamp: time.Now().UnixMilli(),
				}
				s.filesMu.Lock()
				s.transferredFiles = append([]TransferredFile{fInfo}, s.transferredFiles...)
				s.filesMu.Unlock()

				dlURL := fmt.Sprintf("http://%s:%d/file/download/%s/%s", getLocalIP(), s.port, fileID, url.PathEscape(cleanName))
				p := protocol.FileAvailablePayload{
					ID:          fileID,
					FileName:    cleanName,
					FileSize:    written,
					DownloadURL: dlURL,
					MimeType:    fh.Header.Get("Content-Type"),
					Sender:      s.serverName,
					Timestamp:   time.Now().UnixMilli(),
				}
				notifiedList = append(notifiedList, p)

				msg, _ := protocol.NewMessage(protocol.EventFileAvailable, p)
				s.Broadcast(msg)
				log.Printf("[Dosya] Mac'ten telefona dosya hazırlandı ve sinyal gönderildi: %s (URL: %s)", cleanName, dlURL)
			}
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"success": true,
			"sent":    notifiedList,
		})
	})

	// File Download endpoint (Phone or Browser downloads shared file)
	mux.HandleFunc("/file/download/", func(w http.ResponseWriter, r *http.Request) {
		parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/file/download/"), "/")
		if len(parts) == 0 || parts[0] == "" {
			http.Error(w, "Dosya belirtilmedi", http.StatusBadRequest)
			return
		}
		fileID := parts[0]
		var targetPath string
		var origName string

		s.filesMu.RLock()
		for _, tf := range s.transferredFiles {
			if tf.ID == fileID {
				targetPath = tf.Path
				origName = tf.FileName
				break
			}
		}
		s.filesMu.RUnlock()

		if targetPath == "" || !func() bool { _, err := os.Stat(targetPath); return err == nil }() {
			matches, _ := filepath.Glob(filepath.Join(getSharedFilesDir(), fileID+"_*"))
			if len(matches) > 0 {
				targetPath = matches[0]
				origName = strings.TrimPrefix(filepath.Base(targetPath), fileID+"_")
			}
		}

		if targetPath == "" {
			http.Error(w, "Dosya bulunamadı", http.StatusNotFound)
			return
		}

		if origName == "" {
			origName = filepath.Base(targetPath)
		}

		w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=\"%s\"", origName))
		http.ServeFile(w, r, targetPath)
	})

	// Open Downloads Folder in macOS Finder
	mux.HandleFunc("/file/open_folder", func(w http.ResponseWriter, r *http.Request) {
		dir := getDownloadsDir()
		_ = exec.Command("open", dir).Start()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "path": dir})
	})

	// Transferred Files List API
	mux.HandleFunc("/file/list", func(w http.ResponseWriter, r *http.Request) {
		s.filesMu.RLock()
		list := make([]TransferredFile, len(s.transferredFiles))
		copy(list, s.transferredFiles)
		s.filesMu.RUnlock()

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"files": list,
			"count": len(list),
			"dir":   getDownloadsDir(),
		})
	})

	// Notification Inline Reply API
	mux.HandleFunc("/notification/reply", func(w http.ResponseWriter, r *http.Request) {
		key := r.URL.Query().Get("key")
		text := r.URL.Query().Get("text")
		if r.Method == http.MethodPost {
			_ = r.ParseForm()
			if k := r.FormValue("key"); k != "" {
				key = k
			}
			if t := r.FormValue("text"); t != "" {
				text = t
			}
		}

		if key == "" || text == "" {
			http.Error(w, "key and text required", http.StatusBadRequest)
			return
		}

		s.SendNotificationReply(key, 0, text)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "key": key})
	})

	// Notification Action Trigger API
	mux.HandleFunc("/notification/action", func(w http.ResponseWriter, r *http.Request) {
		key := r.URL.Query().Get("key")
		idxStr := r.URL.Query().Get("index")
		if r.Method == http.MethodPost {
			_ = r.ParseForm()
			if k := r.FormValue("key"); k != "" {
				key = k
			}
			if idx := r.FormValue("index"); idx != "" {
				idxStr = idx
			}
		}

		if key == "" || idxStr == "" {
			http.Error(w, "key and index required", http.StatusBadRequest)
			return
		}

		actionIndex, _ := strconv.Atoi(idxStr)
		s.SendNotificationAction(key, actionIndex)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "key": key, "index": actionIndex})
	})

	// Remote Action API (Lock, Sleep, Shutdown, Restart)
	mux.HandleFunc("/remote/action", func(w http.ResponseWriter, r *http.Request) {
		action := r.URL.Query().Get("action")
		if r.Method == http.MethodPost {
			_ = r.ParseForm()
			if a := r.FormValue("action"); a != "" {
				action = a
			}
		}
		if action == "" {
			http.Error(w, "action required", http.StatusBadRequest)
			return
		}

		s.executeRemoteAction(action)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "action": action})
	})

	// Contacts List API
	mux.HandleFunc("/contacts", func(w http.ResponseWriter, r *http.Request) {
		s.contactsMu.RLock()
		contactsCopy := make([]protocol.ContactItem, len(s.contacts))
		copy(contactsCopy, s.contacts)
		s.contactsMu.RUnlock()

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"contacts": contactsCopy,
			"count":    len(contactsCopy),
		})
	})

	// Contacts Refresh API (Requests sync from phone)
	mux.HandleFunc("/contacts/refresh", func(w http.ResponseWriter, r *http.Request) {
		s.RequestContactsSync()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"success": true})
	})

	// Send URL / Tab to Phone
	mux.HandleFunc("/url/send_to_phone", func(w http.ResponseWriter, r *http.Request) {
		rawURL := r.URL.Query().Get("url")
		if r.Method == http.MethodPost {
			_ = r.ParseForm()
			if u := r.FormValue("url"); u != "" {
				rawURL = u
			}
		}
		rawURL = strings.TrimSpace(rawURL)
		if rawURL == "" {
			http.Error(w, "url required", http.StatusBadRequest)
			return
		}
		s.SendOpenUrlToPhone(rawURL)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "url": rawURL})
	})

	// Open URL in Local Browser (macOS)
	mux.HandleFunc("/url/open_local", func(w http.ResponseWriter, r *http.Request) {
		rawURL := r.URL.Query().Get("url")
		if r.Method == http.MethodPost {
			_ = r.ParseForm()
			if u := r.FormValue("url"); u != "" {
				rawURL = u
			}
		}
		rawURL = strings.TrimSpace(rawURL)
		if rawURL == "" {
			http.Error(w, "url required", http.StatusBadRequest)
			return
		}
		_ = s.openURLInBrowser(rawURL)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "url": rawURL})
	})

	// Photos List API
	mux.HandleFunc("/photos", func(w http.ResponseWriter, r *http.Request) {
		s.photosMu.RLock()
		photosCopy := make([]protocol.PhotoItem, len(s.photos))
		copy(photosCopy, s.photos)
		s.photosMu.RUnlock()

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"photos": photosCopy,
			"count":  len(photosCopy),
		})
	})

	// Photos Refresh API (Requests photos from phone)
	mux.HandleFunc("/photos/refresh", func(w http.ResponseWriter, r *http.Request) {
		s.RequestPhotosSync()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"success": true})
	})

	// Photos Download API (Requests full photo from phone to be uploaded to Mac)
	mux.HandleFunc("/photos/download", func(w http.ResponseWriter, r *http.Request) {
		idStr := r.URL.Query().Get("id")
		if r.Method == http.MethodPost {
			_ = r.ParseForm()
			if i := r.FormValue("id"); i != "" {
				idStr = i
			}
		}
		if idStr == "" {
			http.Error(w, "photo id required", http.StatusBadRequest)
			return
		}
		var id int64
		_, _ = fmt.Sscanf(idStr, "%d", &id)
		if id <= 0 {
			http.Error(w, "invalid photo id", http.StatusBadRequest)
			return
		}

		s.RequestPhotoDownload(id)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "id": id})
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

	// APK download endpoint
	mux.HandleFunc("/download", func(w http.ResponseWriter, r *http.Request) {
		candidates := []string{
			"AndroidSync.apk",
			"../AndroidSync.apk",
			"MacSync.apk",
			"../MacSync.apk",
			"android-app/app/build/outputs/apk/debug/app-debug.apk",
			"../android-app/app/build/outputs/apk/debug/app-debug.apk",
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

	welcome, _ := protocol.NewMessage(protocol.EventDeviceInfo, map[string]string{
		"server_name": s.serverName,
		"status":      "connected",
	})
	_ = conn.WriteJSON(welcome)

	s.clientsMu.RLock()
	curMacMedia := s.lastMacMedia
	s.clientsMu.RUnlock()
	if curMacMedia != nil {
		if initialMedia, err := protocol.NewMessage(protocol.EventMediaInfo, curMacMedia); err == nil {
			_ = conn.WriteJSON(initialMedia)
		}
	}

	// 1. Güvenlik & Eşleştirme İsteği Gönder (OS: "macos")
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
		OS:         "macos",
		AuthToken:  token,
		PairingPin: pin,
	})
	_ = conn.WriteJSON(authReq)

	if isPaired {
		s.RequestSmsSync()
	}

	defer func() {
		s.clientsMu.Lock()
		delete(s.clients, conn)
		remaining := len(s.clients)
		if remaining == 0 {
			s.lastDeviceInfo = nil
		}
		s.clientsMu.Unlock()
		log.Printf("[Server] 📱 Android cihaz ayrıldı: %s", conn.RemoteAddr())
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
				s.RequestContactsSync()
				s.RequestPhotosSync()
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
				s.RequestContactsSync()
				s.RequestPhotosSync()
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
		}

	case protocol.EventNotification, protocol.EventPCNotification:
		var p protocol.NotificationPayload
		if err := json.Unmarshal(msg.Payload, &p); err == nil {
			log.Printf("[Bildirim] [%s] %s: %s", p.AppName, p.Title, p.Text)
			s.addNotification(p)
			_ = macos.ShowNotification(p.Title, p.AppName, p.Text, "Ping")
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
				_ = macos.ExecuteMediaAction("PAUSE")
				_ = macos.ShowCallAlert(p.CallerName, p.PhoneNumber)
			} else if p.State == "IDLE" || p.State == "OFFHOOK" {
				macos.DismissCallAlert()
			}
		}

	case protocol.EventSmsSyncResponse:
		var p protocol.SmsSyncPayload
		if err := json.Unmarshal(msg.Payload, &p); err == nil {
			s.smsMu.Lock()
			s.smsMessages = p.Messages
			s.smsMu.Unlock()
			log.Printf("[SMS] %d adet SMS mesajı Mac'e senkronize edildi", len(p.Messages))
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
			_ = macos.ShowSmsAlert(senderName, msgItem.Body)
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
			log.Printf("[Medya Komutu] %s (Yüzde: %.1f)", p.Action, p.Percent)
			if p.Action == "SEEK_PERCENT" {
				_ = macos.ExecuteMediaSeekPercent(p.Percent)
			} else {
				_ = macos.ExecuteMediaAction(p.Action)
			}
		}

	case protocol.EventClipboard:
		var p protocol.ClipboardPayload
		if err := json.Unmarshal(msg.Payload, &p); err == nil {
			if p.Type == "image" && p.ImageBase64 != "" {
				imgBytes, err := base64.StdEncoding.DecodeString(p.ImageBase64)
				if err == nil && len(imgBytes) > 0 {
					log.Printf("[Pano] 🖼 Telefondan Mac'e görsel alındı (%d bayt)", len(imgBytes))
					_ = s.clipManager.SetClipboardImage(imgBytes)
					_ = macos.ShowNotification("📋 Pano: Görsel Alındı", "", "Telefonda kopyalanan görsel Mac panosuna yazıldı (⌘V ile yapıştırabilirsiniz).", "Glass")
				}
			} else if p.Text != "" {
				log.Printf("[Pano] 📋 Telefondan Mac'e kopyalandı (%d bayt)", len(p.Text))
				_ = s.clipManager.SetClipboard(p.Text)
			}
		}

	case protocol.EventFileUploadNotify:
		var p protocol.FileUploadNotifyPayload
		if err := json.Unmarshal(msg.Payload, &p); err == nil {
			s.filesMu.Lock()
			s.transferredFiles = append([]TransferredFile{{
				ID:        p.ID,
				FileName:  p.FileName,
				FileSize:  p.FileSize,
				Path:      p.Path,
				Direction: "incoming",
				Timestamp: p.Timestamp,
			}}, s.transferredFiles...)
			if len(s.transferredFiles) > 100 {
				s.transferredFiles = s.transferredFiles[:100]
			}
			s.filesMu.Unlock()

			sizeMB := float64(p.FileSize) / (1024 * 1024)
			_ = macos.ShowNotification("📁 Yeni Dosya Alındı: "+p.FileName, fmt.Sprintf("%.2f MB - İndirilenler klasörüne kaydedildi.", sizeMB), "Android Sync", "Glass")
			log.Printf("[Dosya] Telefonda dosya bildirimi alındı: %s (%.2f MB)", p.FileName, sizeMB)
		}

	case protocol.EventRemoteAction:
		var p protocol.RemoteActionPayload
		if err := json.Unmarshal(msg.Payload, &p); err == nil {
			s.executeRemoteAction(p.Action)
		}

	case protocol.EventOpenUrl:
		var p protocol.OpenUrlPayload
		if err := json.Unmarshal(msg.Payload, &p); err == nil && p.URL != "" {
			log.Printf("[Sekme Paylaşımı] Telefondan web bağlantısı alındı: %s", p.URL)
			_ = s.openURLInBrowser(p.URL)
			_ = macos.ShowNotification("🌐 Telefondan Bağlantı Açıldı", p.URL, "Android Sync", "Glass")
		}

	case protocol.EventContactsResponse:
		var p protocol.ContactsResponsePayload
		if err := json.Unmarshal(msg.Payload, &p); err == nil {
			s.contactsMu.Lock()
			s.contacts = p.Contacts
			s.contactsMu.Unlock()
			log.Printf("[Rehber] %d adet kişi telefondan başarıyla senkronize edildi", len(p.Contacts))
		}

	case protocol.EventPhotosResponse:
		var p protocol.PhotosResponsePayload
		if err := json.Unmarshal(msg.Payload, &p); err == nil {
			s.photosMu.Lock()
			s.photos = p.Photos
			s.photosMu.Unlock()
			log.Printf("[Galeri] %d adet fotoğraf telefondan başarıyla senkronize edildi", len(p.Photos))
		}

	case protocol.EventTouchpadEvent:
		var p protocol.TouchpadEventPayload
		if err := json.Unmarshal(msg.Payload, &p); err == nil {
			macos.HandleTouchpadEvent(p)
		}

	case protocol.EventPong:
		// Heartbeat
	}
}

func (s *SyncServer) openURLInBrowser(rawURL string) error {
	u := strings.TrimSpace(rawURL)
	if !strings.HasPrefix(u, "http://") && !strings.HasPrefix(u, "https://") {
		u = "https://" + u
	}
	return exec.Command("open", u).Start()
}

func (s *SyncServer) SendOpenUrlToPhone(url string) {
	msg, err := protocol.NewMessage(protocol.EventOpenUrl, protocol.OpenUrlPayload{
		URL:    url,
		Sender: s.serverName,
	})
	if err == nil {
		s.Broadcast(msg)
		log.Printf("[Sekme Paylaşımı] URL telefona gönderildi: %s", url)
	}
}

func (s *SyncServer) RequestContactsSync() {
	msg, err := protocol.NewMessage(protocol.EventContactsRequest, map[string]any{})
	if err == nil {
		s.Broadcast(msg)
		log.Printf("[Rehber] Telefon rehberi senkronizasyon isteği gönderildi")
	}
}

func (s *SyncServer) RequestPhotosSync() {
	msg, err := protocol.NewMessage(protocol.EventPhotosRequest, map[string]any{})
	if err == nil {
		s.Broadcast(msg)
		log.Printf("[Galeri] Telefon fotoğraf galerisi senkronizasyon isteği gönderildi")
	}
}

func (s *SyncServer) RequestPhotoDownload(photoID int64) {
	msg, err := protocol.NewMessage(protocol.EventPhotoDownloadRequest, protocol.PhotoDownloadRequestPayload{
		ID: photoID,
	})
	if err == nil {
		s.Broadcast(msg)
		log.Printf("[Galeri] Fotoğraf indirme isteği telefona gönderildi: id=%d", photoID)
	}
}

// SendNotificationReply sends an inline reply to an Android notification.
func (s *SyncServer) SendNotificationReply(key string, actionIndex int, text string) {
	payload := protocol.NotificationReplyPayload{
		NotificationKey: key,
		ActionIndex:     actionIndex,
		ReplyText:       text,
	}
	msg, err := protocol.NewMessage(protocol.EventNotificationReply, payload)
	if err == nil {
		s.Broadcast(msg)
		log.Printf("[Bildirim Yanıtı] Yanıt iletildi (%s): %s", key, text)
	}
}

// SendNotificationAction triggers an action button on an Android notification.
func (s *SyncServer) SendNotificationAction(key string, actionIndex int) {
	payload := protocol.NotificationActionPayload{
		NotificationKey: key,
		ActionIndex:     actionIndex,
	}
	msg, err := protocol.NewMessage(protocol.EventNotificationAction, payload)
	if err == nil {
		s.Broadcast(msg)
		log.Printf("[Bildirim Eylemi] Eylem isteği gönderildi (%s, index: %d)", key, actionIndex)
	}
}

// executeRemoteAction executes remote macOS commands (Lock, Sleep, Shutdown, Restart).
func (s *SyncServer) executeRemoteAction(action string) {
	log.Printf("[Remote] Uzaktan Mac sistem komutu tetiklendi: %s", action)
	switch action {
	case "LOCK":
		_ = exec.Command("pmset", "displaysleepnow").Start()
	case "SLEEP":
		_ = exec.Command("pmset", "sleepnow").Start()
	case "SHUTDOWN":
		_ = exec.Command("osascript", "-e", "tell app \"System Events\" to shut down").Start()
	case "RESTART":
		_ = exec.Command("osascript", "-e", "tell app \"System Events\" to restart").Start()
	}
}

func (s *SyncServer) SendCallAction(action string, number *string, val *bool) {
	msg, err := protocol.NewMessage(protocol.EventCallAction, protocol.CallActionPayload{
		Action: action,
		Number: number,
		Value:  val,
	})
	if err == nil {
		s.Broadcast(msg)
	}
}

func (s *SyncServer) SendSms(recipient, body string) {
	msg, err := protocol.NewMessage(protocol.EventSmsSend, protocol.SmsSendPayload{
		Recipient: recipient,
		Body:      body,
	})
	if err == nil {
		s.Broadcast(msg)
	}
}

func (s *SyncServer) RequestSmsSync() {
	msg, err := protocol.NewMessage(protocol.EventSmsSyncRequest, map[string]any{"limit": 100})
	if err == nil {
		s.Broadcast(msg)
	}
}

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

func (s *SyncServer) SendPhoneCommandWithPercent(action string, percent float64) {
	s.SendPhoneCommand(action, percent)
}

// BroadcastMacNotification broadcasts an incoming macOS notification to connected phones and updates dashboard.
func (s *SyncServer) BroadcastMacNotification(p protocol.NotificationPayload) {
	log.Printf("[Mac Bildirimi] 🍏 [%s] %s: %s -> Telefona iletiliyor", p.AppName, p.Title, p.Text)
	s.addNotification(p)
	if msg, err := protocol.NewMessage(protocol.EventPCNotification, p); err == nil {
		s.Broadcast(msg)
	}
}

func (s *SyncServer) Broadcast(msg *protocol.Message) {
	s.clientsMu.RLock()
	defer s.clientsMu.RUnlock()

	for conn := range s.clients {
		if err := conn.WriteJSON(msg); err != nil {
			log.Printf("[Server] Broadcast hatası: %v", err)
		}
	}
}

func (s *SyncServer) UpdateMacMedia(info protocol.MediaInfoPayload) {
	s.clientsMu.Lock()
	info.Source = "mac"
	s.lastMacMedia = &info
	s.clientsMu.Unlock()

	msg, err := protocol.NewMessage(protocol.EventMediaInfo, info)
	if err == nil {
		s.Broadcast(msg)
	}
}

const dashboardHTML = `<!DOCTYPE html>
<html lang="tr">
<head>
    <meta charset="UTF-8">
    <meta name="viewport" content="width=device-width, initial-scale=1.0">
    <title>Android Sync | macOS Kontrol Paneli</title>
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

        * { box-sizing: border-box; margin: 0; padding: 0; font-family: -apple-system, BlinkMacSystemFont, 'Plus Jakarta Sans', sans-serif; }

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
            flex-shrink: 0;
            z-index: 10;
        }

        .brand {
            display: flex;
            align-items: center;
            gap: 12px;
            padding: 0 8px 24px 8px;
            border-bottom: 1px solid var(--border-card);
        }

        .brand-icon {
            width: 40px;
            height: 40px;
            background: linear-gradient(135deg, #38BDF8, #6366F1);
            border-radius: var(--radius-md);
            display: flex;
            align-items: center;
            justify-content: center;
            font-size: 20px;
            box-shadow: 0 4px 16px var(--glow-cyan);
        }

        .brand-text h2 {
            font-size: 16px;
            font-weight: 700;
            letter-spacing: -0.02em;
        }

        .brand-text span {
            font-size: 11px;
            color: var(--text-muted);
            text-transform: uppercase;
            letter-spacing: 0.08em;
            font-weight: 600;
        }

        .nav-menu {
            display: flex;
            flex-direction: column;
            gap: 6px;
            margin-top: 24px;
            flex-grow: 1;
        }

        .nav-item {
            display: flex;
            align-items: center;
            gap: 12px;
            padding: 12px 14px;
            border-radius: var(--radius-md);
            color: var(--text-secondary);
            text-decoration: none;
            font-size: 13px;
            font-weight: 600;
            cursor: pointer;
            transition: all 0.2s ease;
            position: relative;
        }

        .nav-item:hover {
            color: var(--text-primary);
            background: rgba(255, 255, 255, 0.04);
        }

        .nav-item.active {
            color: #38BDF8;
            background: rgba(56, 189, 248, 0.12);
        }

        .nav-item.active::before {
            content: '';
            position: absolute;
            left: 0;
            top: 25%;
            height: 50%;
            width: 3px;
            background: var(--accent-blue);
            border-radius: 0 4px 4px 0;
        }

        .badge-count {
            margin-left: auto;
            background: rgba(244, 63, 94, 0.2);
            color: var(--accent-red);
            padding: 2px 7px;
            border-radius: 20px;
            font-size: 11px;
            font-weight: 700;
        }

        .sidebar-bottom {
            padding-top: 16px;
            border-top: 1px solid var(--border-card);
            display: flex;
            flex-direction: column;
            gap: 12px;
        }

        .quick-connection {
            background: rgba(255, 255, 255, 0.03);
            border: 1px solid var(--border-card);
            border-radius: var(--radius-md);
            padding: 12px;
            display: flex;
            align-items: center;
            gap: 10px;
        }

        .dot-status {
            width: 10px;
            height: 10px;
            border-radius: 50%;
            background: var(--accent-red);
            box-shadow: 0 0 10px rgba(244, 63, 94, 0.6);
            transition: all 0.3s ease;
        }

        .dot-status.connected {
            background: var(--accent-green);
            box-shadow: 0 0 10px rgba(16, 185, 129, 0.6);
        }

        .main-content {
            flex-grow: 1;
            display: flex;
            flex-direction: column;
            height: 100vh;
            overflow-y: auto;
            background: transparent;
        }

        .topbar {
            height: 70px;
            padding: 0 32px;
            display: flex;
            align-items: center;
            justify-content: space-between;
            border-bottom: 1px solid var(--border-card);
            background: rgba(8, 11, 17, 0.6);
            backdrop-filter: blur(16px);
            position: sticky;
            top: 0;
            z-index: 5;
        }

        .topbar-title h1 {
            font-size: 20px;
            font-weight: 700;
        }

        .topbar-actions {
            display: flex;
            align-items: center;
            gap: 14px;
        }

        .btn-action {
            display: inline-flex;
            align-items: center;
            gap: 8px;
            padding: 8px 16px;
            border-radius: var(--radius-sm);
            font-size: 13px;
            font-weight: 600;
            cursor: pointer;
            border: 1px solid var(--border-card);
            background: rgba(255, 255, 255, 0.05);
            color: var(--text-primary);
            transition: all 0.2s ease;
        }

        .btn-action:hover {
            background: rgba(255, 255, 255, 0.1);
            border-color: rgba(255, 255, 255, 0.15);
        }

        .btn-primary {
            background: linear-gradient(135deg, #38BDF8, #6366F1);
            border: none;
            color: #FFFFFF;
        }

        .content-area {
            padding: 28px 32px;
            max-width: 1440px;
            width: 100%;
            margin: 0 auto;
            display: flex;
            flex-direction: column;
            gap: 28px;
        }

        .tab-pane {
            display: none;
            flex-direction: column;
            gap: 24px;
            animation: fadeIn 0.25s ease forwards;
        }

        .tab-pane.active {
            display: flex;
        }

        @keyframes fadeIn {
            from { opacity: 0; transform: translateY(6px); }
            to { opacity: 1; transform: translateY(0); }
        }

        .glass-card {
            background: var(--bg-card);
            border: 1px solid var(--border-card);
            border-radius: var(--radius-lg);
            padding: 24px;
            backdrop-filter: blur(20px);
            box-shadow: 0 12px 36px rgba(0, 0, 0, 0.35);
        }

        .grid-overview {
            display: grid;
            grid-template-columns: repeat(auto-fit, minmax(280px, 1fr));
            gap: 20px;
        }

        .metric-card {
            display: flex;
            align-items: center;
            gap: 16px;
            padding: 20px;
            background: rgba(255, 255, 255, 0.02);
            border: 1px solid var(--border-card);
            border-radius: var(--radius-lg);
        }

        .metric-icon {
            width: 48px;
            height: 48px;
            border-radius: var(--radius-md);
            display: flex;
            align-items: center;
            justify-content: center;
            font-size: 22px;
            background: rgba(56, 189, 248, 0.1);
            color: var(--accent-blue);
        }

        .call-banner {
            display: none;
            background: linear-gradient(135deg, rgba(244, 63, 94, 0.2), rgba(99, 102, 241, 0.2));
            border: 1px solid rgba(244, 63, 94, 0.4);
            border-radius: var(--radius-lg);
            padding: 20px 28px;
            align-items: center;
            justify-content: space-between;
            animation: pulseGlow 2s infinite ease-in-out;
        }

        @keyframes pulseGlow {
            0%, 100% { box-shadow: 0 0 20px rgba(244, 63, 94, 0.2); }
            50% { box-shadow: 0 0 40px rgba(244, 63, 94, 0.45); }
        }

        .call-banner.active {
            display: flex;
        }

        .btn-call-action {
            padding: 12px 24px;
            border-radius: 30px;
            font-weight: 700;
            font-size: 14px;
            cursor: pointer;
            border: none;
            display: inline-flex;
            align-items: center;
            gap: 8px;
            transition: all 0.2s ease;
        }

        .btn-call-answer {
            background: var(--accent-green);
            color: #FFFFFF;
            box-shadow: 0 4px 16px rgba(16, 185, 129, 0.4);
        }

        .btn-call-reject {
            background: var(--accent-red);
            color: #FFFFFF;
            box-shadow: 0 4px 16px rgba(244, 63, 94, 0.4);
        }

        .sms-layout {
            display: grid;
            grid-template-columns: 340px 1fr;
            height: 680px;
            border: 1px solid var(--border-card);
            border-radius: var(--radius-lg);
            background: var(--bg-card);
            overflow: hidden;
        }

        .sms-threads {
            background: rgba(10, 14, 22, 0.7);
            border-right: 1px solid var(--border-card);
            overflow-y: auto;
            display: flex;
            flex-direction: column;
        }

        .sms-thread-item {
            padding: 16px;
            border-bottom: 1px solid rgba(255, 255, 255, 0.04);
            cursor: pointer;
            transition: background 0.15s ease;
        }

        .sms-thread-item:hover, .sms-thread-item.active {
            background: rgba(56, 189, 248, 0.08);
        }

        .sms-conversation {
            display: flex;
            flex-direction: column;
            height: 100%;
        }

        .sms-chat-history {
            flex-grow: 1;
            padding: 24px;
            overflow-y: auto;
            display: flex;
            flex-direction: column;
            gap: 14px;
        }

        .chat-bubble {
            max-width: 65%;
            padding: 12px 18px;
            border-radius: 18px;
            font-size: 13.5px;
            line-height: 1.5;
            position: relative;
        }

        .chat-bubble.incoming {
            align-self: flex-start;
            background: rgba(255, 255, 255, 0.07);
            color: var(--text-primary);
            border-bottom-left-radius: 4px;
        }

        .chat-bubble.outgoing {
            align-self: flex-end;
            background: linear-gradient(135deg, #0284C7, #4F46E5);
            color: #FFFFFF;
            border-bottom-right-radius: 4px;
            box-shadow: 0 4px 14px rgba(2, 132, 199, 0.25);
        }

        .sms-compose-bar {
            padding: 16px 20px;
            background: rgba(10, 14, 22, 0.85);
            border-top: 1px solid var(--border-card);
            display: flex;
            gap: 12px;
            align-items: center;
        }

        .input-custom {
            background: var(--bg-input);
            border: 1px solid var(--border-card);
            border-radius: var(--radius-md);
            padding: 12px 16px;
            color: var(--text-primary);
            font-size: 13px;
            outline: none;
            width: 100%;
            transition: all 0.2s ease;
        }

        .input-custom:focus {
            border-color: var(--accent-blue);
            box-shadow: 0 0 12px var(--glow-cyan);
        }

        .dialer-grid {
            display: grid;
            grid-template-columns: repeat(3, 1fr);
            gap: 14px;
            max-width: 320px;
            margin: 0 auto;
        }

        .dialer-btn {
            background: rgba(255, 255, 255, 0.04);
            border: 1px solid var(--border-card);
            border-radius: var(--radius-md);
            height: 64px;
            font-size: 20px;
            font-weight: 700;
            color: var(--text-primary);
            cursor: pointer;
            display: flex;
            flex-direction: column;
            align-items: center;
            justify-content: center;
            transition: all 0.15s ease;
        }

        .dialer-btn:hover {
            background: rgba(56, 189, 248, 0.15);
            border-color: var(--accent-blue);
        }

        .pin-box {
            font-family: 'JetBrains Mono', monospace;
            font-size: 32px;
            letter-spacing: 8px;
            font-weight: 800;
            color: #38BDF8;
            padding: 16px 24px;
            background: rgba(56, 189, 248, 0.1);
            border: 1px dashed var(--accent-blue);
            border-radius: var(--radius-md);
            text-align: center;
            display: inline-block;
        }
    </style>
</head>
<body>
    <div class="app-layout">
        <aside class="sidebar">
            <div class="brand">
                <div class="brand-icon">🍏</div>
                <div class="brand-text">
                    <h2>Mac Sync</h2>
                    <span>macOS Ekosistemi</span>
                </div>
            </div>

            <nav class="nav-menu">
                <div class="nav-item active" onclick="switchTab('tab-overview', this)">
                    <span>📊</span> Genel Bakış
                </div>
                <div class="nav-item" onclick="switchTab('tab-calls', this)">
                    <span>📞</span> Telefon & Aramalar
                </div>
                <div class="nav-item" onclick="switchTab('tab-sms', this)">
                    <span>💬</span> Mesajlar (SMS)
                    <span class="badge-count" id="smsCountBadge">0</span>
                </div>
                <div class="nav-item" onclick="switchTab('tab-media', this)">
                    <span>🎵</span> Medya & Ses
                </div>
                <div class="nav-item" onclick="switchTab('tab-clipboard', this)">
                    <span>📋</span> Evrensel Pano
                </div>
                <div class="nav-item" onclick="switchTab('tab-pairing', this)">
                    <span>🔒</span> Güvenlik & Eşleşme
                </div>
                <div class="nav-item" onclick="switchTab('tab-files', this)">
                    <span>📁</span> Dosya Paylaşımı
                    <span class="badge-count" id="filesCountBadge" style="display:none;">0</span>
                </div>
                <div class="nav-item" onclick="switchTab('tab-contacts', this)">
                    <span>👥</span> Rehber &amp; Kişiler
                    <span class="badge-count" id="contactsCountBadge" style="display:none;">0</span>
                </div>
                <div class="nav-item" onclick="switchTab('tab-tabsharing', this)">
                    <span>🌐</span> Sekme Paylaşımı
                </div>
                <div class="nav-item" onclick="switchTab('tab-photos', this)">
                    <span>📸</span> Fotoğraflar
                    <span class="badge-count" id="photosCountBadge" style="display:none;">0</span>
                </div>
            </nav>

            <div class="sidebar-bottom">
                <div class="quick-connection">
                    <div class="dot-status" id="sidebarDot"></div>
                    <div style="font-size: 12px;">
                        <div id="sidebarConnText" style="font-weight: 700;">Bağlantı Aranıyor</div>
                        <div id="sidebarDeviceName" style="color: var(--text-muted); font-size: 11px;">Cihaz Yok</div>
                    </div>
                </div>
            </div>
        </aside>

        <main class="main-content">
            <header class="topbar">
                <div class="topbar-title">
                    <h1 id="pageTitle">Genel Bakış</h1>
                </div>
                <div class="topbar-actions">
                    <button class="btn-action" style="background:rgba(255,255,255,0.06); border:1px solid var(--border-card); color:var(--text-primary);" onclick="remoteAction('LOCK')" title="Mac'i Kilitle">🔒 Kilitle</button>
                    <button class="btn-action" style="background:rgba(255,255,255,0.06); border:1px solid var(--border-card); color:var(--text-primary);" onclick="remoteAction('SLEEP')" title="Mac'i Uyku Moduna Al">🌙 Uyku</button>
                    <div id="pairingBadge" style="font-size: 12px; padding: 6px 12px; border-radius: 20px; font-weight: 700; background: rgba(245, 158, 11, 0.15); color: var(--accent-yellow);">
                        🟡 Eşleşme Bekleniyor
                    </div>
                    <a href="/download" class="btn-action">
                        <span>📲</span> APK İndir
                    </a>
                </div>
            </header>

            <div class="content-area">
                <!-- LIVE INCOMING / ACTIVE CALL BANNER -->
                <div class="call-banner" id="globalCallBanner">
                    <div style="display: flex; align-items: center; gap: 16px;">
                        <div style="font-size: 32px;" id="callBannerIcon">📞</div>
                        <div>
                            <div style="font-size: 12px; text-transform: uppercase; font-weight: 700; color: #F43F5E;" id="callBannerType">GELEN ARAMA</div>
                            <div style="font-size: 20px; font-weight: 800;" id="callBannerCaller">Ahmet Yılmaz</div>
                            <div style="font-size: 13px; color: var(--text-secondary);" id="callBannerNumber">+90 555 123 45 67</div>
                        </div>
                    </div>
                    <div style="display: flex; gap: 12px;">
                        <button class="btn-call-action btn-call-answer" id="btnBannerAnswer" onclick="triggerCallAction('ANSWER')">
                            <span>📞</span> Yanıtla
                        </button>
                        <button class="btn-call-action btn-call-reject" id="btnBannerReject" onclick="triggerCallAction('REJECT')">
                            <span>✕</span> Reddet
                        </button>
                    </div>
                </div>

                <!-- TAB: OVERVIEW -->
                <div id="tab-overview" class="tab-pane active">
                    <div class="grid-overview">
                        <div class="metric-card">
                            <div class="metric-icon">🔋</div>
                            <div>
                                <div style="font-size: 12px; color: var(--text-muted); font-weight: 600;">PİL DURUMU</div>
                                <div style="font-size: 24px; font-weight: 800;" id="cardBattery">--%</div>
                                <div style="font-size: 12px; color: var(--text-secondary);" id="cardCharging">Bekleniyor</div>
                            </div>
                        </div>
                        <div class="metric-card">
                            <div class="metric-icon">📱</div>
                            <div>
                                <div style="font-size: 12px; color: var(--text-muted); font-weight: 600;">BAĞLI TELEFON</div>
                                <div style="font-size: 18px; font-weight: 800;" id="cardDeviceName">Bağlı Değil</div>
                                <div style="font-size: 12px; color: var(--text-secondary);" id="cardDeviceModel">-</div>
                            </div>
                        </div>
                        <div class="metric-card">
                            <div class="metric-icon">💬</div>
                            <div>
                                <div style="font-size: 12px; color: var(--text-muted); font-weight: 600;">SMS SAYISI</div>
                                <div style="font-size: 24px; font-weight: 800;" id="cardSmsTotal">0</div>
                                <div style="font-size: 12px; color: var(--accent-blue); cursor: pointer;" onclick="syncSms()">Senkronize Et ⟳</div>
                            </div>
                        </div>
                        <div class="metric-card">
                            <div class="metric-icon">🌐</div>
                            <div>
                                <div style="font-size: 12px; color: var(--text-muted); font-weight: 600;">MAC SUNUCU IP</div>
                                <div style="font-size: 18px; font-weight: 800;" id="cardIp">127.0.0.1</div>
                                <div style="font-size: 12px; color: var(--text-secondary);">Port: 42424</div>
                            </div>
                        </div>
                    </div>

                    <!-- Media Card (Dual Mac & Phone) on Overview -->
                    <div class="glass-card" style="margin-bottom: 24px;">
                        <div style="display:flex; justify-content:space-between; align-items:center; margin-bottom:16px;">
                            <h3 style="font-size:16px; font-weight:700;">🎵 Medya &amp; Süre Kontrolü (Tüm Cihazlar)</h3>
                            <button class="btn-action" style="padding:6px 14px; font-size:12px;" onclick="switchTab('media')">Detaylı Medya 🎧</button>
                        </div>

                        <div style="display:grid; grid-template-columns: 1fr 1fr; gap:16px;">
                            <!-- Mac Media Box -->
                            <div style="background:rgba(0,0,0,0.3); border:1px solid rgba(56,189,248,0.25); border-radius:var(--radius-md); padding:16px;">
                                <div style="display:flex; justify-content:space-between; align-items:center; margin-bottom:8px;">
                                    <span style="font-size:11px; font-weight:800; color:var(--accent-blue);">🍏 BU MAC</span>
                                    <span id="macMediaTag" style="font-size:10px; background:rgba(56,189,248,0.15); color:var(--accent-blue); padding:2px 6px; border-radius:4px; font-weight:700;">HAZIR</span>
                                </div>
                                <div style="display:flex; align-items:center; gap:10px; margin-bottom:10px;">
                                    <div style="width:40px; height:40px; border-radius:8px; background:linear-gradient(135deg, #0284C7, #38BDF8); display:flex; align-items:center; justify-content:center; font-size:18px;">🎵</div>
                                    <div style="flex:1; overflow:hidden;">
                                        <div id="macTrackTitle" style="font-size:13px; font-weight:700; white-space:nowrap; overflow:hidden; text-overflow:ellipsis;">Mac Medyası Yok</div>
                                        <div id="macTrackArtist" style="font-size:11px; color:var(--text-secondary); white-space:nowrap; overflow:hidden; text-overflow:ellipsis;">Spotify / Apple Music açın</div>
                                    </div>
                                </div>
                                <div style="margin-bottom:10px;">
                                    <input type="range" id="macMediaSeek" min="0" max="100" value="0" style="width:100%; accent-color:#38BDF8; cursor:pointer;" onmousedown="isSeekingMedia=true" ontouchstart="isSeekingMedia=true" oninput="updateSeekTimePreview(this.value)" onchange="seekMacMedia(this.value)">
                                    <div style="display:flex; justify-content:space-between; font-size:10px; color:var(--text-secondary); margin-top:2px;">
                                        <span id="macMediaCurTime">00:00</span>
                                        <span id="macMediaTotalTime">00:00</span>
                                    </div>
                                </div>
                                <div style="display:flex; justify-content:center; gap:6px; flex-wrap:wrap;">
                                    <button class="btn-action" style="padding:6px 10px; font-size:11px;" onclick="seekRelative(-15)" title="15 Saniye Geri">⏪ 15s</button>
                                    <button class="btn-action" style="padding:6px 10px; font-size:11px;" onclick="sendMediaCmd('PREVIOUS')" title="Önceki Parça">⏮</button>
                                    <button class="btn-action btn-primary" style="padding:6px 14px; font-size:11px;" onclick="sendMediaCmd('PLAY_PAUSE')">⏯ Oynat</button>
                                    <button class="btn-action" style="padding:6px 10px; font-size:11px;" onclick="sendMediaCmd('NEXT')" title="Sonraki Parça">⏭</button>
                                    <button class="btn-action" style="padding:6px 10px; font-size:11px;" onclick="seekRelative(15)" title="15 Saniye İleri">⏩ 15s</button>
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
                                    <button class="btn-action" style="padding:6px 10px; font-size:11px;" onclick="seekPhoneRelative(-15)" title="Telefonda 15s Geri">⏪ 15s</button>
                                    <button class="btn-action" style="padding:6px 10px; font-size:11px;" onclick="sendPhoneCmd('PREV')" title="Telefonda Önceki">⏮</button>
                                    <button class="btn-action btn-primary" style="padding:6px 14px; font-size:11px; background:#10B981;" onclick="sendPhoneCmd('PLAY_PAUSE')">⏯ Oynat</button>
                                    <button class="btn-action" style="padding:6px 10px; font-size:11px;" onclick="sendPhoneCmd('NEXT')" title="Telefonda Sonraki">⏭</button>
                                    <button class="btn-action" style="padding:6px 10px; font-size:11px;" onclick="seekPhoneRelative(15)" title="Telefonda 15s İleri">⏩ 15s</button>
                                </div>
                            </div>
                        </div>
                    </div>

                    <div class="glass-card">
                        <h3 style="font-size: 16px; font-weight: 700; margin-bottom: 16px;">📱 Son Gelen Bildirimler</h3>
                        <div id="notifsContainer" style="display: flex; flex-direction: column; gap: 10px;">
                            <div style="color: var(--text-muted); font-size: 13px;">Henüz bildirim gelmedi.</div>
                        </div>
                    </div>
                </div>

                <!-- TAB: CALLS -->
                <div id="tab-calls" class="tab-pane">
                    <div style="display: grid; grid-template-columns: 1fr 340px; gap: 24px;">
                        <div class="glass-card">
                            <h3 style="font-size: 16px; font-weight: 700; margin-bottom: 16px;">📞 Canlı Görüşme Durumu</h3>
                            <div id="activeCallInfoBox" style="padding: 24px; background: rgba(255,255,255,0.02); border-radius: var(--radius-md); border: 1px solid var(--border-card); text-align: center;">
                                <div style="font-size: 48px; margin-bottom: 8px;">📴</div>
                                <div style="font-size: 18px; font-weight: 700;" id="activeCallStatusTitle">Aktif Görüşme Yok</div>
                                <div style="font-size: 13px; color: var(--text-muted); margin-top: 4px;" id="activeCallTimer">Süre: 00:00</div>
                                <div style="margin-top: 20px; display: flex; gap: 12px; justify-content: center;">
                                    <button class="btn-action" onclick="triggerCallAction('SET_SPEAKER', null, true)">🔊 Hoparlör</button>
                                    <button class="btn-action" onclick="triggerCallAction('SET_MUTE', null, true)">🔇 Mikrofonu Kapat</button>
                                    <button class="btn-call-action btn-call-reject" onclick="triggerCallAction('HANGUP')">Aramayı Sonlandır</button>
                                </div>
                            </div>
                        </div>

                        <div class="glass-card">
                            <h3 style="font-size: 16px; font-weight: 700; margin-bottom: 16px;">🔢 Numara Çevirici</h3>
                            <input type="text" id="dialInput" class="input-custom" placeholder="Numara girin veya tuşlayın..." style="margin-bottom: 16px; font-size: 16px; font-weight: 700; text-align: center;">
                            <div class="dialer-grid">
                                <div class="dialer-btn" onclick="dialDigit('1')">1</div>
                                <div class="dialer-btn" onclick="dialDigit('2')">2</div>
                                <div class="dialer-btn" onclick="dialDigit('3')">3</div>
                                <div class="dialer-btn" onclick="dialDigit('4')">4</div>
                                <div class="dialer-btn" onclick="dialDigit('5')">5</div>
                                <div class="dialer-btn" onclick="dialDigit('6')">6</div>
                                <div class="dialer-btn" onclick="dialDigit('7')">7</div>
                                <div class="dialer-btn" onclick="dialDigit('8')">8</div>
                                <div class="dialer-btn" onclick="dialDigit('9')">9</div>
                                <div class="dialer-btn" onclick="dialDigit('*')">*</div>
                                <div class="dialer-btn" onclick="dialDigit('0')">0</div>
                                <div class="dialer-btn" onclick="dialDigit('#')">#</div>
                            </div>
                            <button class="btn-action btn-primary" style="width: 100%; margin-top: 16px; padding: 14px; font-size: 15px; justify-content: center;" onclick="makeCall()">
                                <span>📞</span> Telefon Üzerinden Ara
                            </button>
                        </div>
                    </div>
                </div>

                <!-- TAB: SMS -->
                <div id="tab-sms" class="tab-pane">
                    <div class="sms-layout">
                        <div class="sms-threads" id="smsThreadsList">
                            <div style="padding: 20px; color: var(--text-muted); font-size: 13px;">Mesajlar yükleniyor...</div>
                        </div>
                        <div class="sms-conversation">
                            <div style="padding: 16px 20px; border-bottom: 1px solid var(--border-card); background: rgba(10,14,22,0.7); display: flex; justify-content: space-between; align-items: center;">
                                <div>
                                    <div style="font-weight: 700; font-size: 15px;" id="smsCurrentContact">Kişi Seçin</div>
                                    <div style="font-size: 12px; color: var(--text-muted);" id="smsCurrentNumber">-</div>
                                </div>
                                <button class="btn-action" onclick="syncSms()">⟳ Mesajları Yenile</button>
                            </div>
                            <div class="sms-chat-history" id="smsChatHistory">
                                <div style="margin: auto; color: var(--text-muted); font-size: 13px;">Görüntülemek için soldan bir konuşma seçin.</div>
                            </div>
                            <div class="sms-compose-bar">
                                <input type="text" id="smsInputText" class="input-custom" placeholder="Mac'ten SMS mesajı yazın..." onkeydown="if(event.key==='Enter') sendSmsMessage()">
                                <button class="btn-action btn-primary" onclick="sendSmsMessage()">
                                    <span>➤</span> Gönder
                                </button>
                            </div>
                        </div>
                    </div>
                </div>

                <!-- TAB: MEDIA -->
                <div id="tab-media" class="tab-pane">
                    <div style="display: grid; grid-template-columns: 1fr 1fr; gap: 24px;">
                        <!-- Full Mac Media Card -->
                        <div class="glass-card">
                            <div style="display:flex; justify-content:space-between; align-items:center; margin-bottom:16px;">
                                <h3 style="font-size:17px; font-weight:700;">🍏 Bu Mac'te Çalan Medya</h3>
                                <span id="fullMacTag" style="font-size:11px; background:rgba(56,189,248,0.15); color:var(--accent-blue); padding:3px 8px; border-radius:4px; font-weight:700;">HAZIR</span>
                            </div>
                            <div style="display:flex; align-items:center; gap:20px; background:rgba(0,0,0,0.3); padding:20px; border-radius:var(--radius-lg); margin-bottom:16px;">
                                <div style="width:72px; height:72px; border-radius:var(--radius-md); background:linear-gradient(135deg, #0284C7, #38BDF8); display:flex; align-items:center; justify-content:center; font-size:32px; box-shadow:0 6px 24px rgba(56,189,248,0.25);">🎵</div>
                                <div style="flex:1; overflow:hidden;">
                                    <div style="font-size:11px; font-weight:700; color:var(--accent-blue); text-transform:uppercase; margin-bottom:4px;">MACOS SİSTEM MEDYASI</div>
                                    <h3 id="fullMacTitle" style="font-size:18px; font-weight:800; white-space:nowrap; overflow:hidden; text-overflow:ellipsis;">Mac Medyası Yok</h3>
                                    <p id="fullMacArtist" style="font-size:13px; color:var(--text-secondary); margin-top:2px; white-space:nowrap; overflow:hidden; text-overflow:ellipsis;">Spotify, Apple Music veya YouTube açın</p>
                                </div>
                            </div>

                            <div style="margin-bottom:16px; background:rgba(0,0,0,0.2); padding:14px; border-radius:var(--radius-md);">
                                <input type="range" id="fullMediaSeek" min="0" max="100" value="0" style="width:100%; accent-color:#38BDF8; cursor:pointer;" onmousedown="isSeekingMedia=true" ontouchstart="isSeekingMedia=true" oninput="updateSeekTimePreview(this.value)" onchange="seekMacMedia(this.value)">
                                <div style="display:flex; justify-content:space-between; font-size:11px; color:var(--text-secondary); margin-top:4px;">
                                    <span id="fullMediaCurTime">00:00</span>
                                    <span id="fullMediaTotalTime">00:00</span>
                                </div>
                            </div>

                            <div style="display:flex; justify-content:center; gap:8px; flex-wrap:wrap; margin-bottom:12px;">
                                <button class="btn-action" style="padding:10px 18px; font-size:13px;" onclick="seekRelative(-15)">⏪ 15s Geri</button>
                                <button class="btn-action" style="padding:10px 18px; font-size:13px;" onclick="sendMediaCmd('PREVIOUS')">⏮ Önceki</button>
                                <button class="btn-action btn-primary" style="padding:10px 24px; font-size:14px;" onclick="sendMediaCmd('PLAY_PAUSE')">⏯ Oynat / Duraklat</button>
                                <button class="btn-action" style="padding:10px 18px; font-size:13px;" onclick="sendMediaCmd('NEXT')">⏭ Sonraki</button>
                                <button class="btn-action" style="padding:10px 18px; font-size:13px;" onclick="seekRelative(15)">⏩ 15s İleri</button>
                            </div>
                            <div style="display:flex; justify-content:center; gap:10px;">
                                <button class="btn-action" style="padding:8px 16px; font-size:12px;" onclick="sendMediaCmd('VOLUME_DOWN')">🔉 Mac Ses -</button>
                                <button class="btn-action" style="padding:8px 16px; font-size:12px;" onclick="sendMediaCmd('VOLUME_UP')">🔊 Mac Ses +</button>
                            </div>
                        </div>

                        <!-- Full Phone Media Card -->
                        <div class="glass-card">
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

                            <div style="margin-bottom:16px; background:rgba(0,0,0,0.2); padding:14px; border-radius:var(--radius-md);">
                                <input type="range" id="fullPhoneMediaSeek" min="0" max="100" value="0" style="width:100%; accent-color:#10B981; cursor:pointer;" onmousedown="isSeekingPhoneMedia=true" ontouchstart="isSeekingPhoneMedia=true" oninput="updatePhoneSeekTimePreview(this.value)" onchange="seekPhoneMedia(this.value)">
                                <div style="display:flex; justify-content:space-between; font-size:11px; color:var(--text-secondary); margin-top:4px;">
                                    <span id="fullPhoneMediaCurTime">00:00</span>
                                    <span id="fullPhoneMediaTotalTime">00:00</span>
                                </div>
                            </div>

                            <div style="display:flex; justify-content:center; gap:8px; flex-wrap:wrap; margin-bottom:12px;">
                                <button class="btn-action" style="padding:10px 18px; font-size:13px;" onclick="seekPhoneRelative(-15)">⏪ 15s Geri</button>
                                <button class="btn-action" style="padding:10px 18px; font-size:13px;" onclick="sendPhoneCmd('PREV')">⏮ Önceki</button>
                                <button class="btn-action btn-primary" style="padding:10px 24px; font-size:14px; background:#10B981;" onclick="sendPhoneCmd('PLAY_PAUSE')">⏯ Oynat / Duraklat</button>
                                <button class="btn-action" style="padding:10px 18px; font-size:13px;" onclick="sendPhoneCmd('NEXT')">⏭ Sonraki</button>
                                <button class="btn-action" style="padding:10px 18px; font-size:13px;" onclick="seekPhoneRelative(15)">⏩ 15s İleri</button>
                            </div>
                        </div>
                    </div>
                </div>

                <!-- TAB: CLIPBOARD -->
                <div id="tab-clipboard" class="tab-pane">
                    <div class="glass-card">
                        <h3 style="font-size: 16px; font-weight: 700; margin-bottom: 16px;">📋 Evrensel Pano</h3>
                        <p style="font-size: 13px; color: var(--text-secondary); margin-bottom: 16px;">
                            Mac'te kopyaladığınız herhangi bir metin veya görsel (⌘C / Ekran Alıntısı) anında Android telefonunuza aktarılır.
                        </p>

                        <!-- Görsel Pano Önizleme Alanı -->
                        <div id="clipImageWrapper" style="display:none; margin-bottom:20px; padding:16px; background:rgba(0,0,0,0.3); border:1px solid var(--border-card); border-radius:var(--radius-md);">
                            <div style="font-size:12px; font-weight:700; color:var(--accent-blue); margin-bottom:10px; display:flex; align-items:center; gap:8px;">
                                <span>🖼️ Panodaki Görsel (PNG)</span>
                                <span id="clipImageSize" style="color:var(--text-muted); font-size:11px; font-weight:normal;"></span>
                            </div>
                            <img id="clipImagePreview" src="" style="max-height:260px; max-width:100%; border-radius:8px; object-fit:contain; border:1px solid rgba(255,255,255,0.1); background:#111;" />
                            <div style="margin-top:12px; display:flex; gap:10px;">
                                <a id="clipImageDownloadBtn" href="#" download="clipboard.png" class="btn-action" style="font-size:12px; text-decoration:none; display:inline-flex; align-items:center; gap:6px;">📥 Görseli İndir</a>
                            </div>
                        </div>

                        <textarea id="clipInputText" class="input-custom" rows="5" placeholder="Telefona göndermek için bir metin yazın veya yapıştırın..."></textarea>
                        <div style="margin-top: 14px; display: flex; justify-content: flex-end;">
                            <button class="btn-action btn-primary" onclick="sendClipboard()">
                                <span>📋</span> Telefona Gönder
                            </button>
                        </div>
                    </div>
                </div>

                <!-- TAB: PAIRING -->
                <div id="tab-pairing" class="tab-pane">
                    <div class="glass-card" style="text-align: center; max-width: 600px; margin: 0 auto;">
                        <h3 style="font-size: 20px; font-weight: 800; margin-bottom: 12px;">🔒 6 Haneli Güvenli PIN Eşleştirme</h3>
                        <p style="font-size: 13px; color: var(--text-secondary); line-height: 1.6; margin-bottom: 24px;">
                            Telefonunuz ilk kez bağlandığında ekranda beliren onay penceresinde bu PIN kodunu doğrulayın. Eşleşme sağlandığında bağlantınız güvenli şifreli token ile mühürlenir.
                        </p>
                        <div class="pin-box" id="pinDisplay">------</div>
                        <div style="margin-top: 24px; font-size: 13px; color: var(--text-muted);" id="pairingStatusText">
                            Durum: Eşleşme bekleniyor
                        </div>
                        <div style="margin-top: 24px;">
                            <button class="btn-action" style="color: var(--accent-red); border-color: rgba(244,63,94,0.3);" onclick="resetPairing()">
                                <span>🔌</span> Eşleşmeyi Sıfırla ve Yeni Kod Üret
                            </button>
                        </div>
                    </div>
                </div>

                <!-- TAB: FILES -->
                <div id="tab-files" class="tab-pane">
                    <div style="display:flex; flex-direction:column; gap:20px;">
                        <div class="glass-card">
                            <div style="display:flex; justify-content:space-between; align-items:center; margin-bottom:16px;">
                                <h3 style="font-size: 16px; font-weight: 700;">📁 Wi-Fi Dosya Gönderimi (Mac ➡️ Telefon)</h3>
                                <button class="btn-action" onclick="openDownloadsFolder()">📂 İndirilenler Klasörünü Aç</button>
                            </div>
                            <div id="dropZone" style="border: 2px dashed rgba(56, 189, 248, 0.4); border-radius: var(--radius-lg); padding: 40px 20px; text-align: center; background: rgba(56, 189, 248, 0.04); cursor: pointer; transition: all 0.3s ease;">
                                <div style="font-size: 40px; margin-bottom: 12px;">📤</div>
                                <div style="font-size: 15px; font-weight: 700; color: var(--text-primary); margin-bottom: 6px;">Dosyaları buraya sürükleyip bırakın veya tıklayın</div>
                                <div style="font-size: 12px; color: var(--text-secondary);">Fotoğraflar, videolar ve belgeler doğrudan telefonun Downloads klasörüne aktarılır</div>
                                <input type="file" id="filePickerInput" multiple style="display: none;" onchange="handleFileSelect(event)">
                            </div>
                            <div id="uploadProgressBox" style="display:none; margin-top:16px; padding:12px 16px; background:rgba(0,0,0,0.3); border-radius:var(--radius-md);">
                                <div style="display:flex; justify-content:space-between; font-size:12px; margin-bottom:6px;">
                                    <span id="uploadFileName">Aktarılıyor...</span>
                                    <span id="uploadPercent">0%</span>
                                </div>
                                <div style="width:100%; height:6px; background:rgba(255,255,255,0.1); border-radius:99px; overflow:hidden;">
                                    <div id="uploadProgressBar" style="width:0%; height:100%; background:linear-gradient(90deg, #38BDF8, #6366F1); transition:width 0.2s;"></div>
                                </div>
                            </div>
                        </div>

                        <div class="glass-card">
                            <div style="display:flex; justify-content:space-between; align-items:center; margin-bottom:16px;">
                                <h3 style="font-size: 16px; font-weight: 700;">🔄 Aktarılan Dosyalar Geçmişi</h3>
                                <button class="btn-action" style="font-size:12px; padding:4px 10px;" onclick="loadTransferredFiles()">Yenile 🔄</button>
                            </div>
                            <div style="overflow-x:auto;">
                                <table style="width:100%; border-collapse:collapse; font-size:13px; text-align:left;">
                                    <thead>
                                        <tr style="border-bottom:1px solid var(--border-card); color:var(--text-muted); font-size:11px; text-transform:uppercase;">
                                            <th style="padding:10px 14px;">Yön</th>
                                            <th style="padding:10px 14px;">Dosya Adı</th>
                                            <th style="padding:10px 14px;">Boyut</th>
                                            <th style="padding:10px 14px;">Tarih</th>
                                            <th style="padding:10px 14px; text-align:right;">İşlem</th>
                                        </tr>
                                    </thead>
                                    <tbody id="filesTableBody">
                                        <tr><td colspan="5" style="padding:24px; text-align:center; color:var(--text-muted);">Henüz aktarılmış dosya yok</td></tr>
                                    </tbody>
                                </table>
                            </div>
                        </div>
                    </div>
                </div>

                <!-- TAB: CONTACTS -->
                <div id="tab-contacts" class="tab-pane">
                    <div class="glass-card">
                        <div style="display: flex; justify-content: space-between; align-items: center; margin-bottom: 20px; flex-wrap: wrap; gap: 14px;">
                            <div>
                                <h3 style="font-size: 16px; font-weight: 700;">👥 Telefon Rehberi &amp; Kişiler</h3>
                                <div style="font-size: 12px; color: var(--text-secondary); margin-top: 4px;">
                                    Telefonunuzdaki kişilerle Mac üzerinden doğrudan arama başlatabilir veya SMS gönderebilirsiniz.
                                </div>
                            </div>
                            <div style="display: flex; gap: 10px; align-items: center;">
                                <span class="badge-count" id="contactsHeaderBadge" style="font-size: 12px; padding: 4px 12px; border-radius: 6px; display: inline-block;">0 Kişi</span>
                                <button class="btn-action" onclick="refreshContacts()">🔄 Telefondan Yenile</button>
                            </div>
                        </div>

                        <!-- Search Bar -->
                        <div style="margin-bottom: 20px;">
                            <input type="text" id="contactsSearchInput" class="input-custom" placeholder="🔍 İsim veya telefon numarası ile hızlı filtreleyin..." oninput="filterContacts()" style="width: 100%; box-sizing: border-box;">
                        </div>

                        <!-- Contacts Grid Container -->
                        <div id="contactsGrid" style="display: grid; grid-template-columns: repeat(auto-fill, minmax(280px, 1fr)); gap: 14px; max-height: calc(100vh - 300px); overflow-y: auto; padding-right: 4px;">
                            <div style="grid-column: 1 / -1; padding: 48px; text-align: center; color: var(--text-muted);">
                                Rehber yükleniyor veya telefondan senkronize ediliyor...
                            </div>
                        </div>
                    </div>
                </div>

                <!-- TAB: TAB SHARING -->
                <div id="tab-tabsharing" class="tab-pane">
                    <div style="display: flex; flex-direction: column; gap: 20px;">
                        <div class="glass-card">
                            <div style="margin-bottom: 16px;">
                                <h3 style="font-size: 16px; font-weight: 700;">🌐 Sekme ve Web Sayfası Paylaşımı</h3>
                                <div style="font-size: 12px; color: var(--text-secondary); margin-top: 4px;">
                                    Mac'inizde veya telefonunuzda gezindiğiniz web sayfalarını diğer cihazın varsayılan tarayıcısında anında açın.
                                </div>
                            </div>

                            <div style="background: rgba(0,0,0,0.2); border: 1px solid var(--border-card); border-radius: var(--radius-md); padding: 18px; margin-bottom: 20px;">
                                <label style="display: block; font-size: 13px; font-weight: 700; color: var(--text-primary); margin-bottom: 8px;">Telefonda Açılacak Web Bağlantısı (URL):</label>
                                <div style="display: flex; gap: 10px; flex-wrap: wrap;">
                                    <input type="url" id="sendTabUrlInput" class="input-custom" placeholder="https://ornek-site.com/sayfa" style="flex: 1; min-width: 250px;">
                                    <button class="btn-action btn-primary" onclick="sendTabToPhone()" style="padding: 10px 20px; font-weight: 700;">🚀 Telefonda Anında Aç</button>
                                    <button class="btn-action" onclick="openLocalTab()" style="padding: 10px 16px;">🍏 Bu Mac'te Aç</button>
                                </div>
                            </div>

                            <div style="margin-bottom: 20px;">
                                <div style="font-size: 11px; font-weight: 700; color: var(--text-muted); text-transform: uppercase; margin-bottom: 8px;">Hızlı Kısayollar:</div>
                                <div style="display: flex; gap: 8px; flex-wrap: wrap;">
                                    <button class="btn-action" style="font-size: 12px; padding: 4px 10px;" onclick="fillAndSendUrl('https://github.com')">🐙 GitHub</button>
                                    <button class="btn-action" style="font-size: 12px; padding: 4px 10px;" onclick="fillAndSendUrl('https://youtube.com')">▶ YouTube</button>
                                    <button class="btn-action" style="font-size: 12px; padding: 4px 10px;" onclick="fillAndSendUrl('https://maps.google.com')">🗺 Google Maps</button>
                                    <button class="btn-action" style="font-size: 12px; padding: 4px 10px;" onclick="fillAndSendUrl('https://wikipedia.org')">📖 Wikipedia</button>
                                </div>
                            </div>

                            <div>
                                <h4 style="font-size: 14px; font-weight: 700; margin-bottom: 12px;">🔗 Son Paylaşılan Bağlantılar</h4>
                                <div id="sharedTabsList" style="display: flex; flex-direction: column; gap: 8px;">
                                    <div style="padding: 20px; text-align: center; color: var(--text-muted); font-size: 13px;">
                                        Henüz paylaşılan bir web sekmesi yok. Telefonda tarayıcıdan "Paylaş &gt; Android Sync" seçerek de buraya sekme gönderebilirsiniz!
                                    </div>
                                </div>
                            </div>
                        </div>
                    </div>
                </div>

                <!-- TAB: PHOTOS -->
                <div id="tab-photos" class="tab-pane">
                    <div class="glass-card">
                        <div style="display: flex; justify-content: space-between; align-items: center; margin-bottom: 20px; flex-wrap: wrap; gap: 14px;">
                            <div>
                                <h3 style="font-size: 16px; font-weight: 700;">📸 Telefon Fotoğraf Galerisi</h3>
                                <div style="font-size: 12px; color: var(--text-secondary); margin-top: 4px;">
                                    Telefonunuzdaki son fotoğrafları ve ekran görüntülerini anında görüntüleyin, Mac'e indirin veya panoya kopyalayın.
                                </div>
                            </div>
                            <div style="display: flex; gap: 10px; align-items: center;">
                                <span class="badge-count" id="photosHeaderBadge" style="font-size: 12px; padding: 4px 12px; border-radius: 6px; display: inline-block;">0 Fotoğraf</span>
                                <button class="btn-action" onclick="refreshPhotos()">🔄 Telefondan Yenile</button>
                            </div>
                        </div>

                        <!-- Gallery Filter & Search -->
                        <div style="display: flex; justify-content: space-between; align-items: center; margin-bottom: 20px; flex-wrap: wrap; gap: 12px;">
                            <div style="display: flex; gap: 8px;">
                                <button class="btn-action btn-primary" id="photoFilterAll" onclick="setPhotoFilter('all')" style="font-size: 12px; padding: 4px 12px;">Tümü</button>
                                <button class="btn-action" id="photoFilterCamera" onclick="setPhotoFilter('camera')" style="font-size: 12px; padding: 4px 12px;">📷 Kamera</button>
                                <button class="btn-action" id="photoFilterScreenshots" onclick="setPhotoFilter('screenshots')" style="font-size: 12px; padding: 4px 12px;">📱 Ekran Görüntüleri</button>
                            </div>
                            <input type="text" id="photosSearchInput" class="input-custom" placeholder="🔍 Dosya adıyla ara..." oninput="filterPhotos()" style="width: 240px;">
                        </div>

                        <!-- Photos Grid Container -->
                        <div id="photosGrid" style="display: grid; grid-template-columns: repeat(auto-fill, minmax(180px, 1fr)); gap: 16px; max-height: calc(100vh - 300px); overflow-y: auto; padding-right: 4px;">
                            <div style="grid-column: 1 / -1; padding: 48px; text-align: center; color: var(--text-muted);">
                                Fotoğraflar yükleniyor veya telefondan senkronize ediliyor...
                            </div>
                        </div>
                    </div>
                </div>

                <!-- Lightbox Modal for Full Photo Preview -->
                <div id="photoLightboxModal" style="display: none; position: fixed; inset: 0; background: rgba(5, 8, 16, 0.88); backdrop-filter: blur(12px); z-index: 9999; align-items: center; justify-content: center; padding: 20px;" onclick="if(event.target===this) closePhotoLightbox()">
                    <div style="background: #111827; border: 1px solid var(--border-card); border-radius: var(--radius-lg); max-width: 900px; width: 100%; max-height: 90vh; display: flex; flex-direction: column; overflow: hidden; box-shadow: 0 25px 50px -12px rgba(0,0,0,0.7);">
                        <div style="display: flex; justify-content: space-between; align-items: center; padding: 16px 20px; border-bottom: 1px solid var(--border-card);">
                            <div style="overflow: hidden;">
                                <h4 id="lightboxTitle" style="font-size: 15px; font-weight: 700; color: var(--text-primary); text-overflow: ellipsis; overflow: hidden; white-space: nowrap;">Fotoğraf Önizleme</h4>
                                <div id="lightboxSubtitle" style="font-size: 12px; color: var(--text-secondary); margin-top: 2px;">-</div>
                            </div>
                            <button class="btn-action" onclick="closePhotoLightbox()" style="padding: 4px 10px; font-size: 14px;">✕</button>
                        </div>
                        <div style="flex: 1; display: flex; align-items: center; justify-content: center; background: #030712; padding: 16px; overflow: hidden;">
                            <img id="lightboxImage" src="" alt="Önizleme" style="max-width: 100%; max-height: 60vh; object-fit: contain; border-radius: var(--radius-md);">
                        </div>
                        <div style="display: flex; justify-content: space-between; align-items: center; padding: 14px 20px; border-top: 1px solid var(--border-card); background: rgba(18, 24, 38, 0.8);">
                            <div id="lightboxInfo" style="font-size: 12px; color: var(--text-muted);">-</div>
                            <div style="display: flex; gap: 8px;">
                                <button class="btn-action" id="lightboxCopyBtn" onclick="copyLightboxImage()" style="font-size: 12px; padding: 6px 12px;">📋 Panoya Kopyala</button>
                                <button class="btn-action btn-primary" id="lightboxDownloadBtn" onclick="downloadLightboxPhoto()" style="font-size: 12px; padding: 6px 14px;">⬇️ Mac'e İndir</button>
                            </div>
                        </div>
                    </div>
                </div>
            </div>
        </main>
    </div>

    <script>
        let allSmsMessages = [];
        let activeThreadAddress = "";
        let currentStatus = null;

        function switchTab(tabId, el) {
            document.querySelectorAll('.tab-pane').forEach(p => p.classList.remove('active'));
            document.querySelectorAll('.nav-item').forEach(i => i.classList.remove('active'));
            const targetPane = document.getElementById(tabId.startsWith('tab-') ? tabId : 'tab-' + tabId);
            if (targetPane) targetPane.classList.add('active');
            if (el) {
                el.classList.add('active');
                const pageTitle = document.getElementById('pageTitle');
                if (pageTitle) pageTitle.textContent = el.textContent.trim().replace(/^[^a-zA-ZçÇğĞıİöÖşŞüÜ]+/, '');
            } else {
                const navItem = document.querySelector('.nav-item[onclick*="' + tabId + '"]');
                if (navItem) {
                    navItem.classList.add('active');
                    const pageTitle = document.getElementById('pageTitle');
                    if (pageTitle) pageTitle.textContent = navItem.textContent.trim().replace(/^[^a-zA-ZçÇğĞıİöÖşŞüÜ]+/, '');
                }
            }
            if (tabId === 'tab-files' || tabId === 'files') {
                loadTransferredFiles();
            } else if (tabId === 'tab-contacts' || tabId === 'contacts') {
                loadContactsList();
            } else if (tabId === 'tab-photos' || tabId === 'photos') {
                loadPhotosList();
            }
        }

        function dialDigit(d) {
            document.getElementById('dialInput').value += d;
        }

        function makeCall() {
            const num = document.getElementById('dialInput').value.trim();
            if (!num) return;
            triggerCallAction('DIAL', num);
        }

        function triggerCallAction(action, number = null, value = null) {
            let u = '/call/action?action=' + encodeURIComponent(action);
            if (number) u += '&number=' + encodeURIComponent(number);
            if (value !== null) u += '&value=' + (value ? '1' : '0');
            fetch(u).then(() => fetchStatus());
        }

        function sendClipboard() {
            const text = document.getElementById('clipInputText').value;
            if (!text) return;
            const params = new URLSearchParams();
            params.append('text', text);
            fetch('/clipboard/send', { method: 'POST', body: params }).then(() => {
                alert('Metin telefon panosuna aktarıldı!');
            });
        }

        function syncSms() {
            fetch('/sms/sync').then(() => {
                setTimeout(loadSmsList, 800);
            });
        }

        function loadSmsList() {
            fetch('/sms/list').then(r => r.json()).then(data => {
                allSmsMessages = data.messages || [];
                renderSmsThreads();
            });
        }

        function renderSmsThreads() {
            const container = document.getElementById('smsThreadsList');
            if (!allSmsMessages.length) {
                container.innerHTML = '<div style="padding: 20px; color: var(--text-muted); font-size: 13px;">Kayıtlı SMS bulunamadı.</div>';
                return;
            }

            const threads = {};
            for (const m of allSmsMessages) {
                if (!threads[m.address]) {
                    threads[m.address] = [];
                }
                threads[m.address].push(m);
            }

            let html = '';
            for (const addr in threads) {
                const list = threads[addr];
                const last = list[0];
                const contact = last.contact_name || addr;
                const activeCls = addr === activeThreadAddress ? 'active' : '';
                html += '<div class="sms-thread-item ' + activeCls + '" onclick="selectThread(\'' + addr + '\')">' +
                    '<div style="font-weight: 700; font-size: 14px;">' + contact + '</div>' +
                    '<div style="font-size: 12px; color: var(--text-muted); white-space: nowrap; overflow: hidden; text-overflow: ellipsis; margin-top: 4px;">' +
                        last.body +
                    '</div>' +
                '</div>';
            }
            container.innerHTML = html;
            if (activeThreadAddress) {
                renderConversation();
            }
        }

        function selectThread(addr) {
            activeThreadAddress = addr;
            renderSmsThreads();
            renderConversation();
        }

        function renderConversation() {
            const list = allSmsMessages.filter(m => m.address === activeThreadAddress);
            list.sort((a, b) => a.timestamp - b.timestamp);
            const container = document.getElementById('smsChatHistory');
            if (!list.length) {
                container.innerHTML = '<div style="margin: auto; color: var(--text-muted);">Mesaj yok.</div>';
                return;
            }

            const contact = list[0].contact_name || activeThreadAddress;
            document.getElementById('smsCurrentContact').textContent = contact;
            document.getElementById('smsCurrentNumber').textContent = activeThreadAddress;

            let html = '';
            for (const m of list) {
                const cls = m.is_incoming ? 'incoming' : 'outgoing';
                html += '<div class="chat-bubble ' + cls + '">' + m.body + '</div>';
            }
            container.innerHTML = html;
            container.scrollTop = container.scrollHeight;
        }

        function sendSmsMessage() {
            const input = document.getElementById('smsInputText');
            const text = input.value.trim();
            if (!text || !activeThreadAddress) return;
            const params = new URLSearchParams();
            params.append('recipient', activeThreadAddress);
            params.append('body', text);

            fetch('/sms/send', { method: 'POST', body: params }).then(r => r.json()).then(() => {
                input.value = '';
                allSmsMessages.unshift({
                    id: 'local_' + Date.now(),
                    address: activeThreadAddress,
                    body: text,
                    timestamp: Date.now(),
                    is_incoming: false
                });
                renderConversation();
                renderSmsThreads();
            });
        }

        function resetPairing() {
            if (!confirm('Eşleşmeyi sıfırlamak istediğinize emin misiniz?')) return;
            fetch('/pair/reset').then(r => r.json()).then(data => {
                document.getElementById('pinDisplay').textContent = data.pairing_pin;
                fetchStatus();
            });
        }

        let isSeekingMedia = false;
        let currentMediaDuration = 0;
        let isSeekingPhoneMedia = false;
        let currentPhoneMediaDuration = 0;

        function formatMs(ms) {
            if (!ms || ms <= 0) return '00:00';
            const totalSec = Math.floor(ms / 1000);
            const m = Math.floor(totalSec / 60);
            const s = totalSec % 60;
            return m.toString().padStart(2, '0') + ':' + s.toString().padStart(2, '0');
        }

        function updateSeekTimePreview(val) {
            if (currentMediaDuration > 0) {
                const cur = (val / 100) * currentMediaDuration;
                const str = formatMs(cur);
                const macCur = document.getElementById('macMediaCurTime');
                if (macCur) macCur.textContent = str;
                const fullCur = document.getElementById('fullMediaCurTime');
                if (fullCur) fullCur.textContent = str;
            }
        }

        function seekMacMedia(val) {
            fetch('/mac/command?action=SEEK_PERCENT&percent=' + encodeURIComponent(val)).catch(() => {});
            setTimeout(function() { isSeekingMedia = false; }, 800);
        }

        function seekRelative(deltaSec) {
            const action = deltaSec > 0 ? 'SEEK_FORWARD' : 'SEEK_BACKWARD';
            fetch('/mac/command?action=' + action).catch(() => {});
        }

        function sendMediaCmd(action) {
            fetch('/mac/command?action=' + encodeURIComponent(action)).catch(() => {});
        }

        function updatePhoneSeekTimePreview(val) {
            if (currentPhoneMediaDuration > 0) {
                const cur = (val / 100) * currentPhoneMediaDuration;
                const str = formatMs(cur);
                const phoneCur = document.getElementById('phoneMediaCurTime');
                if (phoneCur) phoneCur.textContent = str;
                const fullPhoneCur = document.getElementById('fullPhoneMediaCurTime');
                if (fullPhoneCur) fullPhoneCur.textContent = str;
            }
        }

        function seekPhoneMedia(val) {
            fetch('/phone/command?action=SEEK_PERCENT&percent=' + encodeURIComponent(val)).catch(() => {});
            setTimeout(function() { isSeekingPhoneMedia = false; }, 800);
        }

        function seekPhoneRelative(deltaSec) {
            const action = deltaSec > 0 ? 'SEEK_FORWARD' : 'SEEK_BACKWARD';
            fetch('/phone/command?action=' + action).catch(() => {});
        }

        function sendPhoneCmd(action) {
            fetch('/phone/command?action=' + encodeURIComponent(action)).catch(() => {});
        }

        function fetchStatus() {
            fetch('/status').then(r => r.json()).then(s => {
                currentStatus = s;
                // Connection
                const connected = s.connected;
                const dot = document.getElementById('sidebarDot');
                const connText = document.getElementById('sidebarConnText');
                const devName = document.getElementById('sidebarDeviceName');

                if (connected) {
                    dot.classList.add('connected');
                    connText.textContent = 'Bağlandı 🟢';
                    devName.textContent = s.device_name || 'Android Cihaz';
                } else {
                    dot.classList.remove('connected');
                    connText.textContent = 'Bağlantı Aranıyor 🟡';
                    devName.textContent = 'Cihaz Yok';
                }

                // Security & PIN
                const pin = s.pairing_pin || '------';
                document.getElementById('pinDisplay').textContent = pin;
                const badge = document.getElementById('pairingBadge');
                const pText = document.getElementById('pairingStatusText');

                if (s.is_paired) {
                    badge.style.background = 'rgba(16, 185, 129, 0.15)';
                    badge.style.color = '#10B981';
                    badge.textContent = '🔒 Güvenli (Eşleşti)';
                    pText.textContent = 'Cihaz ' + (s.paired_device_name || 'Telefon') + ' ile güvenli bir şekilde eşleşti.';
                } else {
                    badge.style.background = 'rgba(245, 158, 11, 0.15)';
                    badge.style.color = '#F59E0B';
                    badge.textContent = '⚠️ Eşleşme Bekleniyor';
                    pText.textContent = 'Eşleşme bekleniyor. Telefondan ' + pin + ' kodunu onaylayın.';
                }

                // Metrics
                if (s.battery_level !== undefined) {
                    document.getElementById('cardBattery').textContent = s.battery_level + '%';
                    document.getElementById('cardCharging').textContent = s.is_charging ? 'Şarj Oluyor ⚡' : 'Pilde Çalışıyor';
                }
                if (s.device_name) {
                    document.getElementById('cardDeviceName').textContent = s.device_name;
                    document.getElementById('cardDeviceModel').textContent = s.model || '-';
                }
                if (s.local_ip) {
                    document.getElementById('cardIp').textContent = s.local_ip;
                }
                if (s.sms_count !== undefined) {
                    document.getElementById('cardSmsTotal').textContent = s.sms_count;
                    document.getElementById('smsCountBadge').textContent = s.sms_count;
                }

                // Clipboard
                if (s.clipboard_image) {
                    const wrap = document.getElementById('clipImageWrapper');
                    if (wrap) {
                        wrap.style.display = 'block';
                        const img = document.getElementById('clipImagePreview');
                        img.src = 'data:image/png;base64,' + s.clipboard_image;
                        const dl = document.getElementById('clipImageDownloadBtn');
                        dl.href = 'data:image/png;base64,' + s.clipboard_image;
                        const sizeEl = document.getElementById('clipImageSize');
                        if (sizeEl) sizeEl.innerText = '(' + Math.round((s.clipboard_image.length * 3 / 4) / 1024) + ' KB)';
                    }
                } else {
                    const wrap = document.getElementById('clipImageWrapper');
                    if (wrap) wrap.style.display = 'none';
                }

                // Call state & Banner
                const banner = document.getElementById('globalCallBanner');
                if (s.call_state && s.call_state.state === 'RINGING') {
                    banner.classList.add('active');
                    document.getElementById('callBannerType').textContent = 'GELEN ARAMA';
                    document.getElementById('callBannerCaller').textContent = s.call_state.caller_name || 'Bilinmeyen Numara';
                    document.getElementById('callBannerNumber').textContent = s.call_state.phone_number || '';
                    document.getElementById('btnBannerAnswer').style.display = 'inline-flex';
                    document.getElementById('btnBannerReject').style.display = 'inline-flex';
                } else if (s.call_state && s.call_state.state === 'OFFHOOK') {
                    banner.classList.add('active');
                    document.getElementById('callBannerType').textContent = 'DEVAM EDEN GÖRÜŞME';
                    document.getElementById('callBannerCaller').textContent = s.call_state.caller_name || 'Aktif Görüşme';
                    document.getElementById('callBannerNumber').textContent = 'Süre: ' + (s.call_duration_sec || 0) + 's';
                    document.getElementById('btnBannerAnswer').style.display = 'none';
                    document.getElementById('btnBannerReject').style.display = 'inline-flex';
                    document.getElementById('btnBannerReject').textContent = 'Sonlandır';
                } else {
                    banner.classList.remove('active');
                }

                // Notifications
                const notifsContainer = document.getElementById('notifsContainer');
                if (notifsContainer) {
                    if (s.notifications && s.notifications.length > 0) {
                        let nHtml = '';
                        for (const n of s.notifications.slice(0, 8)) {
                            const timeStr = n.timestamp ? new Date(n.timestamp).toLocaleTimeString() : '';
                            var replyBox = '';
                            if (n.can_reply && n.key) {
                                replyBox = '<div style="margin-top:10px; display:flex; gap:8px;">' +
                                    '<input type="text" id="replyInput_' + n.id + '" placeholder="Yanıt yazın..." style="flex:1; background:var(--bg-input); border:1px solid var(--border-card); border-radius:var(--radius-sm); padding:6px 12px; color:var(--text-primary); font-size:12px; outline:none;" onkeydown="if(event.key===\'Enter\') sendNotificationReply(\'' + n.key + '\', \'' + n.id + '\')">' +
                                    '<button class="btn-action btn-primary" style="font-size:12px; padding:6px 14px;" onclick="sendNotificationReply(\'' + n.key + '\', \'' + n.id + '\')">Yanıtla</button>' +
                                '</div>';
                            }
                            var actionsBox = '';
                            if (n.actions && n.actions.length > 0 && n.key) {
                                actionsBox = '<div style="margin-top:10px; display:flex; flex-wrap:wrap; gap:8px;">' +
                                    n.actions.filter(function(a) { return !a.is_reply; }).map(function(a) {
                                        return '<button class="btn-action" style="font-size:11px; padding:4px 10px; border-radius:6px; cursor:pointer;" onclick="triggerNotificationAction(\'' + n.key + '\', ' + a.index + ', this)">⚡ ' +
                                            a.title +
                                        '</button>';
                                    }).join('') +
                                '</div>';
                            }
                            nHtml += '<div style="background:rgba(255,255,255,0.03); border:1px solid rgba(255,255,255,0.06); border-radius:8px; padding:10px;">' +
                                '<div style="display:flex; justify-content:space-between; margin-bottom:2px;">' +
                                    '<strong style="color:var(--accent-blue); font-size:12px;">' + (n.app_name || 'Uygulama') + '</strong>' +
                                    '<span style="font-size:10px; color:var(--text-muted);">' + timeStr + '</span>' +
                                '</div>' +
                                '<div style="font-weight:700; font-size:13px;">' + (n.title || '') + '</div>' +
                                '<div style="font-size:12px; color:var(--text-secondary); margin-top:2px;">' + (n.text || '') + '</div>' +
                                actionsBox +
                                replyBox +
                            '</div>';
                        }
                        notifsContainer.innerHTML = nHtml;
                    } else {
                        notifsContainer.innerHTML = '<div style="color: var(--text-muted); font-size: 13px;">Henüz bildirim gelmedi.</div>';
                    }
                }

                if (s.files_count !== undefined) {
                    const fcBadge = document.getElementById('filesCountBadge');
                    if (fcBadge) {
                        fcBadge.textContent = s.files_count;
                        fcBadge.style.display = s.files_count > 0 ? 'inline-flex' : 'none';
                    }
                }

                // Mac Media
                const macMedia = s.mac_media;
                if (macMedia && (macMedia.title || macMedia.artist)) {
                    const macTitle = macMedia.title || 'Bilinmeyen Parça';
                    const macArtist = macMedia.artist || 'Bilinmeyen Sanatçı';
                    const macPlayingStr = macMedia.is_playing ? 'OYNATILIYOR 🟢' : 'DURAKLATILDI ⏸';

                    const macTrackTitleEl = document.getElementById('macTrackTitle');
                    if (macTrackTitleEl) macTrackTitleEl.textContent = macTitle;
                    const fullMacTitleEl = document.getElementById('fullMacTitle');
                    if (fullMacTitleEl) fullMacTitleEl.textContent = macTitle;

                    const macTrackArtistEl = document.getElementById('macTrackArtist');
                    if (macTrackArtistEl) macTrackArtistEl.textContent = macArtist;
                    const fullMacArtistEl = document.getElementById('fullMacArtist');
                    if (fullMacArtistEl) fullMacArtistEl.textContent = macArtist;

                    const macTagEl = document.getElementById('macMediaTag');
                    if (macTagEl) macTagEl.textContent = macPlayingStr;
                    const fullMacTagEl = document.getElementById('fullMacTag');
                    if (fullMacTagEl) fullMacTagEl.textContent = macPlayingStr;

                    currentMediaDuration = macMedia.duration_ms || 0;
                    const curTimeStr = formatMs(macMedia.position_ms || 0);
                    const totalTimeStr = formatMs(macMedia.duration_ms || 0);

                    const macCur = document.getElementById('macMediaCurTime');
                    if (macCur) macCur.textContent = curTimeStr;
                    const fullCur = document.getElementById('fullMediaCurTime');
                    if (fullCur) fullCur.textContent = curTimeStr;

                    const macTotal = document.getElementById('macMediaTotalTime');
                    if (macTotal) macTotal.textContent = totalTimeStr;
                    const fullTotal = document.getElementById('fullMediaTotalTime');
                    if (fullTotal) fullTotal.textContent = totalTimeStr;

                    if (!isSeekingMedia && macMedia.duration_ms > 0) {
                        const pct = Math.min(100, Math.max(0, (macMedia.position_ms / macMedia.duration_ms) * 100));
                        const seekEl = document.getElementById('macMediaSeek');
                        if (seekEl) seekEl.value = pct;
                        const fullSeekEl = document.getElementById('fullMediaSeek');
                        if (fullSeekEl) fullSeekEl.value = pct;
                    }
                } else {
                    const macTrackTitleEl = document.getElementById('macTrackTitle');
                    if (macTrackTitleEl) macTrackTitleEl.textContent = 'Mac Medyası Yok';
                    const fullMacTitleEl = document.getElementById('fullMacTitle');
                    if (fullMacTitleEl) fullMacTitleEl.textContent = 'Mac Medyası Yok';

                    const macTrackArtistEl = document.getElementById('macTrackArtist');
                    if (macTrackArtistEl) macTrackArtistEl.textContent = 'Spotify / Apple Music açın';
                    const fullMacArtistEl = document.getElementById('fullMacArtist');
                    if (fullMacArtistEl) fullMacArtistEl.textContent = 'Spotify / Apple Music açın';

                    const macTagEl = document.getElementById('macMediaTag');
                    if (macTagEl) macTagEl.textContent = 'BEKLEMEDE ⚪';
                    const fullMacTagEl = document.getElementById('fullMacTag');
                    if (fullMacTagEl) fullMacTagEl.textContent = 'BEKLEMEDE ⚪';
                }

                // Phone Media
                const phoneMedia = s.phone_media;
                if (phoneMedia && (phoneMedia.title || phoneMedia.artist)) {
                    const phoneTitle = phoneMedia.title || 'Bilinmeyen Şarkı';
                    const phoneArtist = phoneMedia.artist || phoneMedia.album || 'Bilinmeyen Sanatçı';
                    const phonePlayingStr = phoneMedia.is_playing ? 'OYNATILIYOR 🟢' : 'DURAKLATILDI ⏸';

                    const phoneTrackTitleEl = document.getElementById('phoneTrackTitle');
                    if (phoneTrackTitleEl) phoneTrackTitleEl.textContent = phoneTitle;
                    const fullPhoneTitleEl = document.getElementById('fullPhoneTitle');
                    if (fullPhoneTitleEl) fullPhoneTitleEl.textContent = phoneTitle;

                    const phoneTrackArtistEl = document.getElementById('phoneTrackArtist');
                    if (phoneTrackArtistEl) phoneTrackArtistEl.textContent = phoneArtist;
                    const fullPhoneArtistEl = document.getElementById('fullPhoneArtist');
                    if (fullPhoneArtistEl) fullPhoneArtistEl.textContent = phoneArtist;

                    const phoneTagEl = document.getElementById('phoneMediaTag');
                    if (phoneTagEl) phoneTagEl.textContent = phonePlayingStr;
                    const fullPhoneTagEl = document.getElementById('fullPhoneTag');
                    if (fullPhoneTagEl) fullPhoneTagEl.textContent = phonePlayingStr;

                    currentPhoneMediaDuration = phoneMedia.duration_ms || 0;
                    const phoneCurTimeStr = formatMs(phoneMedia.position_ms || 0);
                    const phoneTotalTimeStr = formatMs(phoneMedia.duration_ms || 0);

                    const phoneCur = document.getElementById('phoneMediaCurTime');
                    if (phoneCur) phoneCur.textContent = phoneCurTimeStr;
                    const fullPhoneCur = document.getElementById('fullPhoneMediaCurTime');
                    if (fullPhoneCur) fullPhoneCur.textContent = phoneCurTimeStr;

                    const phoneTotal = document.getElementById('phoneMediaTotalTime');
                    if (phoneTotal) phoneTotal.textContent = phoneTotalTimeStr;
                    const fullPhoneTotal = document.getElementById('fullPhoneMediaTotalTime');
                    if (fullPhoneTotal) fullPhoneTotal.textContent = phoneTotalTimeStr;

                    if (!isSeekingPhoneMedia && phoneMedia.duration_ms > 0) {
                        const pct = Math.min(100, Math.max(0, (phoneMedia.position_ms / phoneMedia.duration_ms) * 100));
                        const seekEl = document.getElementById('phoneMediaSeek');
                        if (seekEl) seekEl.value = pct;
                        const fullPhoneSeekEl = document.getElementById('fullPhoneMediaSeek');
                        if (fullPhoneSeekEl) fullPhoneSeekEl.value = pct;
                    }
                } else {
                    const phoneTrackTitleEl = document.getElementById('phoneTrackTitle');
                    if (phoneTrackTitleEl) phoneTrackTitleEl.textContent = 'Telefon Medyası Yok';
                    const fullPhoneTitleEl = document.getElementById('fullPhoneTitle');
                    if (fullPhoneTitleEl) fullPhoneTitleEl.textContent = 'Telefon Medyası Yok';

                    const phoneTrackArtistEl = document.getElementById('phoneTrackArtist');
                    if (phoneTrackArtistEl) phoneTrackArtistEl.textContent = 'Telefonda müzik açın';
                    const fullPhoneArtistEl = document.getElementById('fullPhoneArtist');
                    if (fullPhoneArtistEl) fullPhoneArtistEl.textContent = 'Telefonda müzik açın';

                    const phoneTagEl = document.getElementById('phoneMediaTag');
                    if (phoneTagEl) phoneTagEl.textContent = 'BEKLEMEDE ⚪';
                    const fullPhoneTagEl = document.getElementById('fullPhoneTag');
                    if (fullPhoneTagEl) fullPhoneTagEl.textContent = 'BEKLEMEDE ⚪';
                }
            }).catch(() => {});
        }

        async function sendNotificationReply(key, id) {
            const input = document.getElementById('replyInput_' + id);
            const text = input ? input.value.trim() : '';
            if (!text) return;
            try {
                await fetch('/notification/reply', {
                    method: 'POST',
                    headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
                    body: 'key=' + encodeURIComponent(key) + '&text=' + encodeURIComponent(text)
                });
                if (input) input.value = '';
                alert('Yanıt telefona iletildi ✅');
            } catch (e) {
                console.error(e);
                alert('Hata: ' + e.message);
            }
        }

        async function triggerNotificationAction(key, index, btn) {
            try {
                if (btn) {
                    btn.disabled = true;
                    btn.style.opacity = '0.6';
                }
                const res = await fetch('/notification/action?key=' + encodeURIComponent(key) + '&index=' + index, { method: 'POST' });
                if (res.ok) {
                    if (btn) {
                        btn.style.background = 'rgba(16, 185, 129, 0.2)';
                        btn.style.color = '#10B981';
                        btn.style.borderColor = '#10B981';
                        btn.innerText = '✓ ' + btn.innerText.replace('⚡ ', '');
                    }
                }
            } catch (e) {
                console.error(e);
            }
        }

        async function remoteAction(action) {
            const actionNames = {
                'LOCK': "Mac'i Kilitlemek",
                'SLEEP': "Mac'i Uyku Moduna Almak",
                'SHUTDOWN': "Mac'i Kapatmak",
                'RESTART': "Mac'i Yeniden Başlatmak"
            };
            const label = actionNames[action] || action;
            if (!confirm(label + ' istediğinizden emin misiniz?')) return;
            try {
                await fetch('/remote/action', {
                    method: 'POST',
                    headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
                    body: 'action=' + encodeURIComponent(action)
                });
            } catch (e) {
                console.error(e);
            }
        }

        async function openDownloadsFolder() {
            try {
                await fetch('/file/open_folder');
            } catch (e) {
                console.error(e);
            }
        }

        async function loadTransferredFiles() {
            try {
                const res = await fetch('/file/list');
                const data = await res.json();
                const files = data.files || [];
                const tbody = document.getElementById('filesTableBody');
                const badge = document.getElementById('filesCountBadge');
                if (badge) {
                    badge.textContent = files.length;
                    badge.style.display = files.length > 0 ? 'inline-flex' : 'none';
                }
                if (!tbody) return;
                if (!files.length) {
                    tbody.innerHTML = '<tr><td colspan="5" style="padding:24px; text-align:center; color:var(--text-muted);">Henüz aktarılmış dosya yok</td></tr>';
                    return;
                }
                tbody.innerHTML = files.map(function(f) {
                    const isInc = f.direction === 'incoming';
                    const dirIcon = isInc ? '📥 Gelen' : '📤 Giden';
                    const dirColor = isInc ? 'var(--accent-green)' : 'var(--accent-blue)';
                    const sizeStr = (f.file_size / (1024 * 1024)).toFixed(2) + ' MB';
                    const dlBtn = isInc 
                        ? '<button class="btn-action" style="font-size:11px; padding:4px 10px;" onclick="openDownloadsFolder()">Finder\'da Göster</button>'
                        : '<a href="/file/download/' + f.id + '/' + encodeURIComponent(f.file_name) + '" class="btn-action" style="font-size:11px; padding:4px 10px; text-decoration:none;">İndir</a>';
                    return '<tr style="border-bottom:1px solid rgba(255,255,255,0.04);">' +
                        '<td style="padding:12px 14px; color:' + dirColor + '; font-weight:700;">' + dirIcon + '</td>' +
                        '<td style="padding:12px 14px; font-weight:600;">' + escapeHtml(f.file_name) + '</td>' +
                        '<td style="padding:12px 14px; color:var(--text-muted);">' + sizeStr + '</td>' +
                        '<td style="padding:12px 14px; color:var(--text-muted);">' + formatTime(f.timestamp) + '</td>' +
                        '<td style="padding:12px 14px; text-align:right;">' + dlBtn + '</td>' +
                    '</tr>';
                }).join('');
            } catch (e) {
                console.error(e);
            }
        }

        async function uploadFilesToPhone(files) {
            if (!files || files.length === 0) return;
            const box = document.getElementById('uploadProgressBox');
            const nameEl = document.getElementById('uploadFileName');
            const barEl = document.getElementById('uploadProgressBar');
            const pctEl = document.getElementById('uploadPercent');

            if (box) box.style.display = 'block';
            for (let i = 0; i < files.length; i++) {
                const file = files[i];
                if (nameEl) nameEl.innerText = file.name + ' (' + (i + 1) + '/' + files.length + ') telefona gönderiliyor...';
                if (barEl) barEl.style.width = '30%';
                if (pctEl) pctEl.innerText = '30%';

                const formData = new FormData();
                formData.append('file', file);

                try {
                    if (barEl) barEl.style.width = '70%';
                    if (pctEl) pctEl.innerText = '70%';
                    const res = await fetch('/file/send_to_phone', {
                        method: 'POST',
                        body: formData
                    });
                    if (barEl) barEl.style.width = '100%';
                    if (pctEl) pctEl.innerText = '100%';
                    const json = await res.json();
                } catch (e) {
                    console.error(e);
                    alert('Gönderim hatası: ' + e.message);
                }
            }
            setTimeout(function() {
                if (box) box.style.display = 'none';
                if (barEl) barEl.style.width = '0%';
                loadTransferredFiles();
            }, 1200);
        }

        function handleFileSelect(e) {
            const files = e.target.files;
            uploadFilesToPhone(files);
        }

        function setupDropZone() {
            const dropZone = document.getElementById('dropZone');
            const fileInput = document.getElementById('filePickerInput');
            if (!dropZone || !fileInput) return;

            dropZone.addEventListener('click', function() {
                fileInput.click();
            });

            ['dragenter', 'dragover'].forEach(function(evt) {
                dropZone.addEventListener(evt, function(e) {
                    e.preventDefault();
                    e.stopPropagation();
                    dropZone.style.borderColor = 'var(--accent-blue)';
                    dropZone.style.background = 'rgba(56, 189, 248, 0.12)';
                }, false);
            });

            ['dragleave', 'drop'].forEach(function(evt) {
                dropZone.addEventListener(evt, function(e) {
                    e.preventDefault();
                    e.stopPropagation();
                    dropZone.style.borderColor = 'rgba(56, 189, 248, 0.4)';
                    dropZone.style.background = 'rgba(56, 189, 248, 0.04)';
                }, false);
            });

            dropZone.addEventListener('drop', function(e) {
                const dt = e.dataTransfer;
                if (dt && dt.files) {
                    uploadFilesToPhone(dt.files);
                }
            }, false);
        }

        let allContacts = [];
        let sharedTabsHistory = [];

        async function loadContactsList() {
            try {
                const res = await fetch('/contacts');
                const data = await res.json();
                allContacts = data.contacts || [];
                renderContacts(allContacts);
                const badge = document.getElementById('contactsCountBadge');
                if (badge) {
                    badge.innerText = allContacts.length;
                    badge.style.display = allContacts.length > 0 ? 'inline-block' : 'none';
                }
                const hBadge = document.getElementById('contactsHeaderBadge');
                if (hBadge) hBadge.innerText = allContacts.length + ' Kişi';
            } catch (e) {
                console.error('Rehber yükleme hatası:', e);
            }
        }

        async function refreshContacts() {
            try {
                await fetch('/contacts/refresh', { method: 'POST' });
                alert('📱 Telefona rehber senkronizasyon isteği gönderildi. Birkaç saniye içinde güncellenecektir.');
                setTimeout(loadContactsList, 1500);
            } catch (e) {
                alert('Hata: ' + e.message);
            }
        }

        function filterContacts() {
            const query = (document.getElementById('contactsSearchInput').value || '').toLowerCase().trim();
            if (!query) {
                renderContacts(allContacts);
                return;
            }
            const filtered = allContacts.filter(c => 
                (c.name && c.name.toLowerCase().includes(query)) ||
                (c.number && c.number.replace(/\s+/g, '').includes(query.replace(/\s+/g, '')))
            );
            renderContacts(filtered);
        }

        function renderContacts(list) {
            const grid = document.getElementById('contactsGrid');
            if (!grid) return;
            if (!list || list.length === 0) {
                grid.innerHTML = '<div style="grid-column: 1 / -1; padding: 48px; text-align: center; color: var(--text-muted);">Rehberde görüntülenecek kişi bulunamadı veya arama sonucu boş.</div>';
                return;
            }

            const gradients = [
                'linear-gradient(135deg, #4F46E5, #06B6D4)',
                'linear-gradient(135deg, #10B981, #059669)',
                'linear-gradient(135deg, #F59E0B, #D97706)',
                'linear-gradient(135deg, #EC4899, #8B5CF6)',
                'linear-gradient(135deg, #3B82F6, #1D4ED8)'
            ];

            grid.innerHTML = list.map(function(c, idx) {
                var initials = (c.name || 'İ').trim().split(' ').map(function(w) { return w[0]; }).join('').substring(0, 2).toUpperCase();
                var grad = gradients[idx % gradients.length];
                var safeName = (c.name || 'İsimsiz').replace(/'/g, "\\'");
                var safeNum = (c.number || '').replace(/'/g, "\\'");

                return '<div style="background: rgba(18, 24, 38, 0.7); border: 1px solid var(--border-card); border-radius: var(--radius-md); padding: 16px; display: flex; flex-direction: column; gap: 12px; transition: all 0.2s;">' +
                    '<div style="display: flex; align-items: center; gap: 12px;">' +
                        '<div style="width: 44px; height: 44px; border-radius: 50%; background: ' + grad + '; display: flex; align-items: center; justify-content: center; font-weight: 800; font-size: 15px; color: white; flex-shrink: 0;">' +
                            initials +
                        '</div>' +
                        '<div style="overflow: hidden; flex: 1;">' +
                            '<div style="font-weight: 700; font-size: 14px; color: var(--text-primary); white-space: nowrap; overflow: hidden; text-overflow: ellipsis;">' + escapeHtml(c.name) + '</div>' +
                            '<div style="font-size: 12px; color: var(--text-secondary); margin-top: 2px;">' + escapeHtml(c.number) + '</div>' +
                        '</div>' +
                    '</div>' +
                    '<div style="display: flex; gap: 6px; margin-top: auto; padding-top: 10px; border-top: 1px solid rgba(255,255,255,0.06);">' +
                        '<button class="btn-action" style="flex: 1; padding: 6px 0; font-size: 11px; background: rgba(16, 185, 129, 0.2); color: #34d399; border: 1px solid rgba(16, 185, 129, 0.4);" onclick="triggerCallAction(\'DIAL\', \'' + safeNum + '\')" title="Telefonla Ara">📞 Ara</button>' +
                        '<button class="btn-action btn-primary" style="flex: 1; padding: 6px 0; font-size: 11px;" onclick="startSmsWith(\'' + safeNum + '\', \'' + safeName + '\')" title="SMS Gönder">💬 SMS</button>' +
                        '<button class="btn-action" style="padding: 6px 10px; font-size: 11px;" onclick="copyTextToClip(\'' + safeNum + '\')" title="Numarayı Kopyala">📋</button>' +
                    '</div>' +
                '</div>';
            }).join('');
        }

        function copyTextToClip(text) {
            navigator.clipboard.writeText(text).then(() => {
                alert('Panoya kopyalandı: ' + text);
            }).catch(() => {
                const el = document.createElement('textarea');
                el.value = text;
                document.body.appendChild(el);
                el.select();
                document.execCommand('copy');
                document.body.removeChild(el);
                alert('Panoya kopyalandı: ' + text);
            });
        }

        function startSmsWith(number, name) {
            switchTab('tab-sms');
            selectThread(number);
            const contactEl = document.getElementById('smsCurrentContact');
            const numEl = document.getElementById('smsCurrentNumber');
            if (contactEl) contactEl.textContent = name || number;
            if (numEl) numEl.textContent = number;
            setTimeout(() => {
                const input = document.getElementById('smsInputText');
                if (input) input.focus();
            }, 100);
        }

        async function sendTabToPhone() {
            const input = document.getElementById('sendTabUrlInput');
            let url = (input.value || '').trim();
            if (!url) {
                alert('Lütfen geçerli bir web adresi girin!');
                return;
            }
            if (!url.startsWith('http://') && !url.startsWith('https://')) {
                url = 'https://' + url;
            }
            try {
                const res = await fetch('/url/send_to_phone?url=' + encodeURIComponent(url), { method: 'POST' });
                const data = await res.json();
                if (data.success) {
                    input.value = '';
                    addSharedTabHistory(url, 'outgoing');
                    alert('🌐 Bağlantı telefona iletildi! Telefonda tarayıcı anında açılacaktır.');
                }
            } catch (e) {
                alert('Hata: ' + e.message);
            }
        }

        async function openLocalTab() {
            const input = document.getElementById('sendTabUrlInput');
            let url = (input.value || '').trim();
            if (!url) return;
            try {
                await fetch('/url/open_local?url=' + encodeURIComponent(url), { method: 'POST' });
            } catch (e) {}
        }

        function fillAndSendUrl(url) {
            const input = document.getElementById('sendTabUrlInput');
            if (input) input.value = url;
            sendTabToPhone();
        }

        function addSharedTabHistory(url, dir) {
            sharedTabsHistory.unshift({ url, dir, time: new Date() });
            if (sharedTabsHistory.length > 20) sharedTabsHistory.pop();
            renderSharedTabs();
        }

        function renderSharedTabs() {
            const container = document.getElementById('sharedTabsList');
            if (!container) return;
            if (sharedTabsHistory.length === 0) return;

            container.innerHTML = sharedTabsHistory.map(function(item) {
                var isOut = item.dir === 'outgoing';
                var icon = isOut ? '📱 ➡️' : '🍏 ⬅️';
                var timeStr = item.time.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit', second: '2-digit' });
                var safeUrl = escapeHtml(item.url);
                var rawUrl = item.url.replace(/'/g, "\\'");
                return '<div style="background: rgba(18, 24, 38, 0.6); border: 1px solid var(--border-card); border-radius: var(--radius-md); padding: 12px 16px; display: flex; align-items: center; justify-content: space-between; gap: 12px;">' +
                    '<div style="display: flex; align-items: center; gap: 10px; overflow: hidden;">' +
                        '<span style="font-size: 16px;">' + icon + '</span>' +
                        '<div style="overflow: hidden;">' +
                            '<a href="' + safeUrl + '" target="_blank" style="color: var(--accent-blue); text-decoration: none; font-size: 13px; font-weight: 600; display: block; overflow: hidden; text-overflow: ellipsis; white-space: nowrap;">' + safeUrl + '</a>' +
                            '<span style="font-size: 11px; color: var(--text-muted);">' + timeStr + '</span>' +
                        '</div>' +
                    '</div>' +
                    '<div style="display: flex; gap: 6px; flex-shrink: 0;">' +
                        '<button class="btn-action" style="font-size: 11px; padding: 4px 10px;" onclick="copyTextToClip(\'' + rawUrl + '\')">📋 Kopyala</button>' +
                        '<a class="btn-action btn-primary" style="font-size: 11px; padding: 4px 10px; text-decoration: none;" href="' + safeUrl + '" target="_blank">Aç 🔗</a>' +
                    '</div>' +
                '</div>';
            }).join('');
        }

        let allPhotos = [];
        let currentPhotoFilter = 'all';
        let activeLightboxPhoto = null;

        async function loadPhotosList() {
            try {
                const res = await fetch('/photos');
                const data = await res.json();
                allPhotos = data.photos || [];
                renderPhotos(getFilteredPhotos());
                const badge = document.getElementById('photosCountBadge');
                if (badge) {
                    badge.innerText = allPhotos.length;
                    badge.style.display = allPhotos.length > 0 ? 'inline-block' : 'none';
                }
                const hBadge = document.getElementById('photosHeaderBadge');
                if (hBadge) hBadge.innerText = allPhotos.length + ' Fotoğraf';
            } catch (e) {
                console.error('Fotoğraf listesi yükleme hatası:', e);
            }
        }

        async function refreshPhotos() {
            try {
                await fetch('/photos/refresh', { method: 'POST' });
                alert('📱 Telefona fotoğraf galerisi senkronizasyon isteği gönderildi. Birkaç saniye içinde güncellenecektir.');
                setTimeout(loadPhotosList, 1500);
            } catch (e) {
                alert('Hata: ' + e.message);
            }
        }

        function setPhotoFilter(filter) {
            currentPhotoFilter = filter;
            ['all', 'camera', 'screenshots'].forEach(function(f) {
                var btn = document.getElementById('photoFilter' + f.charAt(0).toUpperCase() + f.slice(1));
                if (btn) {
                    btn.className = (f === filter ? 'btn-action btn-primary' : 'btn-action');
                }
            });
            renderPhotos(getFilteredPhotos());
        }

        function getFilteredPhotos() {
            var input = document.getElementById('photosSearchInput');
            var query = (input ? input.value : '').toLowerCase().trim();
            return allPhotos.filter(function(p) {
                var name = (p.name || '').toLowerCase();
                var matchesQuery = !query || name.includes(query);
                if (!matchesQuery) return false;

                if (currentPhotoFilter === 'camera') {
                    return !name.includes('screenshot') && !name.includes('ekran');
                } else if (currentPhotoFilter === 'screenshots') {
                    return name.includes('screenshot') || name.includes('ekran');
                }
                return true;
            });
        }

        function filterPhotos() {
            renderPhotos(getFilteredPhotos());
        }

        function renderPhotos(list) {
            const grid = document.getElementById('photosGrid');
            if (!grid) return;
            if (!list || list.length === 0) {
                grid.innerHTML = '<div style="grid-column: 1 / -1; padding: 48px; text-align: center; color: var(--text-muted);">Fotoğraf bulunamadı veya arama sonucu boş.</div>';
                return;
            }

            grid.innerHTML = list.map(function(p) {
                var safeName = escapeHtml(p.name);
                var dateStr = p.date ? new Date(p.date).toLocaleDateString([], { month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit' }) : '';
                var sizeMB = (p.size / (1024 * 1024)).toFixed(1) + ' MB';
                var thumbSrc = p.thumbnail ? ('data:image/jpeg;base64,' + p.thumbnail) : '';

                var imgHtml = thumbSrc ? 
                    '<img src="' + thumbSrc + '" alt="' + safeName + '" style="width: 100%; height: 160px; object-fit: cover; display: block; transition: transform 0.3s;">' :
                    '<div style="width: 100%; height: 160px; background: rgba(255,255,255,0.04); display: flex; align-items: center; justify-content: center; font-size: 36px;">🖼</div>';

                return '<div style="background: rgba(18, 24, 38, 0.7); border: 1px solid var(--border-card); border-radius: var(--radius-md); overflow: hidden; display: flex; flex-direction: column; transition: all 0.2s;" class="photo-card">' +
                    '<div style="position: relative; overflow: hidden; cursor: pointer;" onclick="openPhotoLightbox(' + p.id + ')">' +
                        imgHtml +
                        '<div style="position: absolute; bottom: 0; left: 0; right: 0; background: linear-gradient(transparent, rgba(0,0,0,0.85)); padding: 6px 8px; font-size: 11px; color: white; display: flex; justify-content: space-between;">' +
                            '<span>' + dateStr + '</span>' +
                            '<span>' + sizeMB + '</span>' +
                        '</div>' +
                    '</div>' +
                    '<div style="padding: 10px 12px; display: flex; flex-direction: column; gap: 8px; flex: 1;">' +
                        '<div style="font-size: 12px; font-weight: 700; color: var(--text-primary); white-space: nowrap; overflow: hidden; text-overflow: ellipsis;" title="' + safeName + '">' + safeName + '</div>' +
                        '<div style="display: flex; gap: 6px; margin-top: auto;">' +
                            '<button class="btn-action" style="flex: 1; font-size: 11px; padding: 4px 0;" onclick="openPhotoLightbox(' + p.id + ')">🔍 Önizle</button>' +
                            '<button class="btn-action btn-primary" style="flex: 1; font-size: 11px; padding: 4px 0;" onclick="downloadPhotoToPc(' + p.id + ')">⬇️ İndir</button>' +
                        '</div>' +
                    '</div>' +
                '</div>';
            }).join('');
        }

        function openPhotoLightbox(photoId) {
            const photo = allPhotos.find(p => p.id === photoId);
            if (!photo) return;
            activeLightboxPhoto = photo;

            const modal = document.getElementById('photoLightboxModal');
            const title = document.getElementById('lightboxTitle');
            const sub = document.getElementById('lightboxSubtitle');
            const img = document.getElementById('lightboxImage');
            const info = document.getElementById('lightboxInfo');

            title.textContent = photo.name;
            const dateStr = photo.date ? new Date(photo.date).toLocaleString() : '';
            const sizeMB = (photo.size / (1024 * 1024)).toFixed(2) + ' MB';
            sub.textContent = dateStr + ' • ' + sizeMB + (photo.width ? ' • ' + photo.width + 'x' + photo.height : '');
            info.textContent = photo.mime_type || 'image/jpeg';

            if (photo.thumbnail) {
                img.src = 'data:image/jpeg;base64,' + photo.thumbnail;
            } else {
                img.src = '';
            }

            modal.style.display = 'flex';
        }

        function closePhotoLightbox() {
            const modal = document.getElementById('photoLightboxModal');
            if (modal) modal.style.display = 'none';
            activeLightboxPhoto = null;
        }

        async function downloadPhotoToPc(photoId) {
            try {
                const res = await fetch('/photos/download?id=' + photoId, { method: 'POST' });
                const data = await res.json();
                if (data.success) {
                    alert('📥 Fotoğraf telefondan talep edildi! İndirildiğinde Downloads klasörünüze kaydedilecektir.');
                    setTimeout(loadTransferredFiles, 2000);
                }
            } catch (e) {
                alert('İndirme hatası: ' + e.message);
            }
        }

        function downloadLightboxPhoto() {
            if (activeLightboxPhoto) {
                downloadPhotoToPc(activeLightboxPhoto.id);
            }
        }

        function copyLightboxImage() {
            if (!activeLightboxPhoto) return;
            alert('Fotoğraf adı: ' + activeLightboxPhoto.name);
        }

        function escapeHtml(str) {
            return String(str).replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;').replace(/"/g, '&quot;');
        }

        function formatTime(ts) {
            if (!ts) return '';
            const d = new Date(ts);
            return d.getHours().toString().padStart(2, '0') + ':' + d.getMinutes().toString().padStart(2, '0');
        }

        setInterval(fetchStatus, 2000);
        fetchStatus();
        loadSmsList();
        loadTransferredFiles();
        loadContactsList();
        loadPhotosList();
        window.addEventListener('DOMContentLoaded', setupDropZone);
        setTimeout(setupDropZone, 500);
    </script>
</body>
</html>`
