package server

import (
	"context"
	"encoding/base64"
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
	smsMessages      []protocol.SmsMessage
	smsMu            sync.RWMutex
	transferredFiles []TransferredFile
	filesMu          sync.RWMutex
	sharedFilesDir   string
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
	appData := os.Getenv("APPDATA")
	if appData != "" {
		dir := filepath.Join(appData, "AndroidSync", "shared_files")
		_ = os.MkdirAll(dir, 0755)
		return dir
	}
	dir := filepath.Join(".", "shared_files")
	_ = os.MkdirAll(dir, 0755)
	return dir
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
		clients:          make(map[*websocket.Conn]bool),
		notifications:    make([]protocol.NotificationPayload, 0, 50),
		smsMessages:      make([]protocol.SmsMessage, 0, 100),
		transferredFiles: make([]TransferredFile, 0, 50),
		sharedFilesDir:   getSharedFilesDir(),
		contacts:         make([]protocol.ContactItem, 0, 100),
		photos:           make([]protocol.PhotoItem, 0, 50),
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
		var lastClipImg string
		if imgBytes, err := s.clipManager.GetClipboardImage(); err == nil && len(imgBytes) > 0 {
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
		if pcMedia != nil {
			statusResp["pc_media"] = pcMedia
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

	// File Upload from Phone to PC
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
				_ = windows.ShowToast("📁 Yeni Dosya Alındı: "+fInfo.FileName, fmt.Sprintf("%.2f MB - İndirilenler klasörüne kaydedildi.", sizeMB), "Android Sync")
				log.Printf("[Dosya] Telefonda dosya başarıyla alındı: %s (%.2f MB)", dstPath, sizeMB)
			}
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"success": true,
			"files":   uploadedList,
		})
	})

	// File Send from PC to Phone (via Web Dashboard)
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
				log.Printf("[Dosya] PC'den telefona dosya hazırlandı ve sinyal gönderildi: %s (URL: %s)", cleanName, dlURL)
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

	// Open Downloads Folder in Windows Explorer
	mux.HandleFunc("/file/open_folder", func(w http.ResponseWriter, r *http.Request) {
		dir := getDownloadsDir()
		_ = exec.Command("explorer.exe", dir).Start()
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

	// Open URL in Local Browser
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

	// Photos Download API (Requests full photo from phone to be uploaded to PC)
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
			if p.Type == "image" && p.ImageBase64 != "" {
				imgBytes, err := base64.StdEncoding.DecodeString(p.ImageBase64)
				if err == nil && len(imgBytes) > 0 {
					log.Printf("[Pano] 🖼 Telefondan görsel alındı (%d bayt)", len(imgBytes))
					_ = s.clipManager.SetClipboardImage(imgBytes)
					_ = windows.ShowToast("📋 Pano: Görsel Alındı", "Telefonda kopyalanan görsel Windows panosuna yazıldı (Ctrl+V ile yapıştırabilirsiniz).", "Android Sync")
				}
			} else if p.Text != "" {
				log.Printf("[Pano] Telefondan metin alındı (%d bayt)", len(p.Text))
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
			_ = windows.ShowToast("📁 Dosya Alındı: "+p.FileName, fmt.Sprintf("%.2f MB - İndirilenler klasörüne kaydedildi.", sizeMB), "Android Sync")
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
			_ = windows.ShowToast("🌐 Telefondan Bağlantı Açıldı", p.URL, "Android Sync")
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

	case protocol.EventPing:
		resp, _ := protocol.NewMessage(protocol.EventPong, map[string]int64{"time": time.Now().UnixMilli()})
		s.Broadcast(resp)

	default:
		log.Printf("[Server] Bilinmeyen event: %s", msg.Event)
	}
}

func (s *SyncServer) openURLInBrowser(rawURL string) error {
	u := strings.TrimSpace(rawURL)
	if !strings.HasPrefix(u, "http://") && !strings.HasPrefix(u, "https://") {
		u = "https://" + u
	}
	return exec.Command("rundll32", "url.dll,FileProtocolHandler", u).Start()
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

// executeRemoteAction executes remote PC commands (Lock, Sleep, Shutdown, Restart).
func (s *SyncServer) executeRemoteAction(action string) {
	log.Printf("[Remote] Uzaktan sistem komutu tetiklendi: %s", action)
	switch action {
	case "LOCK":
		_ = exec.Command("rundll32.exe", "user32.dll,LockWorkStation").Start()
	case "SLEEP":
		_ = exec.Command("rundll32.exe", "powrprof.dll,SetSuspendState", "0,1,0").Start()
	case "SHUTDOWN":
		_ = exec.Command("shutdown", "/s", "/t", "10").Start()
	case "CANCEL_SHUTDOWN":
		_ = exec.Command("shutdown", "/a").Start()
	case "RESTART":
		_ = exec.Command("shutdown", "/r", "/t", "10").Start()
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
                <li class="nav-item" onclick="switchTab('files')">
                    <span>📁</span> <span>Dosya Paylaşımı</span>
                    <span class="nav-badge" id="filesNavBadge">0</span>
                </li>
                <li class="nav-item" onclick="switchTab('contacts')">
                    <span>👥</span> <span>Rehber &amp; Kişiler</span>
                    <span class="nav-badge" id="contactsNavBadge">0</span>
                </li>
                <li class="nav-item" onclick="switchTab('tabsharing')">
                    <span>🌐</span> <span>Sekme Paylaşımı</span>
                </li>
                <li class="nav-item" onclick="switchTab('photos')">
                    <span>📸</span> <span>Fotoğraflar</span>
                    <span class="nav-badge" id="photosNavBadge">0</span>
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
                    <button class="btn btn-secondary" style="font-size:12px; padding:6px 12px;" onclick="remoteAction('LOCK')" title="Bilgisayarı Kilitle">🔒 Kilitle</button>
                    <button class="btn btn-secondary" style="font-size:12px; padding:6px 12px;" onclick="remoteAction('SLEEP')" title="Bilgisayarı Uyku Moduna Al">🌙 Uyku</button>
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
                            Bilgisayarınızda veya telefonunuzda bir metni kopyaladığınız veya ekran görüntüsü aldığınız anda tüm cihazlarınızın panosu senkronize edilir.
                        </p>

                        <!-- Görsel Pano Önizleme Alanı -->
                        <div id="clipImageWrapper" style="display:none; margin-bottom:20px; padding:16px; background:rgba(0,0,0,0.3); border:1px solid var(--border-card); border-radius:var(--radius-md);">
                            <div style="font-size:12px; font-weight:700; color:var(--accent-blue); margin-bottom:10px; display:flex; align-items:center; gap:8px;">
                                <span>🖼️ Panodaki Görsel (PNG)</span>
                                <span id="clipImageSize" style="color:var(--text-muted); font-size:11px; font-weight:normal;"></span>
                            </div>
                            <img id="clipImagePreview" src="" style="max-height:260px; max-width:100%; border-radius:8px; object-fit:contain; border:1px solid rgba(255,255,255,0.1); background:#111;" />
                            <div style="margin-top:12px; display:flex; gap:10px;">
                                <a id="clipImageDownloadBtn" href="#" download="clipboard.png" class="btn btn-secondary" style="font-size:12px; padding:6px 14px; text-decoration:none; display:inline-flex; align-items:center; gap:6px;">📥 Görseli İndir</a>
                            </div>
                        </div>

                        <div id="fullClipBox" style="background:rgba(0,0,0,0.4); border:1px solid var(--border-card); border-radius:var(--radius-md); padding:20px; font-family:'JetBrains Mono',monospace; font-size:14px; min-height:100px; margin-bottom:20px; white-space:pre-wrap; word-break:break-all;">Pano boş...</div>
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

            <!-- TAB 7: Dosya Paylaşımı -->
            <div id="tab-files" class="tab-content">
                <div class="overview-grid">
                    <!-- Drop Zone Card -->
                    <div class="card col-12">
                        <div style="display:flex; justify-content:space-between; align-items:center; margin-bottom:16px;">
                            <h3 style="font-size:18px; font-weight:700;">📁 Wi-Fi Dosya Gönderimi (PC ➡️ Telefon)</h3>
                            <button class="btn btn-secondary" onclick="openDownloadsFolder()">📂 İndirilenler Klasörünü Aç</button>
                        </div>
                        <div id="dropZone" style="border: 2px dashed rgba(56, 189, 248, 0.4); border-radius: var(--radius-lg); padding: 40px 20px; text-align: center; background: rgba(56, 189, 248, 0.04); cursor: pointer; transition: all 0.3s ease;">
                            <div style="font-size: 40px; margin-bottom: 12px;">📤</div>
                            <div style="font-size: 15px; font-weight: 700; color: var(--text-primary); margin-bottom: 6px;">Dosyaları buraya sürükleyip bırakın veya tıklayın</div>
                            <div style="font-size: 12px; color: var(--text-secondary);">Fotoğraflar, videolar, belgeler ve APK'lar doğrudan telefonun Downloads klasörüne aktarılır</div>
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

                    <!-- Transferred Files List -->
                    <div class="card col-12">
                        <div style="display:flex; justify-content:space-between; align-items:center; margin-bottom:16px;">
                            <h3 style="font-size:16px; font-weight:700;">🔄 Aktarılan Dosyalar Geçmişi</h3>
                            <button class="btn btn-secondary" style="font-size:12px; padding:4px 10px;" onclick="loadTransferredFiles()">Yenile 🔄</button>
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

            <!-- TAB 8: Kişiler & Rehber -->
            <div id="tab-contacts" class="tab-content">
                <div class="overview-grid">
                    <div class="card col-12">
                        <div style="display: flex; justify-content: space-between; align-items: center; margin-bottom: 20px; flex-wrap: wrap; gap: 14px;">
                            <div>
                                <h3 style="font-size: 18px; font-weight: 800;">👥 Telefon Rehberi &amp; Kişiler</h3>
                                <div style="font-size: 13px; color: var(--text-secondary); margin-top: 4px;">
                                    Telefonunuzdaki kişilerle doğrudan arama başlatabilir veya SMS gönderebilirsiniz.
                                </div>
                            </div>
                            <div style="display: flex; gap: 10px; align-items: center;">
                                <span class="nav-badge" id="contactsHeaderBadge" style="font-size: 13px; padding: 6px 14px;">0 Kişi</span>
                                <button class="btn btn-secondary" onclick="refreshContacts()" style="font-size: 13px; padding: 8px 16px;">🔄 Telefından Yenile</button>
                            </div>
                        </div>

                        <!-- Search Bar -->
                        <div style="position: relative; margin-bottom: 20px;">
                            <input type="text" id="contactsSearchInput" class="search-input" placeholder="🔍 İsim veya telefon numarası ile hızlı filtreleyin..." oninput="filterContacts()" style="padding: 12px 18px; font-size: 14px; background: rgba(0,0,0,0.25);">
                        </div>

                        <!-- Contacts Grid Container -->
                        <div id="contactsGrid" style="display: grid; grid-template-columns: repeat(auto-fill, minmax(290px, 1fr)); gap: 14px; max-height: calc(100vh - 300px); overflow-y: auto; padding-right: 4px;">
                            <div style="grid-column: 1 / -1; padding: 48px; text-align: center; color: var(--text-muted);">
                                Rehber yükleniyor veya telefondan senkronize ediliyor...
                            </div>
                        </div>
                    </div>
                </div>
            </div>

            <!-- TAB 9: Sekme Paylaşımı -->
            <div id="tab-tabsharing" class="tab-content">
                <div class="overview-grid">
                    <!-- Send URL Card -->
                    <div class="card col-12">
                        <div style="margin-bottom: 20px;">
                            <h3 style="font-size: 18px; font-weight: 800;">🌐 Sekme ve Web Sayfası Paylaşımı</h3>
                            <div style="font-size: 13px; color: var(--text-secondary); margin-top: 4px;">
                                Bilgisayarınızda veya telefonunuzda gezindiğiniz web sayfalarını diğer cihazın varsayılan tarayıcısında anında açın.
                            </div>
                        </div>

                        <div style="background: rgba(0,0,0,0.2); border: 1px solid var(--border-card); border-radius: var(--radius-lg); padding: 20px; margin-bottom: 20px;">
                            <label style="display: block; font-size: 13px; font-weight: 700; color: var(--text-primary); margin-bottom: 8px;">Telefonda Açılacak Web Bağlantısı (URL):</label>
                            <div style="display: flex; gap: 10px; flex-wrap: wrap;">
                                <input type="url" id="sendTabUrlInput" class="chat-input" placeholder="https://ornek-site.com/sayfa" style="flex: 1; min-width: 250px;">
                                <button class="btn btn-primary" onclick="sendTabToPhone()" style="padding: 12px 24px; font-weight: 700;">🚀 Telefonda Anında Aç</button>
                                <button class="btn btn-secondary" onclick="openLocalTab()" style="padding: 12px 18px;">💻 Bu PC'de Aç</button>
                            </div>
                        </div>

                        <!-- Quick Shortcuts -->
                        <div style="margin-bottom: 24px;">
                            <div style="font-size: 12px; font-weight: 700; color: var(--text-muted); text-transform: uppercase; margin-bottom: 10px;">Hızlı Kısayollar:</div>
                            <div style="display: flex; gap: 8px; flex-wrap: wrap;">
                                <button class="btn btn-secondary" style="font-size: 12px; padding: 6px 12px;" onclick="fillAndSendUrl('https://github.com')">🐙 GitHub</button>
                                <button class="btn btn-secondary" style="font-size: 12px; padding: 6px 12px;" onclick="fillAndSendUrl('https://youtube.com')">▶ YouTube</button>
                                <button class="btn btn-secondary" style="font-size: 12px; padding: 6px 12px;" onclick="fillAndSendUrl('https://maps.google.com')">🗺 Google Maps</button>
                                <button class="btn btn-secondary" style="font-size: 12px; padding: 6px 12px;" onclick="fillAndSendUrl('https://wikipedia.org')">📖 Wikipedia</button>
                            </div>
                        </div>

                        <!-- Shared Links History -->
                        <div>
                            <h4 style="font-size: 15px; font-weight: 700; margin-bottom: 12px;">🔗 Son Paylaşılan Bağlantılar</h4>
                            <div id="sharedTabsList" style="display: flex; flex-direction: column; gap: 8px;">
                                <div style="padding: 20px; text-align: center; color: var(--text-muted); font-size: 13px;">
                                    Henüz paylaşılan bir web sekmesi yok. Telefonda tarayıcıdan "Paylaş &gt; Android Sync" seçerek de buraya sekme gönderebilirsiniz!
                                </div>
                            </div>
                        </div>
                    </div>
                </div>
            </div>

            <!-- TAB 10: Fotoğraf Galerisi -->
            <div id="tab-photos" class="tab-content">
                <div class="overview-grid">
                    <div class="card col-12">
                        <div style="display: flex; justify-content: space-between; align-items: center; margin-bottom: 20px; flex-wrap: wrap; gap: 14px;">
                            <div>
                                <h3 style="font-size: 18px; font-weight: 800;">📸 Telefon Fotoğraf Galerisi</h3>
                                <div style="font-size: 13px; color: var(--text-secondary); margin-top: 4px;">
                                    Telefonunuzdaki son fotoğrafları ve ekran görüntülerini anında görüntüleyin, PC'ye indirin veya panoya kopyalayın.
                                </div>
                            </div>
                            <div style="display: flex; gap: 10px; align-items: center;">
                                <span class="nav-badge" id="photosHeaderBadge" style="font-size: 13px; padding: 6px 14px;">0 Fotoğraf</span>
                                <button class="btn btn-secondary" onclick="refreshPhotos()" style="font-size: 13px; padding: 8px 16px;">🔄 Telefondan Yenile</button>
                            </div>
                        </div>

                        <!-- Gallery Filter & Search -->
                        <div style="display: flex; justify-content: space-between; align-items: center; margin-bottom: 20px; flex-wrap: wrap; gap: 12px;">
                            <div style="display: flex; gap: 8px;">
                                <button class="btn btn-primary" id="photoFilterAll" onclick="setPhotoFilter('all')" style="font-size: 12px; padding: 6px 14px;">Tümü</button>
                                <button class="btn btn-secondary" id="photoFilterCamera" onclick="setPhotoFilter('camera')" style="font-size: 12px; padding: 6px 14px;">📷 Kamera</button>
                                <button class="btn btn-secondary" id="photoFilterScreenshots" onclick="setPhotoFilter('screenshots')" style="font-size: 12px; padding: 6px 14px;">📱 Ekran Görüntüleri</button>
                            </div>
                            <input type="text" id="photosSearchInput" class="search-input" placeholder="🔍 Dosya adıyla ara..." oninput="filterPhotos()" style="width: 250px; padding: 8px 14px; font-size: 13px;">
                        </div>

                        <!-- Photos Grid Container -->
                        <div id="photosGrid" style="display: grid; grid-template-columns: repeat(auto-fill, minmax(180px, 1fr)); gap: 16px; max-height: calc(100vh - 300px); overflow-y: auto; padding-right: 4px;">
                            <div style="grid-column: 1 / -1; padding: 48px; text-align: center; color: var(--text-muted);">
                                Fotoğraflar yükleniyor veya telefondan senkronize ediliyor...
                            </div>
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
                        <button class="btn btn-secondary" onclick="closePhotoLightbox()" style="padding: 6px 12px; font-size: 14px;">✕</button>
                    </div>
                    <div style="flex: 1; display: flex; align-items: center; justify-content: center; background: #030712; padding: 16px; overflow: hidden;">
                        <img id="lightboxImage" src="" alt="Önizleme" style="max-width: 100%; max-height: 60vh; object-fit: contain; border-radius: var(--radius-md);">
                    </div>
                    <div style="display: flex; justify-content: space-between; align-items: center; padding: 14px 20px; border-top: 1px solid var(--border-card); background: rgba(18, 24, 38, 0.8);">
                        <div id="lightboxInfo" style="font-size: 12px; color: var(--text-muted);">-</div>
                        <div style="display: flex; gap: 8px;">
                            <button class="btn btn-secondary" id="lightboxCopyBtn" onclick="copyLightboxImage()" style="font-size: 12px; padding: 8px 14px;">📋 Panoya Kopyala</button>
                            <button class="btn btn-primary" id="lightboxDownloadBtn" onclick="downloadLightboxPhoto()" style="font-size: 12px; padding: 8px 16px;">⬇️ PC'ye İndir</button>
                        </div>
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

            const navIdx = ['overview', 'calls', 'sms', 'media', 'clipboard', 'notifications', 'files', 'contacts', 'tabsharing', 'photos'].indexOf(tabId);
            const navItems = document.querySelectorAll('.nav-item');
            if (navIdx >= 0 && navItems[navIdx]) navItems[navIdx].classList.add('active');

            const titles = {
                'overview': 'Genel Bakış',
                'calls': 'Telefon & Aramalar',
                'sms': 'Mesajlar (SMS)',
                'media': 'Medya & Ses',
                'clipboard': 'Ortak Pano',
                'notifications': 'Canlı Bildirimler',
                'files': 'Wi-Fi Dosya Paylaşımı',
                'contacts': 'Telefon Rehberi & Kişiler',
                'tabsharing': 'Sekme & Bağlantı Paylaşımı',
                'photos': 'Fotoğraf Galerisi'
            };
            document.getElementById('pageTitle').innerText = titles[tabId] || 'Genel Bakış';

            if (tabId === 'sms') {
                loadSmsList();
            } else if (tabId === 'files') {
                loadTransferredFiles();
            } else if (tabId === 'contacts') {
                loadContactsList();
            } else if (tabId === 'photos') {
                loadPhotosList();
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

                // Contacts Count
                if (data.contacts_count !== undefined) {
                    const cBadge = document.getElementById('contactsNavBadge');
                    if (cBadge) cBadge.innerText = data.contacts_count;
                    const chBadge = document.getElementById('contactsHeaderBadge');
                    if (chBadge) chBadge.innerText = data.contacts_count + ' Kişi';
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
                if (data.clipboard_image) {
                    const wrap = document.getElementById('clipImageWrapper');
                    if (wrap) {
                        wrap.style.display = 'block';
                        const img = document.getElementById('clipImagePreview');
                        img.src = 'data:image/png;base64,' + data.clipboard_image;
                        const dl = document.getElementById('clipImageDownloadBtn');
                        dl.href = 'data:image/png;base64,' + data.clipboard_image;
                        const sizeEl = document.getElementById('clipImageSize');
                        if (sizeEl) sizeEl.innerText = '(' + Math.round((data.clipboard_image.length * 3 / 4) / 1024) + ' KB)';
                    }
                    const qEl = document.getElementById('quickClipText');
                    if (qEl && (!data.clipboard || data.clipboard === '')) {
                        qEl.innerHTML = '<span style="color:var(--accent-blue);">🖼️ [Panoda Görsel Var - ' + Math.round((data.clipboard_image.length * 3 / 4) / 1024) + ' KB]</span>';
                    }
                } else {
                    const wrap = document.getElementById('clipImageWrapper');
                    if (wrap) wrap.style.display = 'none';
                }
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
                            var replyBox = '';
                            if (n.can_reply && n.key) {
                                replyBox = '<div style="margin-top:10px; display:flex; gap:8px;">' +
                                    '<input type="text" id="replyInput_' + escapeHtml(n.id) + '" placeholder="Yanıt yazın..." style="flex:1; background:var(--bg-input); border:1px solid var(--border-card); border-radius:var(--radius-sm); padding:6px 12px; color:var(--text-primary); font-size:12px; outline:none;" onkeydown="if(event.key===\'Enter\') sendNotificationReply(\'' + escapeHtml(n.key) + '\', \'' + escapeHtml(n.id) + '\')">' +
                                    '<button class="btn btn-primary" style="font-size:12px; padding:6px 14px;" onclick="sendNotificationReply(\'' + escapeHtml(n.key) + '\', \'' + escapeHtml(n.id) + '\')">Yanıtla</button>' +
                                '</div>';
                            }
                            var actionsBox = '';
                            if (n.actions && n.actions.length > 0 && n.key) {
                                actionsBox = '<div style="margin-top:10px; display:flex; flex-wrap:wrap; gap:8px;">' +
                                    n.actions.filter(function(a) { return !a.is_reply; }).map(function(a) {
                                        return '<button class="btn btn-secondary" style="font-size:11px; padding:5px 12px; border-radius:6px; cursor:pointer;" onclick="triggerNotificationAction(\'' + escapeHtml(n.key) + '\', ' + a.index + ', this)">⚡ ' +
                                            escapeHtml(a.title) +
                                        '</button>';
                                    }).join('') +
                                '</div>';
                            }
                            return '<div style="background:rgba(255,255,255,0.03); border:1px solid rgba(255,255,255,0.06); border-radius:10px; padding:14px;">' +
                                '<div style="display:flex; justify-content:space-between; margin-bottom:4px;">' +
                                    '<strong style="color:var(--accent-blue); font-size:13px;">' + escapeHtml(n.app_name || 'Uygulama') + '</strong>' +
                                    '<span style="font-size:11px; color:var(--text-muted);">' + formatTime(n.timestamp) + '</span>' +
                                '</div>' +
                                '<div style="font-weight:700; font-size:14px;">' + escapeHtml(n.title || '') + '</div>' +
                                '<div style="font-size:13px; color:var(--text-secondary); margin-top:2px;">' + escapeHtml(n.text || '') + '</div>' +
                                actionsBox +
                                replyBox +
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

                // Files count badge
                if (data.files_count !== undefined) {
                    const fBadge = document.getElementById('filesNavBadge');
                    if (fBadge) fBadge.innerText = data.files_count;
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
                'LOCK': 'Bilgisayarı Kilitlemek',
                'SLEEP': 'Bilgisayarı Uyku Moduna Almak',
                'SHUTDOWN': 'Bilgisayarı Kapatmak',
                'RESTART': 'Bilgisayarı Yeniden Başlatmak'
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
                const badge = document.getElementById('filesNavBadge');
                if (badge) badge.innerText = files.length;
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
                        ? '<button class="btn btn-secondary" style="font-size:11px; padding:4px 10px;" onclick="openDownloadsFolder()">Klasörde Göster</button>'
                        : '<a href="/file/download/' + f.id + '/' + encodeURIComponent(f.file_name) + '" class="btn btn-secondary" style="font-size:11px; padding:4px 10px; text-decoration:none;">İndir</a>';
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
                const badge = document.getElementById('contactsNavBadge');
                if (badge) badge.innerText = allContacts.length;
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
                        '<button class="btn btn-success" style="flex: 1; padding: 6px 0; font-size: 11px;" onclick="callAction(\'DIAL\', \'' + safeNum + '\')" title="Telefonla Ara">📞 Ara</button>' +
                        '<button class="btn btn-primary" style="flex: 1; padding: 6px 0; font-size: 11px;" onclick="startSmsWith(\'' + safeNum + '\', \'' + safeName + '\')" title="SMS Gönder">💬 SMS</button>' +
                        '<button class="btn btn-secondary" style="padding: 6px 10px; font-size: 11px;" onclick="copyTextToClip(\'' + safeNum + '\')" title="Numarayı Kopyala">📋</button>' +
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
            switchTab('sms');
            setTimeout(() => {
                const searchInput = document.querySelector('.chat-search-bar input');
                if (searchInput) {
                    searchInput.value = number;
                    searchInput.dispatchEvent(new Event('input'));
                }
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
                var icon = isOut ? '📱 ➡️' : '💻 ⬅️';
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
                        '<button class="btn btn-secondary" style="font-size: 11px; padding: 4px 10px;" onclick="copyTextToClip(\'' + rawUrl + '\')">📋 Kopyala</button>' +
                        '<a class="btn btn-primary" style="font-size: 11px; padding: 4px 10px; text-decoration: none;" href="' + safeUrl + '" target="_blank">Aç 🔗</a>' +
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
                const badge = document.getElementById('photosNavBadge');
                if (badge) badge.innerText = allPhotos.length;
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
                    btn.className = (f === filter ? 'btn btn-primary' : 'btn btn-secondary');
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
                            '<button class="btn btn-secondary" style="flex: 1; font-size: 11px; padding: 5px 0;" onclick="openPhotoLightbox(' + p.id + ')">🔍 Önizle</button>' +
                            '<button class="btn btn-primary" style="flex: 1; font-size: 11px; padding: 5px 0;" onclick="downloadPhotoToPc(' + p.id + ')">⬇️ İndir</button>' +
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

        setInterval(updateStatus, 1500);
        updateStatus();
        loadSmsList();
        loadTransferredFiles();
        loadContactsList();
        loadPhotosList();
        window.addEventListener('DOMContentLoaded', setupDropZone);
        setTimeout(setupDropZone, 500);
    </script>
</body>
</html>
`
