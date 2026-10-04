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

type ClientDevice struct {
	ID           string                              `json:"id"`
	Name         string                              `json:"name"`
	Model        string                              `json:"model"`
	IP           string                              `json:"ip"`
	RemoteAddr   string                              `json:"remote_addr"`
	BatteryLevel int                                 `json:"battery_level"`
	IsCharging   bool                                `json:"is_charging"`
	ConnectedAt  int64                               `json:"connected_at"`
	LastSeen     int64                               `json:"last_seen"`
	Media        *protocol.MediaInfoPayload          `json:"media,omitempty"`
	CallState    *protocol.CallStatePayload          `json:"call_state,omitempty"`
	SmsList      []protocol.SmsMessage               `json:"sms_list,omitempty"`
	Contacts     []protocol.ContactItem              `json:"contacts,omitempty"`
	Photos       []protocol.PhotoItem                `json:"photos,omitempty"`
	Apps         []protocol.InstalledAppInfo         `json:"apps,omitempty"`
	LastFrame    *protocol.ScreenMirrorFramePayload  `json:"-"`
	Storage      *protocol.StorageMountStatusPayload `json:"storage,omitempty"`
	Hotspot      *protocol.HotspotStatusPayload      `json:"hotspot,omitempty"`
	conn         *websocket.Conn
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
	devicesMu       sync.RWMutex
	devices         map[string]*ClientDevice
	connToDevID     map[*websocket.Conn]string
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
	lastMirrorFrame   *protocol.ScreenMirrorFramePayload
	mirrorMu          sync.RWMutex
	lastStorageStatus *protocol.StorageMountStatusPayload
	storageMu         sync.RWMutex
	lastHotspotStatus *protocol.HotspotStatusPayload
	hotspotMu         sync.RWMutex
	isCallAudioActive bool
	callAudioMu       sync.RWMutex
	lastAudioFrame    string
	installedApps     []protocol.InstalledAppInfo
	appsMu            sync.RWMutex
}

type TransferredFile struct {
	ID         string `json:"id"`
	FileName   string `json:"file_name"`
	FileSize   int64  `json:"file_size"`
	Path       string `json:"path"`
	Direction  string `json:"direction"` // "incoming" or "outgoing"
	Timestamp  int64  `json:"timestamp"`
	DeviceID   string `json:"device_id,omitempty"`
	DeviceName string `json:"device_name,omitempty"`
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
		port:             port,
		serverName:       serverName,
		machineID:        cfg.MachineID,
		pairingPIN:       generateNewPIN(),
		isPaired:         cfg.AuthToken != "",
		configPath:       cfgPath,
		config:           cfg,
		clipManager:      clipManager,
		clients:          make(map[*websocket.Conn]bool),
		devices:          make(map[string]*ClientDevice),
		connToDevID:      make(map[*websocket.Conn]string),
		notifications:    make([]protocol.NotificationPayload, 0, 50),
		smsMessages:      make([]protocol.SmsMessage, 0, 100),
		transferredFiles: make([]TransferredFile, 0, 50),
		sharedFilesDir:   getSharedFilesDir(),
		audioHub:         NewAudioHub(),
		contacts:         make([]protocol.ContactItem, 0, 100),
		photos:           make([]protocol.PhotoItem, 0, 50),
		installedApps:    make([]protocol.InstalledAppInfo, 0, 100),
	}
	s.saveConfig()
	return s
}

func (s *SyncServer) registerDevice(conn *websocket.Conn) *ClientDevice {
	host, _, err := net.SplitHostPort(conn.RemoteAddr().String())
	if err != nil || host == "" {
		host = "client"
	}
	devID := fmt.Sprintf("dev_%s", strings.ReplaceAll(host, ".", "_"))

	s.devicesMu.Lock()
	defer s.devicesMu.Unlock()

	if existing, exists := s.devices[devID]; exists && existing.conn != conn {
		_, port, _ := net.SplitHostPort(conn.RemoteAddr().String())
		devID = fmt.Sprintf("%s_%s", devID, port)
	}

	dev := &ClientDevice{
		ID:           devID,
		Name:         "Android (" + host + ")",
		Model:        "Android",
		IP:           host,
		RemoteAddr:   conn.RemoteAddr().String(),
		BatteryLevel: 100,
		ConnectedAt:  time.Now().UnixMilli(),
		LastSeen:     time.Now().UnixMilli(),
		SmsList:      make([]protocol.SmsMessage, 0),
		Contacts:     make([]protocol.ContactItem, 0),
		Photos:       make([]protocol.PhotoItem, 0),
		Apps:         make([]protocol.InstalledAppInfo, 0),
		conn:         conn,
	}
	s.devices[devID] = dev
	s.connToDevID[conn] = devID
	return dev
}

func (s *SyncServer) unregisterDevice(conn *websocket.Conn) {
	s.devicesMu.Lock()
	defer s.devicesMu.Unlock()
	if devID, ok := s.connToDevID[conn]; ok {
		delete(s.devices, devID)
		delete(s.connToDevID, conn)
	}
}

func (s *SyncServer) getDevice(id string) *ClientDevice {
	s.devicesMu.RLock()
	defer s.devicesMu.RUnlock()
	if id != "" && id != "all" {
		if d, ok := s.devices[id]; ok {
			return d
		}
	}
	for _, d := range s.devices {
		return d
	}
	return nil
}

func (s *SyncServer) getDeviceList() []*ClientDevice {
	s.devicesMu.RLock()
	defer s.devicesMu.RUnlock()
	list := make([]*ClientDevice, 0, len(s.devices))
	for _, d := range s.devices {
		list = append(list, d)
	}
	return list
}

func (s *SyncServer) SendToDevice(deviceID string, msg *protocol.Message) {
	if deviceID == "" || deviceID == "all" {
		s.Broadcast(msg)
		return
	}
	s.devicesMu.RLock()
	dev, ok := s.devices[deviceID]
	s.devicesMu.RUnlock()
	if ok && dev.conn != nil {
		if err := dev.conn.WriteJSON(msg); err != nil {
			log.Printf("[Server] SendToDevice (%s) hatası: %v", deviceID, err)
		}
		return
	}
	s.Broadcast(msg)
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

func (s *SyncServer) removeNotification(key, id string) {
	s.notifsMu.Lock()
	defer s.notifsMu.Unlock()
	filtered := make([]protocol.NotificationPayload, 0, len(s.notifications))
	for _, n := range s.notifications {
		if (key != "" && n.Key == key) || (id != "" && n.ID == id) {
			continue
		}
		filtered = append(filtered, n)
	}
	s.notifications = filtered
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

		s.mirrorMu.RLock()
		statusResp["screen_mirror_active"] = s.lastMirrorFrame != nil
		s.mirrorMu.RUnlock()

		s.storageMu.RLock()
		if s.lastStorageStatus != nil {
			statusResp["storage_status"] = s.lastStorageStatus
		}
		s.storageMu.RUnlock()

		s.hotspotMu.RLock()
		if s.lastHotspotStatus != nil {
			statusResp["hotspot_status"] = s.lastHotspotStatus
		}
		s.hotspotMu.RUnlock()

		s.callAudioMu.RLock()
		statusResp["call_audio_active"] = s.isCallAudioActive
		s.callAudioMu.RUnlock()

		statusResp["devices"] = s.getDeviceList()

		_ = json.NewEncoder(w).Encode(statusResp)
	})

	// Connected Devices API
	mux.HandleFunc("/devices", func(w http.ResponseWriter, r *http.Request) {
		devList := s.getDeviceList()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"success": true,
			"devices": devList,
			"count":   len(devList),
		})
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
		deviceID := r.URL.Query().Get("device_id")
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

		log.Printf("[Çağrı] Komut iletiliyor (Cihaz: %s): %s (Numara: %s)", deviceID, action, number)
		s.SendCallActionToDevice(deviceID, action, numPtr, valPtr)

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
		deviceID := r.URL.Query().Get("device_id")
		var msgsCopy []protocol.SmsMessage
		if deviceID != "" && deviceID != "all" {
			if dev := s.getDevice(deviceID); dev != nil {
				s.devicesMu.RLock()
				msgsCopy = make([]protocol.SmsMessage, len(dev.SmsList))
				copy(msgsCopy, dev.SmsList)
				s.devicesMu.RUnlock()
			}
		} else {
			s.smsMu.RLock()
			msgsCopy = make([]protocol.SmsMessage, len(s.smsMessages))
			copy(msgsCopy, s.smsMessages)
			s.smsMu.RUnlock()
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"messages": msgsCopy,
			"count":    len(msgsCopy),
		})
	})

	// SMS Sync Request API
	mux.HandleFunc("/sms/sync", func(w http.ResponseWriter, r *http.Request) {
		deviceID := r.URL.Query().Get("device_id")
		s.RequestSmsSyncFromDevice(deviceID)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "status": "sync_requested"})
	})

	// SMS Send API
	mux.HandleFunc("/sms/send", func(w http.ResponseWriter, r *http.Request) {
		recipient := r.URL.Query().Get("recipient")
		body := r.URL.Query().Get("body")
		deviceID := r.URL.Query().Get("device_id")

		if r.Method == http.MethodPost {
			_ = r.ParseForm()
			if rRec := r.FormValue("recipient"); rRec != "" {
				recipient = rRec
			}
			if rBody := r.FormValue("body"); rBody != "" {
				body = rBody
			}
			if rDev := r.FormValue("device_id"); rDev != "" {
				deviceID = rDev
			}
		}

		if recipient == "" || body == "" {
			http.Error(w, "recipient and body required", http.StatusBadRequest)
			return
		}

		log.Printf("[SMS] Gönderiliyor (%s): -> %s: %s", deviceID, recipient, body)
		s.SendSmsToDevice(deviceID, recipient, body)

		devName := ""
		if dev := s.getDevice(deviceID); dev != nil {
			devName = dev.Name
		}

		newMsg := protocol.SmsMessage{
			ID:         fmt.Sprintf("mac_local_%d", time.Now().UnixMilli()),
			ThreadID:   0,
			Address:    recipient,
			Body:       body,
			Timestamp:  time.Now().UnixMilli(),
			IsIncoming: false,
			Read:       true,
			DeviceID:   deviceID,
			DeviceName: devName,
		}
		s.smsMu.Lock()
		s.smsMessages = append([]protocol.SmsMessage{newMsg}, s.smsMessages...)
		s.smsMu.Unlock()

		if deviceID != "" {
			s.devicesMu.Lock()
			if dev := s.devices[deviceID]; dev != nil {
				dev.SmsList = append([]protocol.SmsMessage{newMsg}, dev.SmsList...)
			}
			s.devicesMu.Unlock()
		}

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
		deviceID := r.URL.Query().Get("device_id")
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
		log.Printf("[Server] Telefondan işlem istendi (%s): %s (Percent: %.1f)", deviceID, action, pct)
		s.SendPhoneCommandToDevice(deviceID, action, pct)
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
		deviceID := r.URL.Query().Get("device_id")
		text := r.URL.Query().Get("text")
		if r.Method == http.MethodPost {
			_ = r.ParseForm()
			if pText := r.FormValue("text"); pText != "" {
				text = pText
			}
			if pDev := r.FormValue("device_id"); pDev != "" {
				deviceID = pDev
			}
		}
		if text != "" {
			_ = s.clipManager.SetClipboard(text)
			msg, _ := protocol.NewMessage(protocol.EventClipboard, protocol.ClipboardPayload{
				Text:      text,
				Timestamp: time.Now().UnixMilli(),
			})
			s.SendToDevice(deviceID, msg)
			log.Printf("[Pano] Web arayüzünden iletildi (%s, %d bayt)", deviceID, len(text))
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

		deviceID := r.URL.Query().Get("device_id")
		if deviceID == "" {
			deviceID = r.FormValue("device_id")
		}
		devName := ""
		if dev := s.getDevice(deviceID); dev != nil {
			devName = dev.Name
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
					ID:         fmt.Sprintf("file_%d", time.Now().UnixMilli()),
					FileName:   filepath.Base(dstPath),
					FileSize:   written,
					Path:       dstPath,
					Direction:  "incoming",
					Timestamp:  time.Now().UnixMilli(),
					DeviceID:   deviceID,
					DeviceName: devName,
				}
				s.filesMu.Lock()
				s.transferredFiles = append([]TransferredFile{fInfo}, s.transferredFiles...)
				if len(s.transferredFiles) > 100 {
					s.transferredFiles = s.transferredFiles[:100]
				}
				s.filesMu.Unlock()

				uploadedList = append(uploadedList, fInfo)
				sizeMB := float64(written) / (1024 * 1024)
				_ = macos.ShowNotification("📁 Yeni Dosya Alındı: "+fInfo.FileName, fmt.Sprintf("%.2f MB (%s) - İndirilenler klasörüne kaydedildi.", sizeMB, devName), "Android Sync", "Glass")
				log.Printf("[Dosya] Mac'e dosya başarıyla alındı (%s): %s (%.2f MB)", devName, dstPath, sizeMB)
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

		deviceID := r.URL.Query().Get("device_id")
		if deviceID == "" {
			deviceID = r.FormValue("device_id")
		}
		devName := ""
		if dev := s.getDevice(deviceID); dev != nil {
			devName = dev.Name
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
					ID:         fileID,
					FileName:   cleanName,
					FileSize:   written,
					Path:       dstPath,
					Direction:  "outgoing",
					Timestamp:  time.Now().UnixMilli(),
					DeviceID:   deviceID,
					DeviceName: devName,
				}
				s.filesMu.Lock()
				s.transferredFiles = append([]TransferredFile{fInfo}, s.transferredFiles...)
				s.filesMu.Unlock()

				dlURL := fmt.Sprintf("http://%s:%d/file/download/%s/%s", getLocalIP(), s.port, fileID, url.PathEscape(cleanName))
				p := protocol.FileAvailablePayload{
					ID:             fileID,
					FileName:       cleanName,
					FileSize:       written,
					DownloadURL:    dlURL,
					MimeType:       fh.Header.Get("Content-Type"),
					Sender:         s.serverName,
					Timestamp:      time.Now().UnixMilli(),
					TargetDeviceID: deviceID,
				}
				notifiedList = append(notifiedList, p)

				msg, _ := protocol.NewMessage(protocol.EventFileAvailable, p)
				s.SendToDevice(deviceID, msg)
				log.Printf("[Dosya] Mac'ten telefona dosya hazırlandı (%s): %s", deviceID, cleanName)
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
		deviceID := r.URL.Query().Get("device_id")
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
			if d := r.FormValue("device_id"); d != "" {
				deviceID = d
			}
		}

		if key == "" || text == "" {
			http.Error(w, "key and text required", http.StatusBadRequest)
			return
		}

		s.SendNotificationReplyToDevice(deviceID, key, 0, text)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "key": key})
	})

	// Notification Action Trigger API
	mux.HandleFunc("/notification/action", func(w http.ResponseWriter, r *http.Request) {
		deviceID := r.URL.Query().Get("device_id")
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
			if d := r.FormValue("device_id"); d != "" {
				deviceID = d
			}
		}

		if key == "" || idxStr == "" {
			http.Error(w, "key and index required", http.StatusBadRequest)
			return
		}

		actionIndex, _ := strconv.Atoi(idxStr)
		s.SendNotificationActionToDevice(deviceID, key, actionIndex)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "key": key, "index": actionIndex})
	})

	// Notification Dismiss API (Dismiss on Mac and Sync to Phone)
	mux.HandleFunc("/notification/dismiss", func(w http.ResponseWriter, r *http.Request) {
		deviceID := r.URL.Query().Get("device_id")
		key := r.URL.Query().Get("key")
		id := r.URL.Query().Get("id")
		if r.Method == http.MethodPost {
			_ = r.ParseForm()
			if k := r.FormValue("key"); k != "" {
				key = k
			}
			if i := r.FormValue("id"); i != "" {
				id = i
			}
			if d := r.FormValue("device_id"); d != "" {
				deviceID = d
			}
		}

		if key == "" && id == "" {
			http.Error(w, "key or id required", http.StatusBadRequest)
			return
		}

		s.removeNotification(key, id)
		s.SendNotificationDismissToDevice(deviceID, key, id)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "key": key, "id": id})
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
		deviceID := r.URL.Query().Get("device_id")
		var contactsCopy []protocol.ContactItem
		if deviceID != "" && deviceID != "all" {
			if dev := s.getDevice(deviceID); dev != nil {
				s.devicesMu.RLock()
				contactsCopy = make([]protocol.ContactItem, len(dev.Contacts))
				copy(contactsCopy, dev.Contacts)
				s.devicesMu.RUnlock()
			}
		} else {
			s.contactsMu.RLock()
			contactsCopy = make([]protocol.ContactItem, len(s.contacts))
			copy(contactsCopy, s.contacts)
			s.contactsMu.RUnlock()
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"contacts": contactsCopy,
			"count":    len(contactsCopy),
		})
	})

	// Contacts Refresh API (Requests sync from phone)
	mux.HandleFunc("/contacts/refresh", func(w http.ResponseWriter, r *http.Request) {
		deviceID := r.URL.Query().Get("device_id")
		s.RequestContactsSyncFromDevice(deviceID)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"success": true})
	})

	// Send URL / Tab to Phone
	mux.HandleFunc("/url/send_to_phone", func(w http.ResponseWriter, r *http.Request) {
		deviceID := r.URL.Query().Get("device_id")
		rawURL := r.URL.Query().Get("url")
		if r.Method == http.MethodPost {
			_ = r.ParseForm()
			if u := r.FormValue("url"); u != "" {
				rawURL = u
			}
			if d := r.FormValue("device_id"); d != "" {
				deviceID = d
			}
		}
		rawURL = strings.TrimSpace(rawURL)
		if rawURL == "" {
			http.Error(w, "url required", http.StatusBadRequest)
			return
		}
		s.SendOpenUrlToDevice(deviceID, rawURL)
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
		deviceID := r.URL.Query().Get("device_id")
		var photosCopy []protocol.PhotoItem
		if deviceID != "" && deviceID != "all" {
			if dev := s.getDevice(deviceID); dev != nil {
				s.devicesMu.RLock()
				photosCopy = make([]protocol.PhotoItem, len(dev.Photos))
				copy(photosCopy, dev.Photos)
				s.devicesMu.RUnlock()
			}
		} else {
			s.photosMu.RLock()
			photosCopy = make([]protocol.PhotoItem, len(s.photos))
			copy(photosCopy, s.photos)
			s.photosMu.RUnlock()
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"photos": photosCopy,
			"count":  len(photosCopy),
		})
	})

	// Photos Refresh API (Requests photos from phone)
	mux.HandleFunc("/photos/refresh", func(w http.ResponseWriter, r *http.Request) {
		deviceID := r.URL.Query().Get("device_id")
		s.RequestPhotosSyncFromDevice(deviceID)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"success": true})
	})

	// Photos Download API (Requests full photo from phone to be uploaded to Mac)
	mux.HandleFunc("/photos/download", func(w http.ResponseWriter, r *http.Request) {
		deviceID := r.URL.Query().Get("device_id")
		idStr := r.URL.Query().Get("id")
		if r.Method == http.MethodPost {
			_ = r.ParseForm()
			if i := r.FormValue("id"); i != "" {
				idStr = i
			}
			if d := r.FormValue("device_id"); d != "" {
				deviceID = d
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

		s.RequestPhotoDownloadFromDevice(deviceID, id)
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

	// Call Audio Bridge API (Two-way audio relay between PC and phone during call)
	mux.HandleFunc("/call/audio", func(w http.ResponseWriter, r *http.Request) {
		deviceID := r.URL.Query().Get("device_id")
		action := strings.ToUpper(r.URL.Query().Get("action"))
		if r.Method == http.MethodPost {
			_ = r.ParseForm()
			if a := r.FormValue("action"); a != "" {
				action = strings.ToUpper(a)
			}
			if d := r.FormValue("device_id"); d != "" {
				deviceID = d
			}
		}

		if action == "START" {
			s.callAudioMu.Lock()
			s.isCallAudioActive = true
			s.callAudioMu.Unlock()
			s.SendCallAudioBridgeToDevice(deviceID, protocol.CallAudioBridgePayload{Action: "START"})
			log.Printf("[Çağrı Sesi] Ses köprüsü başlatıldı (%s)", deviceID)
		} else if action == "STOP" {
			s.callAudioMu.Lock()
			s.isCallAudioActive = false
			s.callAudioMu.Unlock()
			s.SendCallAudioBridgeToDevice(deviceID, protocol.CallAudioBridgePayload{Action: "STOP"})
			log.Printf("[Çağrı Sesi] Ses köprüsü durduruldu (%s)", deviceID)
		} else if action == "DATA" {
			data := r.URL.Query().Get("data")
			if r.Method == http.MethodPost {
				if d := r.FormValue("data"); d != "" {
					data = d
				}
			}
			if data != "" {
				s.SendCallAudioBridgeToDevice(deviceID, protocol.CallAudioBridgePayload{
					Action: "DATA",
					Data:   data,
				})
			}
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "action": action})
	})

	// Screen Mirror API (Start / Stop)
	mux.HandleFunc("/screen/mirror", func(w http.ResponseWriter, r *http.Request) {
		deviceID := r.URL.Query().Get("device_id")
		action := strings.ToUpper(r.URL.Query().Get("action"))
		if action == "" {
			action = "START"
		}
		quality := 65
		if qStr := r.URL.Query().Get("quality"); qStr != "" {
			_, _ = fmt.Sscanf(qStr, "%d", &quality)
		}
		s.SendScreenMirrorRequestToDevice(deviceID, action, quality)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "action": action})
	})

	// Screen Touch API
	mux.HandleFunc("/screen/touch", func(w http.ResponseWriter, r *http.Request) {
		deviceID := r.URL.Query().Get("device_id")
		action := r.URL.Query().Get("action")
		var x, y float32
		if xStr := r.URL.Query().Get("x"); xStr != "" {
			var x64 float64
			_, _ = fmt.Sscanf(xStr, "%f", &x64)
			x = float32(x64)
		}
		if yStr := r.URL.Query().Get("y"); yStr != "" {
			var y64 float64
			_, _ = fmt.Sscanf(yStr, "%f", &y64)
			y = float32(y64)
		}
		s.SendScreenTouchToDevice(deviceID, action, x, y)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "action": action})
	})

	// Screen Frame API
	mux.HandleFunc("/screen/frame", func(w http.ResponseWriter, r *http.Request) {
		deviceID := r.URL.Query().Get("device_id")
		var frame *protocol.ScreenMirrorFramePayload
		if deviceID != "" && deviceID != "all" {
			if dev := s.getDevice(deviceID); dev != nil {
				frame = dev.LastFrame
			}
		}
		if frame == nil {
			s.mirrorMu.RLock()
			frame = s.lastMirrorFrame
			s.mirrorMu.RUnlock()
		}

		if frame == nil || frame.Data == "" {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"available": false})
			return
		}
		format := r.URL.Query().Get("format")
		if format == "image" || format == "jpeg" {
			data, err := base64.StdEncoding.DecodeString(frame.Data)
			if err == nil {
				w.Header().Set("Content-Type", "image/jpeg")
				w.Header().Set("Cache-Control", "no-cache")
				_, _ = w.Write(data)
				return
			}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"available": true,
			"width":     frame.Width,
			"height":    frame.Height,
		})
	})

	// Storage Mount API (WebDAV for macOS Finder)
	mux.HandleFunc("/storage/mount", func(w http.ResponseWriter, r *http.Request) {
		deviceID := r.URL.Query().Get("device_id")
		action := strings.ToUpper(r.URL.Query().Get("action"))
		if action == "UNMOUNT" || action == "STOP" {
			_ = exec.Command("diskutil", "unmount", "/Volumes/AndroidPhone").Run()
			s.SendStorageMountRequestToDevice(deviceID, "STOP")
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "mounted": false})
			return
		}

		s.SendStorageMountRequestToDevice(deviceID, "START")

		phoneIP := ""
		if deviceID != "" {
			if dev := s.getDevice(deviceID); dev != nil && dev.IP != "" {
				phoneIP = dev.IP
			}
		}
		if phoneIP == "" {
			s.clientsMu.RLock()
			for conn := range s.clients {
				addr := conn.RemoteAddr().String()
				host, _, _ := net.SplitHostPort(addr)
				if host != "" && host != "127.0.0.1" {
					phoneIP = host
					break
				}
			}
			s.clientsMu.RUnlock()
		}
		if phoneIP == "" {
			phoneIP = "192.168.50.118"
		}

		mountURL := fmt.Sprintf("http://%s:8088/", phoneIP)
		_ = os.MkdirAll("/Volumes/AndroidPhone", 0755)
		_ = exec.Command("mount_webdav", "-s", mountURL, "/Volumes/AndroidPhone").Run()
		_ = exec.Command("open", "/Volumes/AndroidPhone").Start()

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"success": true,
			"mounted": true,
			"url":     mountURL,
		})
	})

	// Storage Unmount API
	mux.HandleFunc("/storage/unmount", func(w http.ResponseWriter, r *http.Request) {
		deviceID := r.URL.Query().Get("device_id")
		_ = exec.Command("diskutil", "unmount", "/Volumes/AndroidPhone").Run()
		s.SendStorageMountRequestToDevice(deviceID, "STOP")
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "mounted": false})
	})

	// Hotspot Toggle API
	mux.HandleFunc("/hotspot/toggle", func(w http.ResponseWriter, r *http.Request) {
		deviceID := r.URL.Query().Get("device_id")
		action := strings.ToUpper(r.URL.Query().Get("action"))
		if action == "" {
			s.hotspotMu.RLock()
			st := s.lastHotspotStatus
			s.hotspotMu.RUnlock()
			if st != nil && st.Enabled {
				action = "STOP"
			} else {
				action = "START"
			}
		}
		s.SendHotspotCommandToDevice(deviceID, action)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "action": action})
	})

	// Hotspot Connect API (networksetup for macOS)
	mux.HandleFunc("/hotspot/connect", func(w http.ResponseWriter, r *http.Request) {
		deviceID := r.URL.Query().Get("device_id")
		s.hotspotMu.RLock()
		st := s.lastHotspotStatus
		s.hotspotMu.RUnlock()

		if st == nil || !st.Enabled || st.SSID == "" {
			s.SendHotspotCommandToDevice(deviceID, "START")
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"success": false, "message": "Hotspot başlatılıyor..."})
			return
		}

		go func(ssid, password string) {
			_ = exec.Command("networksetup", "-setairportnetwork", "en0", ssid, password).Run()
		}(st.SSID, st.Password)

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"success":  true,
			"ssid":     st.SSID,
			"password": st.Password,
		})
	})

	// App List API
	mux.HandleFunc("/apps/list", func(w http.ResponseWriter, r *http.Request) {
		deviceID := r.URL.Query().Get("device_id")
		var apps []protocol.InstalledAppInfo
		if deviceID != "" && deviceID != "all" {
			if dev := s.getDevice(deviceID); dev != nil {
				s.devicesMu.RLock()
				apps = make([]protocol.InstalledAppInfo, len(dev.Apps))
				copy(apps, dev.Apps)
				s.devicesMu.RUnlock()
			}
		} else {
			s.appsMu.RLock()
			apps = make([]protocol.InstalledAppInfo, len(s.installedApps))
			copy(apps, s.installedApps)
			s.appsMu.RUnlock()
		}

		if len(apps) == 0 {
			s.SendAppListRequestToDevice(deviceID)
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"success": true,
			"apps":    apps,
			"count":   len(apps),
		})
	})

	// App Launch API
	mux.HandleFunc("/apps/launch", func(w http.ResponseWriter, r *http.Request) {
		deviceID := r.URL.Query().Get("device_id")
		pkg := r.URL.Query().Get("pkg")
		if pkg == "" {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"success": false, "error": "pkg parameter required"})
			return
		}
		s.SendAppLaunchRequestToDevice(deviceID, pkg)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "pkg": pkg})
	})

	// Screen Key Input API
	mux.HandleFunc("/screen/key", func(w http.ResponseWriter, r *http.Request) {
		deviceID := r.URL.Query().Get("device_id")
		codeStr := r.URL.Query().Get("code")
		code, _ := strconv.Atoi(codeStr)
		if code > 0 {
			s.SendScreenKeyToDevice(deviceID, code)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "code": code})
	})

	// Screen Text Input API
	mux.HandleFunc("/screen/text", func(w http.ResponseWriter, r *http.Request) {
		deviceID := r.URL.Query().Get("device_id")
		text := r.URL.Query().Get("text")
		if r.Method == http.MethodPost {
			_ = r.ParseForm()
			if t := r.FormValue("text"); t != "" {
				text = t
			}
			if d := r.FormValue("device_id"); d != "" {
				deviceID = d
			}
		}
		if text != "" {
			s.SendScreenTextToDevice(deviceID, text)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "text": text})
	})

	// Screen Dim Control API (AMOLED Black Power Saving)
	mux.HandleFunc("/screen/dim", func(w http.ResponseWriter, r *http.Request) {
		deviceID := r.URL.Query().Get("device_id")
		enabledStr := r.URL.Query().Get("enabled")
		enabled := enabledStr == "true" || enabledStr == "1"
		s.SendScreenDimToDevice(deviceID, enabled)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "enabled": enabled})
	})

	// Phone Ringer Mode Control API
	mux.HandleFunc("/ringer/set", func(w http.ResponseWriter, r *http.Request) {
		deviceID := r.URL.Query().Get("device_id")
		mode := strings.ToUpper(r.URL.Query().Get("mode"))
		if mode == "" {
			mode = "NORMAL"
		}
		s.SendRingerCommandToDevice(deviceID, mode)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "mode": mode})
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

	dev := s.registerDevice(conn)

	s.clientsMu.Lock()
	s.clients[conn] = true
	s.clientsMu.Unlock()

	log.Printf("[Server] 📱 Android cihaz bağlandı (%s): %s", dev.ID, conn.RemoteAddr())

	welcome, _ := protocol.NewMessage(protocol.EventDeviceInfo, map[string]string{
		"server_name": s.serverName,
		"status":      "connected",
		"device_id":   dev.ID,
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
		s.RequestSmsSyncFromDevice(dev.ID)
		s.RequestContactsSyncFromDevice(dev.ID)
		s.RequestPhotosSyncFromDevice(dev.ID)
		s.SendAppListRequestToDevice(dev.ID)
	}

	defer func() {
		s.unregisterDevice(conn)
		s.clientsMu.Lock()
		delete(s.clients, conn)
		remaining := len(s.clients)
		if remaining == 0 {
			s.lastDeviceInfo = nil
		}
		s.clientsMu.Unlock()
		conn.Close()
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
		s.processMessage(conn, &msg)
	}
}

func (s *SyncServer) processMessage(conn *websocket.Conn, msg *protocol.Message) {
	s.devicesMu.RLock()
	devID := s.connToDevID[conn]
	dev := s.devices[devID]
	s.devicesMu.RUnlock()

	devName := ""
	if dev != nil {
		devName = dev.Name
		dev.LastSeen = time.Now().UnixMilli()
	}

	switch msg.Event {
	case protocol.EventAuthResponse:
		var p protocol.AuthResponsePayload
		if err := json.Unmarshal(msg.Payload, &p); err == nil {
			if p.Status == "AUTHORIZED" {
				s.configMu.Lock()
				s.isPaired = true
				s.configMu.Unlock()
				log.Printf("[Güvenlik] 🔒 Yetkilendirme başarılı! (%s)", p.ClientName)
				if dev != nil {
					s.RequestSmsSyncFromDevice(dev.ID)
					s.RequestContactsSyncFromDevice(dev.ID)
					s.RequestPhotosSyncFromDevice(dev.ID)
					s.SendAppListRequestToDevice(dev.ID)
				}
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
				if dev != nil {
					s.RequestSmsSyncFromDevice(dev.ID)
					s.RequestContactsSyncFromDevice(dev.ID)
					s.RequestPhotosSyncFromDevice(dev.ID)
					s.SendAppListRequestToDevice(dev.ID)
				}
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
			if dev != nil {
				if p.DeviceName != "" {
					dev.Name = p.DeviceName
				}
				if p.Model != "" {
					dev.Model = p.Model
				}
				dev.BatteryLevel = p.BatteryLevel
				dev.IsCharging = p.IsCharging
				dev.LastSeen = time.Now().UnixMilli()
			}
			s.clientsMu.Lock()
			prevDev := s.lastDeviceInfo
			s.lastDeviceInfo = &p
			s.clientsMu.Unlock()
			log.Printf("[Cihaz] 📱 %s (%s) - Pil: %%%d (Şarjda: %v)", p.DeviceName, p.Model, p.BatteryLevel, p.IsCharging)
			// Düşük pil uyarısı
			if p.BatteryLevel <= 20 && !p.IsCharging {
				if prevDev == nil || prevDev.BatteryLevel > 20 || prevDev.IsCharging {
					_ = macos.ShowNotification("⚠️ Düşük Pil Uyarısı", fmt.Sprintf("%s şarjı azaldı: %%%d. Lütfen şarja takın.", p.Model, p.BatteryLevel), "Android Sync", "Glass")
				}
			}
		}

	case protocol.EventNotification, protocol.EventPCNotification:
		var p protocol.NotificationPayload
		if err := json.Unmarshal(msg.Payload, &p); err == nil {
			if dev != nil {
				if p.DeviceID == "" {
					p.DeviceID = dev.ID
				}
				if p.DeviceName == "" {
					p.DeviceName = dev.Name
				}
			}
			log.Printf("[Bildirim] [%s] [%s] %s: %s", p.DeviceName, p.AppName, p.Title, p.Text)
			s.addNotification(p)
			subTitle := p.AppName
			if p.DeviceName != "" {
				subTitle += " (" + p.DeviceName + ")"
			}
			_ = macos.ShowNotification(p.Title, subTitle, p.Text, "Ping")
		}

	case protocol.EventCallState:
		var p protocol.CallStatePayload
		if err := json.Unmarshal(msg.Payload, &p); err == nil {
			if dev != nil {
				p.DeviceID = dev.ID
				p.DeviceName = dev.Name
				dev.CallState = &p
			}
			s.callMu.Lock()
			s.lastCallState = &p
			if p.State == "OFFHOOK" && s.activeCallStart.IsZero() {
				s.activeCallStart = time.Now()
			} else if p.State == "IDLE" {
				s.activeCallStart = time.Time{}
			}
			s.callMu.Unlock()

			log.Printf("[Arama] Durum: %s, Numara: %s, Kişi: %s (Cihaz: %s)", p.State, p.PhoneNumber, p.CallerName, devName)
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
			if dev != nil {
				for i := range p.Messages {
					p.Messages[i].DeviceID = dev.ID
					p.Messages[i].DeviceName = dev.Name
				}
				dev.SmsList = p.Messages
			}
			s.smsMu.Lock()
			s.smsMessages = p.Messages
			s.smsMu.Unlock()
			log.Printf("[SMS] %d adet SMS mesajı telefondan (%s) senkronize edildi", len(p.Messages), devName)
		}

	case protocol.EventSmsNewMessage:
		var msgItem protocol.SmsMessage
		if err := json.Unmarshal(msg.Payload, &msgItem); err == nil {
			if dev != nil {
				msgItem.DeviceID = dev.ID
				msgItem.DeviceName = dev.Name
				dev.SmsList = append([]protocol.SmsMessage{msgItem}, dev.SmsList...)
			}
			s.smsMu.Lock()
			s.smsMessages = append([]protocol.SmsMessage{msgItem}, s.smsMessages...)
			s.smsMu.Unlock()

			senderName := msgItem.ContactName
			if senderName == "" {
				senderName = msgItem.Address
			}
			if devName != "" {
				senderName += " (" + devName + ")"
			}
			log.Printf("[SMS Yeni] [%s]: %s", senderName, msgItem.Body)
			_ = macos.ShowSmsAlert(senderName, msgItem.Body)
		}

	case protocol.EventSmsSentStatus:
		var status protocol.SmsSentStatusPayload
		if err := json.Unmarshal(msg.Payload, &status); err == nil {
			log.Printf("[SMS Durumu] Gönderim (%s): %v -> %s", devName, status.Success, status.Recipient)
		}

	case protocol.EventMediaInfo:
		var p protocol.MediaInfoPayload
		if err := json.Unmarshal(msg.Payload, &p); err == nil {
			p.Source = "phone"
			if dev != nil {
				p.DeviceID = dev.ID
				p.DeviceName = dev.Name
				dev.Media = &p
			}
			s.clientsMu.Lock()
			s.lastPhoneMedia = &p
			s.clientsMu.Unlock()
			log.Printf("[Medya] 📱 Telefondaki Medya (%s): %s - %s", devName, p.Title, p.Artist)
		}

	case protocol.EventMediaCommand:
		var p protocol.MediaCommandPayload
		if err := json.Unmarshal(msg.Payload, &p); err == nil {
			log.Printf("[Medya Komutu] %s (Percent: %.1f)", p.Action, p.Percent)
			if p.Action == "SEEK_PERCENT" {
				_ = macos.ExecuteMediaSeekPercent(p.Percent)
			} else if p.Action == "SEEK_FORWARD" {
				_ = macos.ExecuteMediaSeekRelative(15)
			} else if p.Action == "SEEK_BACKWARD" {
				_ = macos.ExecuteMediaSeekRelative(-15)
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
					log.Printf("[Pano] 🖼 Telefondan (%s) görsel alındı (%d bayt)", devName, len(imgBytes))
					_ = s.clipManager.SetClipboardImage(imgBytes)
					_ = macos.ShowNotification("📋 Pano: Görsel Alındı", fmt.Sprintf("%s cihazından kopyalanan görsel Mac panosuna yazıldı.", devName), "Android Sync", "Glass")
				}
			} else if p.Text != "" {
				log.Printf("[Pano] Telefondan (%s) metin alındı (%d bayt)", devName, len(p.Text))
				_ = s.clipManager.SetClipboard(p.Text)
			}
		}

	case protocol.EventFileUploadNotify:
		var p protocol.FileUploadNotifyPayload
		if err := json.Unmarshal(msg.Payload, &p); err == nil {
			dID := ""
			dName := ""
			if dev != nil {
				dID = dev.ID
				dName = dev.Name
			}
			s.filesMu.Lock()
			s.transferredFiles = append([]TransferredFile{{
				ID:         p.ID,
				FileName:   p.FileName,
				FileSize:   p.FileSize,
				Path:       p.Path,
				Direction:  "incoming",
				Timestamp:  p.Timestamp,
				DeviceID:   dID,
				DeviceName: dName,
			}}, s.transferredFiles...)
			if len(s.transferredFiles) > 100 {
				s.transferredFiles = s.transferredFiles[:100]
			}
			s.filesMu.Unlock()

			sizeMB := float64(p.FileSize) / (1024 * 1024)
			_ = macos.ShowNotification("📁 Dosya Alındı: "+p.FileName, fmt.Sprintf("%.2f MB (%s) - İndirilenler klasörüne kaydedildi.", sizeMB, devName), "Android Sync", "Glass")
			log.Printf("[Dosya] Telefondan (%s) dosya bildirimi alındı: %s (%.2f MB)", devName, p.FileName, sizeMB)
		}

	case protocol.EventRemoteAction:
		var p protocol.RemoteActionPayload
		if err := json.Unmarshal(msg.Payload, &p); err == nil {
			s.executeRemoteAction(p.Action)
		}

	case protocol.EventOpenUrl:
		var p protocol.OpenUrlPayload
		if err := json.Unmarshal(msg.Payload, &p); err == nil && p.URL != "" {
			log.Printf("[Sekme Paylaşımı] Telefondan (%s) bağlantı alındı: %s", devName, p.URL)
			_ = s.openURLInBrowser(p.URL)
			_ = macos.ShowNotification("🌐 Telefondan Bağlantı Açıldı", fmt.Sprintf("%s (%s)", p.URL, devName), "Android Sync", "Glass")
		}

	case protocol.EventContactsResponse:
		var p protocol.ContactsResponsePayload
		if err := json.Unmarshal(msg.Payload, &p); err == nil {
			if dev != nil {
				for i := range p.Contacts {
					p.Contacts[i].DeviceID = dev.ID
					p.Contacts[i].DeviceName = dev.Name
				}
				dev.Contacts = p.Contacts
			}
			s.contactsMu.Lock()
			s.contacts = p.Contacts
			s.contactsMu.Unlock()
			log.Printf("[Rehber] %d adet kişi telefondan (%s) senkronize edildi", len(p.Contacts), devName)
		}

	case protocol.EventPhotosResponse:
		var p protocol.PhotosResponsePayload
		if err := json.Unmarshal(msg.Payload, &p); err == nil {
			if dev != nil {
				for i := range p.Photos {
					p.Photos[i].DeviceID = dev.ID
					p.Photos[i].DeviceName = dev.Name
				}
				dev.Photos = p.Photos
			}
			s.photosMu.Lock()
			s.photos = p.Photos
			s.photosMu.Unlock()
			log.Printf("[Galeri] %d adet fotoğraf telefondan (%s) senkronize edildi", len(p.Photos), devName)
		}

	case protocol.EventNotificationDismiss:
		var p protocol.NotificationDismissPayload
		if err := json.Unmarshal(msg.Payload, &p); err == nil {
			s.removeNotification(p.NotificationKey, p.NotificationID)
			log.Printf("[Bildirim] Telefonda kapatılan bildirim Mac'ten kaldırıldı (%s): key=%s, id=%s", devName, p.NotificationKey, p.NotificationID)
		}

	case protocol.EventTouchpadEvent:
		var p protocol.TouchpadEventPayload
		if err := json.Unmarshal(msg.Payload, &p); err == nil {
			macos.HandleTouchpadEvent(p)
		}

	case protocol.EventBiometricUnlock:
		var p protocol.BiometricUnlockPayload
		if err := json.Unmarshal(msg.Payload, &p); err == nil {
			macos.HandleBiometricUnlock(p)
		}

	case protocol.EventScreenMirrorFrame:
		var p protocol.ScreenMirrorFramePayload
		if err := json.Unmarshal(msg.Payload, &p); err == nil {
			if dev != nil {
				p.DeviceID = dev.ID
				p.DeviceName = dev.Name
				dev.LastFrame = &p
			}
			s.mirrorMu.Lock()
			s.lastMirrorFrame = &p
			s.mirrorMu.Unlock()
		}

	case protocol.EventStorageMountStatus:
		var p protocol.StorageMountStatusPayload
		if err := json.Unmarshal(msg.Payload, &p); err == nil {
			if dev != nil {
				p.DeviceID = dev.ID
				p.DeviceName = dev.Name
				dev.Storage = &p
			}
			s.storageMu.Lock()
			s.lastStorageStatus = &p
			s.storageMu.Unlock()
			log.Printf("[WebDAV] 📁 Telefon depolama durumu (%s): aktif=%v, port=%d, url=%s", devName, p.Enabled, p.Port, p.URL)
		}

	case protocol.EventHotspotStatus:
		var p protocol.HotspotStatusPayload
		if err := json.Unmarshal(msg.Payload, &p); err == nil {
			if dev != nil {
				p.DeviceID = dev.ID
				p.DeviceName = dev.Name
				dev.Hotspot = &p
			}
			s.hotspotMu.Lock()
			s.lastHotspotStatus = &p
			s.hotspotMu.Unlock()
			log.Printf("[Hotspot] 📡 Hotspot durumu (%s): aktif=%v, ssid=%s", devName, p.Enabled, p.SSID)
		}

	case protocol.EventCallAudioBridge:
		var p protocol.CallAudioBridgePayload
		if err := json.Unmarshal(msg.Payload, &p); err == nil {
			s.callAudioMu.Lock()
			if p.Action == "START" {
				s.isCallAudioActive = true
			} else if p.Action == "STOP" {
				s.isCallAudioActive = false
			}
			if p.Data != "" {
				s.lastAudioFrame = p.Data
			}
			s.callAudioMu.Unlock()
		}

	case protocol.EventAppListResponse:
		var p protocol.AppListResponsePayload
		if err := json.Unmarshal(msg.Payload, &p); err == nil {
			if dev != nil {
				for i := range p.Apps {
					p.Apps[i].DeviceID = dev.ID
					p.Apps[i].DeviceName = dev.Name
				}
				dev.Apps = p.Apps
			}
			s.appsMu.Lock()
			s.installedApps = p.Apps
			s.appsMu.Unlock()
			log.Printf("[Uygulamalar] 📱 %d adet uygulama telefondan (%s) senkronize edildi", len(p.Apps), devName)
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
	return exec.Command("open", u).Start()
}

func (s *SyncServer) SendOpenUrlToPhone(url string) {
	s.SendOpenUrlToDevice("", url)
}

func (s *SyncServer) SendOpenUrlToDevice(deviceID, url string) {
	msg, err := protocol.NewMessage(protocol.EventOpenUrl, protocol.OpenUrlPayload{
		URL:            url,
		Sender:         s.serverName,
		TargetDeviceID: deviceID,
	})
	if err == nil {
		s.SendToDevice(deviceID, msg)
		log.Printf("[Sekme Paylaşımı] URL telefona gönderildi (%s): %s", deviceID, url)
	}
}

func (s *SyncServer) RequestContactsSync() {
	s.RequestContactsSyncFromDevice("")
}

func (s *SyncServer) RequestContactsSyncFromDevice(deviceID string) {
	msg, err := protocol.NewMessage(protocol.EventContactsRequest, map[string]any{"target_device_id": deviceID})
	if err == nil {
		s.SendToDevice(deviceID, msg)
		log.Printf("[Rehber] Telefon rehberi senkronizasyon isteği gönderildi (%s)", deviceID)
	}
}

func (s *SyncServer) RequestPhotosSync() {
	s.RequestPhotosSyncFromDevice("")
}

func (s *SyncServer) RequestPhotosSyncFromDevice(deviceID string) {
	msg, err := protocol.NewMessage(protocol.EventPhotosRequest, map[string]any{"target_device_id": deviceID})
	if err == nil {
		s.SendToDevice(deviceID, msg)
		log.Printf("[Galeri] Fotoğraf senkronizasyon isteği gönderildi (%s)", deviceID)
	}
}

func (s *SyncServer) RequestPhotoDownload(photoID int64) {
	s.RequestPhotoDownloadFromDevice("", photoID)
}

func (s *SyncServer) RequestPhotoDownloadFromDevice(deviceID string, photoID int64) {
	msg, err := protocol.NewMessage(protocol.EventPhotoDownloadRequest, protocol.PhotoDownloadRequestPayload{
		ID:             photoID,
		TargetDeviceID: deviceID,
	})
	if err == nil {
		s.SendToDevice(deviceID, msg)
		log.Printf("[Galeri] Fotoğraf indirme isteği gönderildi (%s): id=%d", deviceID, photoID)
	}
}

func (s *SyncServer) SendScreenMirrorRequest(action string, quality int) {
	s.SendScreenMirrorRequestToDevice("", action, quality)
}

func (s *SyncServer) SendScreenMirrorRequestToDevice(deviceID, action string, quality int) {
	msg, err := protocol.NewMessage(protocol.EventScreenMirrorRequest, protocol.ScreenMirrorRequestPayload{
		Action:         action,
		Quality:        quality,
		TargetDeviceID: deviceID,
	})
	if err == nil {
		s.SendToDevice(deviceID, msg)
		log.Printf("[Ekran Yansıtma] İstek gönderildi (%s): action=%s, quality=%d", deviceID, action, quality)
	}
}

func (s *SyncServer) SendScreenTouch(action string, x, y float32) {
	s.SendScreenTouchToDevice("", action, x, y)
}

func (s *SyncServer) SendScreenTouchToDevice(deviceID, action string, x, y float32) {
	msg, err := protocol.NewMessage(protocol.EventScreenTouch, protocol.ScreenTouchPayload{
		Action:         action,
		X:              x,
		Y:              y,
		TargetDeviceID: deviceID,
	})
	if err == nil {
		s.SendToDevice(deviceID, msg)
	}
}

func (s *SyncServer) SendStorageMountRequest(action string) {
	s.SendStorageMountRequestToDevice("", action)
}

func (s *SyncServer) SendStorageMountRequestToDevice(deviceID, action string) {
	msg, err := protocol.NewMessage(protocol.EventStorageMountRequest, protocol.StorageMountRequestPayload{
		Action:         action,
		TargetDeviceID: deviceID,
	})
	if err == nil {
		s.SendToDevice(deviceID, msg)
		log.Printf("[WebDAV] Depolama isteği gönderildi (%s): action=%s", deviceID, action)
	}
}

func (s *SyncServer) SendHotspotCommand(action string) {
	s.SendHotspotCommandToDevice("", action)
}

func (s *SyncServer) SendHotspotCommandToDevice(deviceID, action string) {
	msg, err := protocol.NewMessage(protocol.EventHotspotCommand, protocol.HotspotCommandPayload{
		Action:         action,
		TargetDeviceID: deviceID,
	})
	if err == nil {
		s.SendToDevice(deviceID, msg)
		log.Printf("[Hotspot] Komut gönderildi (%s): action=%s", deviceID, action)
	}
}

func (s *SyncServer) SendCallAudioBridge(payload protocol.CallAudioBridgePayload) {
	s.SendCallAudioBridgeToDevice("", payload)
}

func (s *SyncServer) SendCallAudioBridgeToDevice(deviceID string, payload protocol.CallAudioBridgePayload) {
	payload.TargetDeviceID = deviceID
	msg, err := protocol.NewMessage(protocol.EventCallAudioBridge, payload)
	if err == nil {
		s.SendToDevice(deviceID, msg)
	}
}

func (s *SyncServer) SendAppListRequest() {
	s.SendAppListRequestToDevice("")
}

func (s *SyncServer) SendAppListRequestToDevice(deviceID string) {
	msg, err := protocol.NewMessage(protocol.EventAppListRequest, map[string]string{"target_device_id": deviceID})
	if err == nil {
		s.SendToDevice(deviceID, msg)
		log.Printf("[Uygulamalar] Uygulama listesi isteği gönderildi (%s)", deviceID)
	}
}

func (s *SyncServer) SendAppLaunchRequest(packageName string) {
	s.SendAppLaunchRequestToDevice("", packageName)
}

func (s *SyncServer) SendAppLaunchRequestToDevice(deviceID, packageName string) {
	msg, err := protocol.NewMessage(protocol.EventAppLaunchRequest, protocol.AppLaunchRequestPayload{
		PackageName:    packageName,
		TargetDeviceID: deviceID,
	})
	if err == nil {
		s.SendToDevice(deviceID, msg)
		log.Printf("[Uygulamalar] Uygulama başlatma isteği gönderildi (%s): %s", deviceID, packageName)
	}
}

func (s *SyncServer) SendScreenKey(keyCode int) {
	s.SendScreenKeyToDevice("", keyCode)
}

func (s *SyncServer) SendScreenKeyToDevice(deviceID string, keyCode int) {
	msg, err := protocol.NewMessage(protocol.EventScreenKey, protocol.ScreenKeyPayload{
		KeyCode:        keyCode,
		TargetDeviceID: deviceID,
	})
	if err == nil {
		s.SendToDevice(deviceID, msg)
	}
}

func (s *SyncServer) SendScreenText(text string) {
	s.SendScreenTextToDevice("", text)
}

func (s *SyncServer) SendScreenTextToDevice(deviceID, text string) {
	msg, err := protocol.NewMessage(protocol.EventScreenText, protocol.ScreenTextPayload{
		Text:           text,
		TargetDeviceID: deviceID,
	})
	if err == nil {
		s.SendToDevice(deviceID, msg)
	}
}

func (s *SyncServer) SendScreenDim(enabled bool) {
	s.SendScreenDimToDevice("", enabled)
}

func (s *SyncServer) SendScreenDimToDevice(deviceID string, enabled bool) {
	msg, err := protocol.NewMessage(protocol.EventScreenDim, protocol.ScreenDimPayload{
		Enabled:        enabled,
		TargetDeviceID: deviceID,
	})
	if err == nil {
		s.SendToDevice(deviceID, msg)
		log.Printf("[Ekran] Ekran karartma isteği gönderildi (%s): enabled=%v", deviceID, enabled)
	}
}

func (s *SyncServer) SendRingerCommand(mode string) {
	s.SendRingerCommandToDevice("", mode)
}

func (s *SyncServer) SendRingerCommandToDevice(deviceID, mode string) {
	msg, err := protocol.NewMessage(protocol.EventRingerCommand, protocol.RingerCommandPayload{
		Mode:           mode,
		TargetDeviceID: deviceID,
	})
	if err == nil {
		s.SendToDevice(deviceID, msg)
		log.Printf("[Zil Sesi] Zil sesi komutu gönderildi (%s): %s", deviceID, mode)
	}
}

func (s *SyncServer) SendNotificationReply(key string, actionIndex int, text string) {
	s.SendNotificationReplyToDevice("", key, actionIndex, text)
}

func (s *SyncServer) SendNotificationReplyToDevice(deviceID, key string, actionIndex int, text string) {
	payload := protocol.NotificationReplyPayload{
		NotificationKey: key,
		ActionIndex:     actionIndex,
		ReplyText:       text,
		TargetDeviceID:  deviceID,
	}
	msg, err := protocol.NewMessage(protocol.EventNotificationReply, payload)
	if err == nil {
		s.SendToDevice(deviceID, msg)
		log.Printf("[Bildirim Yanıtı] Yanıt iletildi (%s, %s): %s", deviceID, key, text)
	}
}

func (s *SyncServer) SendNotificationAction(key string, actionIndex int) {
	s.SendNotificationActionToDevice("", key, actionIndex)
}

func (s *SyncServer) SendNotificationActionToDevice(deviceID, key string, actionIndex int) {
	payload := protocol.NotificationActionPayload{
		NotificationKey: key,
		ActionIndex:     actionIndex,
		TargetDeviceID:  deviceID,
	}
	msg, err := protocol.NewMessage(protocol.EventNotificationAction, payload)
	if err == nil {
		s.SendToDevice(deviceID, msg)
		log.Printf("[Bildirim Eylemi] Eylem isteği gönderildi (%s, %s, index: %d)", deviceID, key, actionIndex)
	}
}

func (s *SyncServer) SendNotificationDismiss(key, id string) {
	s.SendNotificationDismissToDevice("", key, id)
}

func (s *SyncServer) SendNotificationDismissToDevice(deviceID, key, id string) {
	payload := protocol.NotificationDismissPayload{
		NotificationKey: key,
		NotificationID:  id,
		TargetDeviceID:  deviceID,
	}
	msg, err := protocol.NewMessage(protocol.EventNotificationDismiss, payload)
	if err == nil {
		s.SendToDevice(deviceID, msg)
		log.Printf("[Bildirim Kapatma] Kapatma isteği gönderildi (%s): key=%s, id=%s", deviceID, key, id)
	}
}

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
	s.SendCallActionToDevice("", action, number, val)
}

func (s *SyncServer) SendCallActionToDevice(deviceID, action string, number *string, val *bool) {
	msg, err := protocol.NewMessage(protocol.EventCallAction, protocol.CallActionPayload{
		Action:         action,
		Number:         number,
		Value:          val,
		TargetDeviceID: deviceID,
	})
	if err == nil {
		s.SendToDevice(deviceID, msg)
	}
}

func (s *SyncServer) SendSms(recipient, body string) {
	s.SendSmsToDevice("", recipient, body)
}

func (s *SyncServer) SendSmsToDevice(deviceID, recipient, body string) {
	msg, err := protocol.NewMessage(protocol.EventSmsSend, protocol.SmsSendPayload{
		Recipient:      recipient,
		Body:           body,
		TargetDeviceID: deviceID,
	})
	if err == nil {
		s.SendToDevice(deviceID, msg)
	}
}

func (s *SyncServer) RequestSmsSync() {
	s.RequestSmsSyncFromDevice("")
}

func (s *SyncServer) RequestSmsSyncFromDevice(deviceID string) {
	msg, err := protocol.NewMessage(protocol.EventSmsSyncRequest, map[string]any{
		"limit":            100,
		"target_device_id": deviceID,
	})
	if err == nil {
		s.SendToDevice(deviceID, msg)
	}
}

func (s *SyncServer) SendPhoneCommand(action string, percent ...float64) {
	s.SendPhoneCommandToDevice("", action, percent...)
}

func (s *SyncServer) SendPhoneCommandToDevice(deviceID, action string, percent ...float64) {
	pct := 0.0
	if len(percent) > 0 {
		pct = percent[0]
	}
	msg, err := protocol.NewMessage(protocol.EventPhoneCommand, protocol.PhoneCommandPayload{
		Action:         action,
		Percent:        pct,
		TargetDeviceID: deviceID,
	})
	if err == nil {
		s.SendToDevice(deviceID, msg)
	}
}

func (s *SyncServer) SendPhoneCommandWithPercent(action string, percent float64) {
	s.SendPhoneCommandToDevice("", action, percent)
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

        /* Multi-Device UI Styles */
        .device-select {
            background: var(--bg-input);
            border: 1px solid var(--border-glow);
            color: var(--accent-blue);
            font-weight: 700;
            border-radius: var(--radius-sm);
            padding: 6px 12px;
            font-size: 13px;
            outline: none;
            cursor: pointer;
            transition: all 0.2s ease;
            box-shadow: 0 2px 8px rgba(0,0,0,0.3);
        }
        .device-select:hover, .device-select:focus {
            border-color: #38BDF8;
            box-shadow: 0 0 12px var(--glow-cyan);
        }
        .device-select option {
            background: #0d111b;
            color: #F8FAFC;
        }
        .global-dev-select-wrap {
            display: flex;
            align-items: center;
            gap: 8px;
            background: rgba(56, 189, 248, 0.08);
            border: 1px solid rgba(56, 189, 248, 0.25);
            padding: 4px 10px;
            border-radius: var(--radius-md);
        }
        .device-tag {
            display: inline-flex;
            align-items: center;
            gap: 4px;
            font-size: 10px;
            font-weight: 700;
            padding: 2px 8px;
            border-radius: 99px;
            background: rgba(56, 189, 248, 0.15);
            color: var(--accent-blue);
            border: 1px solid rgba(56, 189, 248, 0.3);
            white-space: nowrap;
        }
        .device-tag.device-pc {
            background: rgba(99, 102, 241, 0.15);
            color: #818CF8;
            border-color: rgba(99, 102, 241, 0.3);
        }
        .device-filter-bar {
            display: flex;
            gap: 6px;
            overflow-x: auto;
            padding-bottom: 4px;
        }
        .device-filter-pill {
            font-size: 11px;
            font-weight: 700;
            padding: 4px 10px;
            border-radius: 99px;
            border: 1px solid var(--border-card);
            background: rgba(255, 255, 255, 0.04);
            color: var(--text-secondary);
            cursor: pointer;
            white-space: nowrap;
            transition: all 0.2s;
        }
        .device-filter-pill:hover {
            color: var(--text-primary);
            background: rgba(255, 255, 255, 0.08);
        }
        .device-filter-pill.active {
            background: linear-gradient(135deg, #0284C7, #2563EB);
            color: white;
            border-color: transparent;
            box-shadow: 0 2px 8px rgba(2, 132, 199, 0.4);
        }
        .device-card-item {
            background: rgba(18, 24, 38, 0.72);
            border: 1px solid var(--border-card);
            border-radius: var(--radius-lg);
            padding: 20px;
            transition: all 0.25s ease;
            position: relative;
        }
        .device-card-item:hover {
            border-color: var(--border-glow);
            transform: translateY(-2px);
            box-shadow: 0 8px 24px rgba(0,0,0,0.4);
        }
        .device-card-item.selected {
            border-color: var(--accent-blue);
            box-shadow: 0 0 20px var(--glow-cyan);
        }

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
                <li class="nav-item" onclick="switchTab('screen')">
                    <span>📱</span> <span>Ekran Yansıtma</span>
                    <span class="nav-badge" id="screenNavBadge" style="display:none;">Canlı</span>
                </li>
                <li class="nav-item" onclick="switchTab('network')">
                    <span>⚡</span> <span>Ağ &amp; Hotspot</span>
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
                    <div class="global-dev-select-wrap" title="Hedef Cihaz Seçimi">
                        <span style="font-size:12px; font-weight:700; color:var(--accent-blue);">🎯 Hedef:</span>
                        <select id="globalDeviceSelector" class="device-select" onchange="onGlobalDeviceChange(this.value)">
                            <option value="all">🌐 Tüm Cihazlar</option>
                        </select>
                    </div>
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
                <div id="incomingCallerDevice" style="font-size:11px; color:var(--accent-blue); font-weight:700; margin-bottom:4px;"></div>
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
                    <!-- Connected Devices Dynamic Grid -->
                    <div class="col-4" style="display:flex; flex-direction:column; gap:16px;">
                        <div style="display:flex; justify-content:space-between; align-items:center;">
                            <h3 style="font-size:16px; font-weight:700;">📱 Bağlı Telefonlar</h3>
                            <span id="overviewDevCount" class="nav-badge" style="font-size:11px;">0 Cihaz</span>
                        </div>
                        <div id="overviewDevicesGrid" style="display:flex; flex-direction:column; gap:14px;">
                            <div class="card" style="padding:20px; text-align:center; color:var(--text-muted);">
                                Bağlantı aranıyor...
                            </div>
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
                        <h3 style="font-size:18px; font-weight:700; margin-bottom:14px;">📞 Hızlı Arama &amp; Tuş Takımı</h3>
                        <div style="display:flex; align-items:center; gap:10px; margin-bottom:12px;">
                            <span style="font-size:12px; font-weight:700; color:var(--text-secondary);">Arama Yapılacak Cihaz:</span>
                            <select id="callDeviceSelector" class="device-select" style="flex:1;">
                                <option value="all">Otomatik (Varsayılan)</option>
                            </select>
                        </div>
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
                        <div id="smsDeviceFilterTabs" class="device-filter-bar" style="padding: 0 16px 10px 16px;">
                            <button class="device-filter-pill active" onclick="filterSmsByDevice('all', this)">🌐 Tümü</button>
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
                            <select id="smsSendDeviceSelector" class="device-select" style="max-width:140px; font-size:11px;" title="SMS Gönderilecek Hat / Telefon">
                                <option value="all">Otomatik</option>
                            </select>
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

                    <!-- Multi-Device Phone Media Container -->
                    <div id="multiDevicePhoneMediaContainer" class="col-6" style="display:flex; flex-direction:column; gap:16px;">
                        <div class="card" style="padding:20px; text-align:center; color:var(--text-muted);">
                            Telefonda çalan medya bekleniyor...
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
                        <div style="display:flex; align-items:center; gap:10px; margin-bottom:12px;">
                            <span style="font-size:12px; font-weight:700; color:var(--accent-blue);">Hedef Cihaz:</span>
                            <select id="clipDeviceSelector" class="device-select" style="max-width:260px;">
                                <option value="all">🌐 Tüm Cihazlar (Mesh Pano)</option>
                            </select>
                        </div>
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
                    <div style="display:flex; justify-content:space-between; align-items:center; margin-bottom:16px; flex-wrap:wrap; gap:12px;">
                        <div style="display:flex; align-items:center; gap:12px;">
                            <h3 style="font-size:18px; font-weight:700;">🔔 Canlı Bildirim Akışı</h3>
                            <span id="notifBadgeFull" style="font-size:12px; color:var(--text-muted);">0 Bildirim</span>
                        </div>
                        <div id="notifDeviceFilterContainer" class="device-filter-bar">
                            <button class="device-filter-pill active" onclick="filterNotifsByDevice('all', this)">🌐 Tümü</button>
                        </div>
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
                        <div style="display:flex; justify-content:space-between; align-items:center; margin-bottom:16px; flex-wrap:wrap; gap:12px;">
                            <h3 style="font-size:18px; font-weight:700;">📁 Wi-Fi Dosya Gönderimi (PC ➡️ Telefon)</h3>
                            <div style="display:flex; align-items:center; gap:10px; flex-wrap:wrap;">
                                <div style="display:flex; align-items:center; gap:8px;">
                                    <span style="font-size:12px; font-weight:700; color:var(--accent-blue);">Hedef Cihaz:</span>
                                    <select id="fileDeviceSelector" class="device-select" style="max-width:240px;">
                                        <option value="all">🌐 Tüm Bağlı Telefonlar</option>
                                    </select>
                                </div>
                                <button class="btn btn-secondary" onclick="openDownloadsFolder()">📂 İndirilenler Klasörünü Aç</button>
                            </div>
                        </div>
                        <div id="dropZone" style="border: 2px dashed rgba(56, 189, 248, 0.4); border-radius: var(--radius-lg); padding: 40px 20px; text-align: center; background: rgba(56, 189, 248, 0.04); cursor: pointer; transition: all 0.3s ease;">
                            <div style="font-size: 40px; margin-bottom: 12px;">📤</div>
                            <div style="font-size: 15px; font-weight: 700; color: var(--text-primary); margin-bottom: 6px;">Dosyaları buraya sürükleyip bırakın veya tıklayın</div>
                            <div style="font-size: 12px; color: var(--text-secondary);">Fotoğraflar, videolar, belgeler ve APK'lar doğrudan seçili telefonun Downloads klasörüne aktarılır</div>
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
                                        <th style="padding:10px 14px;">Cihaz</th>
                                        <th style="padding:10px 14px;">Dosya Adı</th>
                                        <th style="padding:10px 14px;">Boyut</th>
                                        <th style="padding:10px 14px;">Tarih</th>
                                        <th style="padding:10px 14px; text-align:right;">İşlem</th>
                                    </tr>
                                </thead>
                                <tbody id="filesTableBody">
                                    <tr><td colspan="6" style="padding:24px; text-align:center; color:var(--text-muted);">Henüz aktarılmış dosya yok</td></tr>
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
                            <div style="display: flex; gap: 10px; align-items: center; flex-wrap: wrap;">
                                <select id="contactsDeviceSelector" class="device-select" onchange="onContactsDeviceChange(this.value)">
                                    <option value="all">🌐 Tüm Cihazların Rehberi</option>
                                </select>
                                <span class="nav-badge" id="contactsHeaderBadge" style="font-size: 13px; padding: 6px 14px;">0 Kişi</span>
                                <button class="btn btn-secondary" onclick="refreshContacts()" style="font-size: 13px; padding: 8px 16px;">🔄 Telefondan Yenile</button>
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
                            <div style="display: flex; gap: 10px; flex-wrap: wrap; align-items:center;">
                                <select id="tabDeviceSelector" class="device-select" style="min-width:180px;">
                                    <option value="all">🌐 Tüm Telefonlarda Aç</option>
                                </select>
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
                            <div style="display: flex; gap: 10px; align-items: center; flex-wrap: wrap;">
                                <select id="photosDeviceSelector" class="device-select" onchange="onPhotosDeviceChange(this.value)">
                                    <option value="all">🌐 Tüm Telefonlar</option>
                                </select>
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

            <!-- TAB 11: Canlı Ekran Yansıtma & Uzaktan Kontrol -->
            <div id="tab-screen" class="tab-content">
                <div style="display:flex; gap: 24px; min-height: calc(100vh - 140px);">
                    <!-- Sol: Telefon Canlı Ekranı -->
                    <div class="card" style="flex: 1; display:flex; flex-direction:column; align-items:center; justify-content:center; background: rgba(10, 14, 23, 0.95); position: relative; border-radius: var(--radius-xl); overflow: hidden; padding: 20px;">
                        <div style="display:flex; align-items:center; gap:10px; margin-bottom:14px; width:340px; justify-content:space-between;">
                            <span style="font-size:13px; font-weight:700; color:var(--accent-blue);">Yansıtılan Cihaz:</span>
                            <select id="screenDeviceSelector" class="device-select" onchange="onScreenDeviceChange(this.value)" style="flex:1;">
                                <option value="">Cihaz Seçin...</option>
                            </select>
                        </div>
                        <div id="phoneScreenContainer" tabindex="0" style="width: 340px; height: 620px; background: #000; border: 4px solid #334155; border-radius: 36px; overflow: hidden; position: relative; display:flex; flex-direction:column; box-shadow: 0 25px 60px rgba(0,0,0,0.8), 0 0 25px rgba(56, 189, 248, 0.2); outline: none;">
                            <!-- Kamera Çentiği -->
                            <div style="position: absolute; top: 8px; left: 50%; transform: translateX(-50%); width: 70px; height: 16px; background: #1e293b; border-radius: 99px; z-index: 20; display:flex; align-items:center; justify-content:center;">
                                <div style="width: 8px; height: 8px; background: #0f172a; border-radius: 50%;"></div>
                            </div>

                            <!-- Canlı Klavye Rozeti -->
                            <div id="keyboardActiveBadge" style="display:none; position:absolute; bottom: 56px; left: 50%; transform: translateX(-50%); background: rgba(14, 165, 233, 0.95); color: #fff; font-size: 11px; font-weight: 700; padding: 4px 12px; border-radius: 99px; z-index: 30; pointer-events: none; box-shadow: 0 4px 12px rgba(0,0,0,0.5); backdrop-filter: blur(4px);">⌨️ Canlı Klavye Aktif</div>

                            <!-- Dosya Sürükle-Bırak Katmanı -->
                            <div id="screenDropOverlay" style="display:none; position:absolute; inset:0; background:rgba(2, 132, 199, 0.88); z-index:50; flex-direction:column; align-items:center; justify-content:center; color:#fff; text-align:center; padding:20px; pointer-events:none; backdrop-filter: blur(4px);">
                                <div style="font-size:44px; margin-bottom:8px;">📥</div>
                                <div style="font-size:16px; font-weight:800;">Dosyayı Telefona Gönder</div>
                                <div style="font-size:12px; opacity:0.9; margin-top:4px;">APK, fotoğraf veya dosyaları buraya bırakın</div>
                            </div>
                            
                            <!-- Canlı Ekran Alanı -->
                            <div id="screenViewWrapper" style="flex: 1; width: 100%; height: 100%; position: relative; cursor: crosshair; user-select: none;">
                                <img id="screenMirrorImg" src="" alt="Ekran Kapalı" style="width: 100%; height: 100%; object-fit: contain; display: none;">
                                <div id="screenMirrorPlaceholder" style="width: 100%; height: 100%; display: flex; flex-direction: column; align-items: center; justify-content: center; gap: 14px; color: var(--text-muted); text-align: center; padding: 30px;">
                                    <span style="font-size: 48px;">📱</span>
                                    <div style="font-size: 15px; font-weight: 700; color: var(--text-primary);">Ekran Yansıtma Beklemede</div>
                                    <div style="font-size: 12px; color: var(--text-secondary);">Telefon ekranını bilgisayarınızdan canlı izlemek ve farenizle dokunarak kontrol etmek için yansıtmayı başlatın.</div>
                                    <button class="btn btn-primary" onclick="toggleScreenMirror(true)" style="margin-top: 10px;">▶️ Yansıtmayı Başlat</button>
                                </div>
                            </div>

                            <!-- Sanal Telefon Alt Tuşları -->
                            <div style="height: 48px; background: rgba(15, 23, 42, 0.95); border-top: 1px solid rgba(255,255,255,0.08); display: flex; justify-content: space-around; align-items: center; z-index: 10;">
                                <button class="btn btn-secondary" onclick="sendScreenTouchAction('back')" title="Geri" style="padding: 6px 16px; border:none; background:transparent; font-size:16px;">◀</button>
                                <button class="btn btn-secondary" onclick="sendScreenTouchAction('home')" title="Ana Ekran" style="padding: 6px 16px; border:none; background:transparent; font-size:16px;">⌂</button>
                                <button class="btn btn-secondary" onclick="sendScreenTouchAction('recents')" title="Son Uygulamalar" style="padding: 6px 16px; border:none; background:transparent; font-size:16px;">▢</button>
                            </div>
                        </div>
                    </div>

                    <!-- Sağ: Kontroller ve Panel -->
                    <div style="width: 380px; display: flex; flex-direction: column; gap: 16px;">
                        <div class="card">
                            <h3 style="font-size: 16px; font-weight: 800; margin-bottom: 12px;">🎮 Canlı Kontrol &amp; Akış</h3>
                            <div style="font-size: 13px; color: var(--text-secondary); margin-bottom: 16px;">
                                Ekran üzerindeki herhangi bir noktaya tıklayarak veya sürükleyerek telefonunuza dokunma hareketi gönderebilirsiniz. <strong>Canlı Klavye:</strong> Ekrana tıkladıktan sonra klavyenizle yazabilirsiniz.
                            </div>
                            
                            <div style="display:flex; flex-direction:column; gap:10px;">
                                <button class="btn btn-primary" id="btnMirrorStart" onclick="toggleScreenMirror(true)">▶️ Canlı Yansıtmayı Başlat</button>
                                <button class="btn btn-danger" id="btnMirrorStop" onclick="toggleScreenMirror(false)" style="display:none;">⏹️ Yansıtmayı Durdur</button>
                            </div>

                            <div style="margin-top: 20px; padding-top: 16px; border-top: 1px solid var(--border-card);">
                                <div style="font-size: 12px; font-weight: 700; color: var(--text-muted); margin-bottom: 8px;">AKIM KALİTESİ</div>
                                <div style="display:flex; gap:8px;">
                                    <button class="btn btn-secondary" id="btnQual60" onclick="setMirrorQuality(60)" style="flex:1; font-size:12px;">Hızlı (60p)</button>
                                    <button class="btn btn-primary" id="btnQual75" onclick="setMirrorQuality(75)" style="flex:1; font-size:12px;">Dengeli (75p)</button>
                                    <button class="btn btn-secondary" id="btnQual90" onclick="setMirrorQuality(90)" style="flex:1; font-size:12px;">HD (90p)</button>
                                </div>
                            </div>
                        </div>

                        <div class="card">
                            <h3 style="font-size: 15px; font-weight: 800; margin-bottom: 12px;">⚡ Hızlı Eylemler &amp; Profil</h3>
                            <div style="display:grid; grid-template-columns: 1fr 1fr; gap: 8px;">
                                <button class="btn btn-secondary" onclick="sendScreenTouchAction('power')" style="font-size:12px;">🔒 Ekran Kilidi</button>
                                <button class="btn btn-secondary" onclick="sendScreenTouchAction('notifications')" style="font-size:12px;">⚡ Bildirimler</button>
                                <button class="btn btn-secondary" id="btnDimScreen" onclick="toggleScreenDim()" style="font-size:12px;">🌙 Ekranı Karart</button>
                                <button class="btn btn-secondary" onclick="setPhoneRinger('NORMAL')" style="font-size:12px;">🔔 Zil Açık</button>
                                <button class="btn btn-secondary" onclick="setPhoneRinger('VIBRATE')" style="font-size:12px;">📳 Titreşim</button>
                                <button class="btn btn-secondary" onclick="setPhoneRinger('SILENT')" style="font-size:12px;">🔕 Sessiz Mod</button>
                            </div>
                        </div>

                        <div class="card" style="margin-top: 16px;">
                            <div style="display:flex; justify-content:space-between; align-items:center; margin-bottom: 12px;">
                                <h3 style="font-size: 15px; font-weight: 800;">📱 Telefon Uygulamaları (App Streaming)</h3>
                                <button class="btn btn-secondary" onclick="loadPhoneApps()" style="font-size:11px; padding: 4px 10px;">🔄 Yenile</button>
                            </div>
                            <input type="text" id="appSearchInput" placeholder="🔍 Uygulama ara (Instagram, WhatsApp vb.)..." oninput="filterApps()" style="width: 100%; padding: 8px 12px; border-radius: var(--radius-sm); border: 1px solid var(--border-card); background: rgba(0,0,0,0.3); color: #fff; font-size: 12px; margin-bottom: 10px;">
                            <div id="phoneAppsList" style="display:grid; grid-template-columns: 1fr 1fr; gap: 8px; max-height: 200px; overflow-y: auto; padding-right: 4px;">
                                <div style="grid-column: 1 / -1; text-align: center; color: var(--text-muted); font-size: 12px; padding: 12px;">
                                    Uygulamaları listelemek için "Yenile"ye tıklayın.
                                </div>
                            </div>
                        </div>
                    </div>
                </div>
            </div>

            <!-- TAB 12: Ağ & Donanım (WebDAV, Hotspot, Sesli Arama) -->
            <div id="tab-network" class="tab-content">
                <div style="margin-bottom: 20px; display:flex; align-items:center; gap:12px; background:rgba(0,0,0,0.3); padding:14px 20px; border-radius:var(--radius-md); border:1px solid var(--border-card); flex-wrap:wrap;">
                    <span style="font-size:13px; font-weight:800; color:var(--accent-blue);">🎯 Yönetilecek Cihaz:</span>
                    <select id="networkDeviceSelector" class="device-select" onchange="onNetworkDeviceChange(this.value)" style="max-width:280px;">
                        <option value="all">Otomatik (Varsayılan Cihaz)</option>
                    </select>
                    <span style="font-size:12px; color:var(--text-secondary);">WebDAV sürücüsü ve Hotspot komutları bu cihaza yönlendirilir.</span>
                </div>
                <div style="display:grid; grid-template-columns: repeat(auto-fit, minmax(360px, 1fr)); gap: 24px;">
                    
                    <!-- KART 1: WebDAV Ağ Sürücüsü (Z:\) -->
                    <div class="card">
                        <div style="display:flex; align-items:center; gap: 14px; margin-bottom: 16px;">
                            <div style="width: 48px; height: 48px; border-radius: 12px; background: rgba(56, 189, 248, 0.15); display:flex; align-items:center; justify-content:center; font-size: 24px;">📁</div>
                            <div>
                                <h3 style="font-size: 16px; font-weight: 800;">Telefon Ağ Sürücüsü (Z:\)</h3>
                                <p style="font-size: 12px; color: var(--text-muted);">WebDAV ile Windows Gezgini'ne doğrudan bağlama</p>
                            </div>
                        </div>

                        <div style="background: rgba(0,0,0,0.3); border-radius: var(--radius-md); padding: 14px; margin-bottom: 16px; font-size: 13px;">
                            <div style="display:flex; justify-content:space-between; margin-bottom: 6px;">
                                <span style="color:var(--text-secondary);">Sürücü Harfi:</span>
                                <strong style="color:var(--accent-blue);">Z:\ Sürücüsü</strong>
                            </div>
                            <div style="display:flex; justify-content:space-between; margin-bottom: 6px;">
                                <span style="color:var(--text-secondary);">Protokol:</span>
                                <span>WebDAV (Port 8088)</span>
                            </div>
                            <div style="display:flex; justify-content:space-between;">
                                <span style="color:var(--text-secondary);">Durum:</span>
                                <span id="storageMountStatusText" style="color:var(--accent-green); font-weight:700;">Hazır</span>
                            </div>
                        </div>

                        <div style="display:flex; flex-direction:column; gap: 10px;">
                            <button class="btn btn-primary" onclick="mountStorageDrive('Z:')">⚡ Z:\ Olarak Windows'a Bağla &amp; Aç</button>
                            <div style="display:flex; gap: 8px;">
                                <button class="btn btn-secondary" onclick="openStorageDrive('Z:')">📂 Gezginde Aç</button>
                                <button class="btn btn-danger" onclick="unmountStorageDrive('Z:')">🔌 Sürücüyü Çıkar</button>
                            </div>
                        </div>
                    </div>

                    <!-- KART 2: Anlık Kişisel Erişim Noktası (Instant Hotspot) -->
                    <div class="card">
                        <div style="display:flex; align-items:center; gap: 14px; margin-bottom: 16px;">
                            <div style="width: 48px; height: 48px; border-radius: 12px; background: rgba(16, 185, 129, 0.15); display:flex; align-items:center; justify-content:center; font-size: 24px;">📡</div>
                            <div>
                                <h3 style="font-size: 16px; font-weight: 800;">Anlık Kişisel Erişim Noktası</h3>
                                <p style="font-size: 12px; color: var(--text-muted);">Tek tıkla Hotspot aç ve PC'yi Wi-Fi ile bağla</p>
                            </div>
                        </div>

                        <div style="background: rgba(0,0,0,0.3); border-radius: var(--radius-md); padding: 14px; margin-bottom: 16px; font-size: 13px;">
                            <div style="display:flex; justify-content:space-between; margin-bottom: 6px;">
                                <span style="color:var(--text-secondary);">Hotspot Durumu:</span>
                                <span id="hotspotStatusBadge" style="color:var(--text-muted); font-weight:700;">Kapalı ⚪</span>
                            </div>
                            <div style="display:flex; justify-content:space-between; margin-bottom: 6px;">
                                <span style="color:var(--text-secondary);">Wi-Fi SSID:</span>
                                <strong id="hotspotSSID" style="color:var(--accent-blue);">--</strong>
                            </div>
                            <div style="display:flex; justify-content:space-between;">
                                <span style="color:var(--text-secondary);">Şifre:</span>
                                <strong id="hotspotPassword" style="font-family:'JetBrains Mono',monospace;">--</strong>
                            </div>
                        </div>

                        <div style="display:flex; flex-direction:column; gap: 10px;">
                            <button class="btn btn-success" onclick="connectInstantHotspot()">🚀 Hotspot Başlat &amp; PC'yi Otomatik Bağla</button>
                            <button class="btn btn-secondary" onclick="toggleHotspot('STOP')">⏹️ Hotspot'u Kapat</button>
                        </div>
                    </div>

                    <!-- KART 3: PC Üzerinden Sesli Telefon Görüşmesi (Hands-Free Call Audio) -->
                    <div class="card" style="grid-column: 1 / -1;">
                        <div style="display:flex; align-items:center; justify-content:space-between; margin-bottom: 16px;">
                            <div style="display:flex; align-items:center; gap: 14px;">
                                <div style="width: 48px; height: 48px; border-radius: 12px; background: rgba(99, 102, 241, 0.15); display:flex; align-items:center; justify-content:center; font-size: 24px;">🎙️</div>
                                <div>
                                    <h3 style="font-size: 16px; font-weight: 800;">PC Üzerinden Sesli Telefon Görüşmesi (Hands-Free)</h3>
                                    <p style="font-size: 12px; color: var(--text-muted);">Aramaları bilgisayar mikrofonunuz ve hoparlörünüzle eller serbest yapın</p>
                                </div>
                            </div>
                            <span id="callAudioActiveBadge" style="background:rgba(255,255,255,0.06); padding:4px 12px; border-radius:99px; font-size:12px; font-weight:700;">Beklemede ⚪</span>
                        </div>

                        <div style="display:flex; gap: 16px; align-items:center; flex-wrap:wrap;">
                            <button class="btn btn-primary" id="btnToggleHandsFree" onclick="toggleHandsFreeAudio()">🎙️ PC'den Konuş (Hands-Free Başlat)</button>
                            <button class="btn btn-secondary" id="btnTogglePCMic" onclick="togglePCMic()" style="display:none;">🔇 PC Mikrofonunu Sustur</button>
                            <div style="font-size: 13px; color: var(--text-secondary); margin-left: auto;">
                                16kHz HD PCM çift yönlü ses köprüsü
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
                    <div style="flex: 1; display: flex; align-items: center; justify-content: center; background: #0307    <script>
        let allSmsMessages = [];
        let activeThreadAddress = null;
        let isPhoneRinging = false;
        let isSeekingMedia = false;
        let currentMediaDuration = 0;
        let connectedDevices = [];
        let activeGlobalDeviceId = "all";
        let activeSmsFilterDeviceId = "all";
        let activeNotifFilterDeviceId = "all";
        let allNotifications = [];

        function switchTab(tabId) {
            document.querySelectorAll('.tab-content').forEach(function(el) { el.classList.remove('active'); });
            document.querySelectorAll('.nav-item').forEach(function(el) { el.classList.remove('active'); });

            const targetTab = document.getElementById('tab-' + tabId);
            if (targetTab) targetTab.classList.add('active');

            const navIdx = ['overview', 'calls', 'sms', 'media', 'clipboard', 'notifications', 'files', 'contacts', 'tabsharing', 'photos', 'screen', 'network'].indexOf(tabId);
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
                'photos': 'Fotoğraf Galerisi',
                'screen': 'Canlı Telefon Ekranı & Uzaktan Kontrol',
                'network': 'Ağ Sürücüsü (Z:\\) & Hotspot & Ses Köprüsü'
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
            } else if (tabId === 'screen') {
                setupScreenTouch();
                setupScreenKeyboard();
                setupScreenDragAndDrop();
            }
        }

        function updateDeviceSelectors(devices) {
            connectedDevices = devices || [];

            const dropdownConfigs = [
                { id: 'globalDeviceSelector', defaultLabel: '🌐 Tüm Cihazlar', defaultValue: 'all' },
                { id: 'callDeviceSelector', defaultLabel: 'Otomatik (Varsayılan)', defaultValue: 'all' },
                { id: 'smsSendDeviceSelector', defaultLabel: 'Otomatik', defaultValue: 'all' },
                { id: 'clipDeviceSelector', defaultLabel: '🌐 Tüm Cihazlar (Mesh Pano)', defaultValue: 'all' },
                { id: 'fileDeviceSelector', defaultLabel: '🌐 Tüm Bağlı Telefonlar', defaultValue: 'all' },
                { id: 'contactsDeviceSelector', defaultLabel: '🌐 Tüm Cihazların Rehberi', defaultValue: 'all' },
                { id: 'tabDeviceSelector', defaultLabel: '🌐 Tüm Telefonlarda Aç', defaultValue: 'all' },
                { id: 'photosDeviceSelector', defaultLabel: '🌐 Tüm Telefonlar', defaultValue: 'all' },
                { id: 'screenDeviceSelector', defaultLabel: 'Cihaz Seçin...', defaultValue: '' },
                { id: 'networkDeviceSelector', defaultLabel: 'Otomatik (Varsayılan Cihaz)', defaultValue: 'all' }
            ];

            dropdownConfigs.forEach(function(cfg) {
                const el = document.getElementById(cfg.id);
                if (!el) return;
                const prevVal = el.value;
                el.innerHTML = '<option value="' + cfg.defaultValue + '">' + cfg.defaultLabel + '</option>';
                connectedDevices.forEach(function(d) {
                    const opt = document.createElement('option');
                    opt.value = d.id;
                    const name = (d.model || d.name || 'Android');
                    const ip = d.ip ? ' (' + d.ip + ')' : '';
                    opt.textContent = '📱 ' + name + ip;
                    el.appendChild(opt);
                });

                // Auto-select or restore
                if (prevVal && Array.from(el.options).some(o => o.value === prevVal)) {
                    el.value = prevVal;
                } else if (connectedDevices.length === 1) {
                    el.value = connectedDevices[0].id;
                } else {
                    el.value = cfg.defaultValue;
                }
            });

            // Update SMS Filter Pills
            const smsFilterContainer = document.getElementById('smsDeviceFilterTabs');
            if (smsFilterContainer) {
                let html = '<button class="device-filter-pill' + (activeSmsFilterDeviceId === 'all' ? ' active' : '') + '" onclick="filterSmsByDevice(\'all\', this)">🌐 Tümü</button>';
                connectedDevices.forEach(function(d) {
                    const activeClass = (activeSmsFilterDeviceId === d.id ? ' active' : '');
                    const name = d.model || d.name || 'Cihaz';
                    html += '<button class="device-filter-pill' + activeClass + '" onclick="filterSmsByDevice(\'' + d.id + '\', this)">📱 ' + escapeHtml(name) + '</button>';
                });
                smsFilterContainer.innerHTML = html;
            }

            // Update Notifications Filter Pills
            const notifFilterContainer = document.getElementById('notifDeviceFilterContainer');
            if (notifFilterContainer) {
                let html = '<button class="device-filter-pill' + (activeNotifFilterDeviceId === 'all' ? ' active' : '') + '" onclick="filterNotifsByDevice(\'all\', this)">🌐 Tümü</button>';
                connectedDevices.forEach(function(d) {
                    const activeClass = (activeNotifFilterDeviceId === d.id ? ' active' : '');
                    const name = d.model || d.name || 'Cihaz';
                    html += '<button class="device-filter-pill' + activeClass + '" onclick="filterNotifsByDevice(\'' + d.id + '\', this)">📱 ' + escapeHtml(name) + '</button>';
                });
                notifFilterContainer.innerHTML = html;
            }
        }

        function onGlobalDeviceChange(devId) {
            activeGlobalDeviceId = devId;
            const globalEl = document.getElementById('globalDeviceSelector');
            if (globalEl) globalEl.value = devId;

            const selectors = ['callDeviceSelector', 'smsSendDeviceSelector', 'clipDeviceSelector', 'fileDeviceSelector', 'contactsDeviceSelector', 'tabDeviceSelector', 'photosDeviceSelector', 'networkDeviceSelector'];
            selectors.forEach(function(id) {
                const el = document.getElementById(id);
                if (el) {
                    const exists = Array.from(el.options).some(o => o.value === devId);
                    el.value = (exists && devId !== 'all') ? devId : 'all';
                }
            });

            const screenEl = document.getElementById('screenDeviceSelector');
            if (screenEl && devId !== 'all') {
                const exists = Array.from(screenEl.options).some(o => o.value === devId);
                if (exists) {
                    screenEl.value = devId;
                    if (isScreenMirroring) {
                        toggleScreenMirror(false);
                        setTimeout(() => toggleScreenMirror(true), 300);
                    }
                }
            }

            if (devId !== 'all') {
                activeSmsFilterDeviceId = devId;
                activeNotifFilterDeviceId = devId;
            } else {
                activeSmsFilterDeviceId = 'all';
                activeNotifFilterDeviceId = 'all';
            }

            renderSmsThreads();
            renderNotifications();
            renderContacts(allContacts);
            renderPhotos(getFilteredPhotos());
            renderOverviewDevicesGrid(connectedDevices);
        }

        function selectDeviceAndSwitch(devId, tab) {
            onGlobalDeviceChange(devId);
            switchTab(tab);
        }

        function renderOverviewDevicesGrid(devices) {
            const container = document.getElementById('overviewDevicesGrid');
            const countBadge = document.getElementById('overviewDevCount');
            if (!container) return;

            const devList = devices || [];
            if (countBadge) countBadge.innerText = devList.length + ' Cihaz';

            if (devList.length === 0) {
                container.innerHTML = '<div class="card" style="padding:24px; text-align:center; color:var(--text-muted);">' +
                    '<div style="font-size:32px; margin-bottom:8px;">📱</div>' +
                    '<div style="font-size:14px; font-weight:700; color:var(--text-primary);">Bağlantı Aranıyor...</div>' +
                    '<div style="font-size:12px; color:var(--text-secondary); margin-top:4px;">Wi-Fi eşleşmesi bekleniyor</div>' +
                '</div>';
                return;
            }

            container.innerHTML = devList.map(function(d) {
                const isSelected = (activeGlobalDeviceId === d.id);
                const chargingStr = d.is_charging ? '⚡ Şarj Oluyor' : 'Pilde Çalışıyor';
                const batVal = d.battery_level >= 0 ? d.battery_level : 0;
                const batPct = d.battery_level >= 0 ? d.battery_level + '%' : '--%';
                const name = escapeHtml(d.model || d.name || 'Android Cihaz');
                const ipStr = escapeHtml(d.ip || d.remote_addr || 'Wi-Fi');

                return '<div class="device-card-item' + (isSelected ? ' selected' : '') + '">' +
                    '<div style="display:flex; justify-content:space-between; align-items:flex-start; margin-bottom:12px;">' +
                        '<div style="display:flex; align-items:center; gap:12px;">' +
                            '<div style="width:44px; height:44px; border-radius:12px; background:linear-gradient(135deg, rgba(56,189,248,0.2), rgba(99,102,241,0.2)); border:1px solid rgba(56,189,248,0.3); display:flex; align-items:center; justify-content:center; font-size:22px;">📱</div>' +
                            '<div>' +
                                '<div style="font-size:15px; font-weight:800; color:var(--text-primary); display:flex; align-items:center; gap:6px;">' +
                                    name + (d.is_charging ? '<span style="color:#10B981; font-size:12px;">⚡</span>' : '') +
                                '</div>' +
                                '<div style="font-size:11px; color:var(--text-muted); margin-top:2px;">' +
                                    ipStr + ' • ' + batPct +
                                '</div>' +
                            '</div>' +
                        '</div>' +
                        '<span class="status-pill" style="font-size:10px; padding:3px 8px;">🟢 Çevrimiçi</span>' +
                    '</div>' +
                    '<div style="background:rgba(0,0,0,0.25); border-radius:var(--radius-sm); padding:10px 12px; margin-bottom:12px;">' +
                        '<div style="display:flex; justify-content:space-between; font-size:11px; margin-bottom:6px;">' +
                            '<span style="color:var(--text-secondary);">' + chargingStr + '</span>' +
                            '<strong style="color:var(--accent-green);">' + batPct + '</strong>' +
                        '</div>' +
                        '<div style="width:100%; height:6px; background:rgba(255,255,255,0.1); border-radius:99px; overflow:hidden;">' +
                            '<div style="width:' + batVal + '%; height:100%; background:linear-gradient(90deg, #10B981, #38BDF8); transition:width 0.5s;"></div>' +
                        '</div>' +
                    '</div>' +
                    '<div style="display:flex; gap:6px; margin-bottom:10px;">' +
                        '<button class="btn" style="flex:1; padding:5px 8px; font-size:11px;" onclick="sendPhoneCmd(\'VOLUME_UP\', \'' + d.id + '\')">🔊 Ses +</button>' +
                        '<button class="btn" style="flex:1; padding:5px 8px; font-size:11px;" onclick="sendPhoneCmd(\'VOLUME_DOWN\', \'' + d.id + '\')">🔉 Ses -</button>' +
                        '<button class="btn" style="flex:1; padding:5px 8px; font-size:11px;" onclick="sendPhoneCmd(\'MUTE\', \'' + d.id + '\')">🔇 Sessiz</button>' +
                    '</div>' +
                    '<div style="display:flex; gap:6px; border-top:1px solid rgba(255,255,255,0.06); padding-top:10px;">' +
                        '<button class="btn btn-secondary" style="flex:1; padding:5px 8px; font-size:11px;" onclick="selectDeviceAndSwitch(\'' + d.id + '\', \'screen\')">📱 Ekran</button>' +
                        '<button class="btn btn-secondary" style="flex:1; padding:5px 8px; font-size:11px;" onclick="selectDeviceAndSwitch(\'' + d.id + '\', \'sms\')">💬 SMS</button>' +
                        '<button class="btn ' + (isSelected ? 'btn-primary' : 'btn-secondary') + '" style="flex:1; padding:5px 8px; font-size:11px;" onclick="onGlobalDeviceChange(\'' + d.id + '\')">🎯 Yönet</button>' +
                    '</div>' +
                '</div>';
            }).join('');
        }

        function renderMultiDeviceMedia(devices) {
            const container = document.getElementById('multiDevicePhoneMediaContainer');
            if (!container) return;

            const devList = devices || [];
            if (devList.length === 0) {
                container.innerHTML = '<div class="card" style="padding:24px; text-align:center; color:var(--text-muted);">' +
                    '<div style="font-size:32px; margin-bottom:8px;">🎵</div>' +
                    '<div style="font-size:14px; font-weight:700;">Telefonda Çalan Medya Yok</div>' +
                    '<div style="font-size:12px; color:var(--text-secondary); margin-top:4px;">Bağlı bir Android cihazda müzik veya video açın</div>' +
                '</div>';
                return;
            }

            container.innerHTML = devList.map(function(d) {
                const m = d.media || {};
                const isPlaying = m.is_playing;
                const statusTag = isPlaying ? 'OYNATILIYOR 🟢' : (m.title ? 'DURAKLATILDI ⏸' : 'BEKLEMEDE ⚪');
                const title = escapeHtml(m.title || 'Müzik Çalınmıyor');
                const artist = escapeHtml(m.artist || m.album || (d.model || d.name || 'Telefonda müzik açın'));
                const duration = m.duration_ms || 0;
                const position = m.position_ms || 0;
                const curStr = formatMs(position);
                const totalStr = formatMs(duration);
                const pct = (duration > 0) ? Math.min(100, Math.max(0, (position / duration) * 100)) : 0;
                const devName = escapeHtml(d.model || d.name || 'Android');

                return '<div class="card" style="margin-bottom:0;">' +
                    '<div style="display:flex; justify-content:space-between; align-items:center; margin-bottom:16px;">' +
                        '<div style="display:flex; align-items:center; gap:8px;">' +
                            '<h3 style="font-size:16px; font-weight:700;">📱 ' + devName + '</h3>' +
                            '<span class="device-tag">Android</span>' +
                        '</div>' +
                        '<span style="font-size:11px; background:rgba(16,185,129,0.15); color:var(--accent-green); padding:3px 8px; border-radius:4px; font-weight:700;">' + statusTag + '</span>' +
                    '</div>' +
                    '<div style="display:flex; align-items:center; gap:16px; background:rgba(0,0,0,0.3); padding:16px; border-radius:var(--radius-lg); margin-bottom:14px;">' +
                        '<div style="width:60px; height:60px; border-radius:var(--radius-md); background:linear-gradient(135deg, #10B981, #059669); display:flex; align-items:center; justify-content:center; font-size:26px; box-shadow:0 6px 20px rgba(16,185,129,0.25); flex-shrink:0;">🎵</div>' +
                        '<div style="flex:1; overflow:hidden;">' +
                            '<div style="font-size:10px; font-weight:700; color:var(--accent-green); text-transform:uppercase; margin-bottom:2px;">MEDYA OYNATICI</div>' +
                            '<h3 style="font-size:16px; font-weight:800; white-space:nowrap; overflow:hidden; text-overflow:ellipsis;">' + title + '</h3>' +
                            '<p style="font-size:12px; color:var(--text-secondary); margin-top:2px; white-space:nowrap; overflow:hidden; text-overflow:ellipsis;">' + artist + '</p>' +
                        '</div>' +
                    '</div>' +
                    '<div style="margin-bottom:14px; background:rgba(0,0,0,0.2); padding:12px; border-radius:var(--radius-md);">' +
                        '<input type="range" min="0" max="100" value="' + pct + '" style="width:100%; accent-color:#10B981; cursor:pointer;" onchange="seekPhoneMedia(this.value, \'' + d.id + '\')">' +
                        '<div style="display:flex; justify-content:space-between; font-size:11px; color:var(--text-secondary); margin-top:4px;">' +
                            '<span>' + curStr + '</span>' +
                            '<span>' + totalStr + '</span>' +
                        '</div>' +
                    '</div>' +
                    '<div style="display:flex; justify-content:center; gap:6px; flex-wrap:wrap; margin-bottom:10px;">' +
                        '<button class="btn" style="padding:8px 14px; font-size:12px;" onclick="seekPhoneRelative(-15, \'' + d.id + '\')">⏪ 15s</button>' +
                        '<button class="btn" style="padding:8px 14px; font-size:12px;" onclick="sendPhoneCmd(\'PREV\', \'' + d.id + '\')">⏮ Önceki</button>' +
                        '<button class="btn btn-primary" style="padding:8px 18px; font-size:12px; background:#10B981;" onclick="sendPhoneCmd(\'PLAY_PAUSE\', \'' + d.id + '\')">⏯ ' + (isPlaying ? 'Duraklat' : 'Oynat') + '</button>' +
                        '<button class="btn" style="padding:8px 14px; font-size:12px;" onclick="sendPhoneCmd(\'NEXT\', \'' + d.id + '\')">⏭ Sonraki</button>' +
                        '<button class="btn" style="padding:8px 14px; font-size:12px;" onclick="seekPhoneRelative(15, \'' + d.id + '\')">⏩ 15s</button>' +
                    '</div>' +
                    '<div style="display:flex; justify-content:center; gap:8px;">' +
                        '<button class="btn" style="padding:6px 12px; font-size:11px;" onclick="sendPhoneCmd(\'VOLUME_DOWN\', \'' + d.id + '\')">🔉 Ses -</button>' +
                        '<button class="btn" style="padding:6px 12px; font-size:11px;" onclick="sendPhoneCmd(\'VOLUME_UP\', \'' + d.id + '\')">🔊 Ses +</button>' +
                        '<button class="btn" style="padding:6px 12px; font-size:11px;" onclick="sendPhoneCmd(\'MUTE\', \'' + d.id + '\')">🔇 Sessiz</button>' +
                    '</div>' +
                '</div>';
            }).join('');
        }

        async function updateStatus() {
            try {
                const res = await fetch('/status');
                const data = await res.json();

                // Multi-device sync
                updateDeviceSelectors(data.devices || []);
                renderOverviewDevicesGrid(data.devices || []);
                renderMultiDeviceMedia(data.devices || []);

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

                // PC Media Update
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

                // Notifications
                allNotifications = data.notifications || [];
                renderNotifications();

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
                console.error('Status fetch error:', e);
            }
        }

        function filterNotifsByDevice(devId, btn) {
            activeNotifFilterDeviceId = devId;
            document.querySelectorAll('#notifDeviceFilterContainer .device-filter-pill').forEach(function(el) {
                el.classList.remove('active');
            });
            if (btn) btn.classList.add('active');
            renderNotifications();
        }

        function renderNotifications() {
            const listEl = document.getElementById('fullNotifList');
            const badgeFull = document.getElementById('notifBadgeFull');
            const quickNotifsEl = document.getElementById('quickNotifList');

            const filtered = (activeNotifFilterDeviceId && activeNotifFilterDeviceId !== 'all')
                ? allNotifications.filter(n => n.device_id === activeNotifFilterDeviceId)
                : allNotifications;

            if (badgeFull) badgeFull.innerText = filtered.length + ' Bildirim';

            if (!filtered || filtered.length === 0) {
                if (listEl) listEl.innerHTML = '<p style="color:var(--text-muted); font-size:14px; text-align:center; padding:40px;">Henüz gelen bildirim yok.</p>';
                if (quickNotifsEl) quickNotifsEl.innerHTML = '<p style="color:var(--text-muted); font-size:13px;">Gelen bildirim bulunamadı.</p>';
                return;
            }

            if (listEl) {
                listEl.innerHTML = filtered.map(function(n) {
                    var replyBox = '';
                    if (n.can_reply && n.key) {
                        replyBox = '<div style="margin-top:10px; display:flex; gap:8px;">' +
                            '<input type="text" id="replyInput_' + escapeHtml(n.id) + '" placeholder="Yanıt yazın..." style="flex:1; background:var(--bg-input); border:1px solid var(--border-card); border-radius:var(--radius-sm); padding:6px 12px; color:var(--text-primary); font-size:12px; outline:none;" onkeydown="if(event.key===\'Enter\') sendNotificationReply(\'' + escapeHtml(n.key) + '\', \'' + escapeHtml(n.id) + '\', \'' + escapeHtml(n.device_id || '') + '\')">' +
                            '<button class="btn btn-primary" style="font-size:12px; padding:6px 14px;" onclick="sendNotificationReply(\'' + escapeHtml(n.key) + '\', \'' + escapeHtml(n.id) + '\', \'' + escapeHtml(n.device_id || '') + '\')">Yanıtla</button>' +
                        '</div>';
                    }
                    var actionsBox = '';
                    if (n.actions && n.actions.length > 0 && n.key) {
                        actionsBox = '<div style="margin-top:10px; display:flex; flex-wrap:wrap; gap:8px;">' +
                            n.actions.filter(function(a) { return !a.is_reply; }).map(function(a) {
                                return '<button class="btn btn-secondary" style="font-size:11px; padding:5px 12px; border-radius:6px; cursor:pointer;" onclick="triggerNotificationAction(\'' + escapeHtml(n.key) + '\', ' + a.index + ', this, \'' + escapeHtml(n.device_id || '') + '\')">⚡ ' +
                                    escapeHtml(a.title) +
                                '</button>';
                            }).join('') +
                        '</div>';
                    }
                    var devTag = n.device_name ? '<span class="device-tag">📱 ' + escapeHtml(n.device_name) + '</span>' : '';
                    return '<div class="notif-item-card" style="background:rgba(255,255,255,0.03); border:1px solid rgba(255,255,255,0.06); border-radius:10px; padding:14px; position:relative; transition:all 0.3s ease;">' +
                        '<div style="display:flex; justify-content:space-between; align-items:center; margin-bottom:4px;">' +
                            '<div style="display:flex; align-items:center; gap:8px;">' +
                                '<strong style="color:var(--accent-blue); font-size:13px;">' + escapeHtml(n.app_name || 'Uygulama') + '</strong>' +
                                devTag +
                            '</div>' +
                            '<div style="display:flex; align-items:center; gap:8px;">' +
                                '<span style="font-size:11px; color:var(--text-muted);">' + formatTime(n.timestamp) + '</span>' +
                                '<button title="Bildirimi Kapat (Telefonda da silinir)" onclick="dismissNotification(\'' + escapeHtml(n.key || '') + '\', \'' + escapeHtml(n.id || '') + '\', this, \'' + escapeHtml(n.device_id || '') + '\')" style="background:none; border:none; color:var(--text-muted); font-size:14px; cursor:pointer; padding:2px 6px; border-radius:4px; line-height:1; transition:all 0.2s;" onmouseover="this.style.color=\'var(--accent-red)\'; this.style.background=\'rgba(248,81,73,0.15)\'" onmouseout="this.style.color=\'var(--text-muted)\'; this.style.background=\'none\'">✕</button>' +
                            '</div>' +
                        '</div>' +
                        '<div style="font-weight:700; font-size:14px;">' + escapeHtml(n.title || '') + '</div>' +
                        '<div style="font-size:13px; color:var(--text-secondary); margin-top:2px;">' + escapeHtml(n.text || '') + '</div>' +
                        actionsBox +
                        replyBox +
                    '</div>';
                }).join('');
            }

            if (quickNotifsEl) {
                quickNotifsEl.innerHTML = filtered.slice(0, 3).map(function(n) {
                    var devTag = n.device_name ? '<span class="device-tag" style="font-size:9px; padding:1px 6px;">📱 ' + escapeHtml(n.device_name) + '</span>' : '';
                    return '<div style="background:rgba(255,255,255,0.03); border:1px solid rgba(255,255,255,0.06); border-radius:8px; padding:10px; cursor:pointer;" onclick="switchTab(\'notifications\')">' +
                        '<div style="display:flex; justify-content:space-between; align-items:center; margin-bottom:2px;">' +
                            '<div style="display:flex; align-items:center; gap:6px;">' +
                                '<strong style="color:var(--accent-blue); font-size:12px;">' + escapeHtml(n.app_name || 'Uygulama') + '</strong>' +
                                devTag +
                            '</div>' +
                            '<span style="font-size:10px; color:var(--text-muted);">' + formatTime(n.timestamp) + '</span>' +
                        '</div>' +
                        '<div style="font-weight:700; font-size:13px;">' + escapeHtml(n.title || '') + '</div>' +
                        '<div style="font-size:12px; color:var(--text-secondary); margin-top:2px; white-space:nowrap; overflow:hidden; text-overflow:ellipsis;">' + escapeHtml(n.text || '') + '</div>' +
                    '</div>';
                }).join('');
            }
        }

        function handleCallState(call, durationSec) {
            const overlay = document.getElementById('incomingCallOverlay');
            const activeBar = document.getElementById('activeCallBar');
            const callBadge = document.getElementById('callNavBadge');
            const callerDevEl = document.getElementById('incomingCallerDevice');

            if (call.state === 'RINGING') {
                overlay.style.display = 'block';
                activeBar.style.display = 'none';
                callBadge.style.display = 'inline-block';
                callBadge.innerText = 'Çalıyor';
                if (callerDevEl) {
                    callerDevEl.innerText = call.device_name ? '📱 Gelen Hat: ' + call.device_name : '';
                }
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
                const devSuffix = call.device_name ? ' (' + call.device_name + ')' : '';
                document.getElementById('activeCallTitle').innerText = '🟢 ' + (call.caller_name || call.phone_number || 'Arama') + devSuffix;
            } else {
                overlay.style.display = 'none';
                activeBar.style.display = 'none';
                callBadge.style.display = 'none';
            }
        }

        async function callAction(action, number, value, devId) {
            try {
                devId = devId || document.getElementById('callDeviceSelector')?.value || activeGlobalDeviceId;
                let url = '/call/action?action=' + encodeURIComponent(action);
                if (number) url += '&number=' + encodeURIComponent(number);
                if (value !== undefined) url += '&value=' + encodeURIComponent(value);
                if (devId && devId !== 'all') url += '&device_id=' + encodeURIComponent(devId);
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
            const devId = document.getElementById('callDeviceSelector')?.value || activeGlobalDeviceId;
            if (num) {
                callAction('DIAL', num, undefined, devId);
            }
        }

        function dialActiveChat() {
            if (activeThreadAddress) {
                const devId = document.getElementById('callDeviceSelector')?.value || activeGlobalDeviceId;
                callAction('DIAL', activeThreadAddress, undefined, devId);
            }
        }

        // SMS Functions
        async function loadSmsList(devId) {
            try {
                devId = devId || activeGlobalDeviceId;
                let url = '/sms/list';
                if (devId && devId !== 'all') url += '?device_id=' + encodeURIComponent(devId);
                const res = await fetch(url);
                const data = await res.json();
                allSmsMessages = data.messages || [];
                renderSmsThreads();
                renderQuickSms();
            } catch (e) {
                console.error(e);
            }
        }

        function filterSmsByDevice(devId, btn) {
            activeSmsFilterDeviceId = devId;
            document.querySelectorAll('#smsDeviceFilterTabs .device-filter-pill').forEach(function(el) {
                el.classList.remove('active');
            });
            if (btn) btn.classList.add('active');
            renderSmsThreads();
        }

        function renderQuickSms() {
            const el = document.getElementById('quickSmsList');
            if (!allSmsMessages.length) {
                el.innerHTML = '<p style="color:var(--text-muted); font-size:13px;">Gelen mesaj bulunamadı.</p>';
                return;
            }
            el.innerHTML = allSmsMessages.slice(0, 3).map(function(m) {
                const devTag = m.device_name ? '<span class="device-tag" style="font-size:9px; padding:1px 6px;">📱 ' + escapeHtml(m.device_name) + '</span>' : '';
                return '<div style="background:rgba(255,255,255,0.03); border-radius:8px; padding:10px; cursor:pointer;" onclick="switchTab(\'sms\')">' +
                    '<div style="display:flex; justify-content:space-between; align-items:center; font-size:12px; margin-bottom:2px;">' +
                        '<div style="display:flex; align-items:center; gap:6px;">' +
                            '<strong style="color:var(--accent-blue);">' + escapeHtml(m.contact_name || m.address) + '</strong>' +
                            devTag +
                        '</div>' +
                        '<span style="color:var(--text-muted); font-size:10px;">' + formatTime(m.timestamp) + '</span>' +
                    '</div>' +
                    '<div style="font-size:12px; color:var(--text-secondary); white-space:nowrap; overflow:hidden; text-overflow:ellipsis;">' + escapeHtml(m.body) + '</div>' +
                '</div>';
            }).join('');
        }

        function renderSmsThreads() {
            const threadsMap = {};
            const filtered = (activeSmsFilterDeviceId && activeSmsFilterDeviceId !== 'all')
                ? allSmsMessages.filter(m => m.device_id === activeSmsFilterDeviceId)
                : allSmsMessages;

            filtered.forEach(function(m) {
                const key = m.address || 'Bilinmeyen';
                if (!threadsMap[key]) {
                    threadsMap[key] = {
                        address: key,
                        name: m.contact_name || key,
                        latestMsg: m.body,
                        timestamp: m.timestamp,
                        deviceName: m.device_name,
                        deviceId: m.device_id,
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
                const devBadge = t.deviceName ? '<span class="device-tag" style="font-size:9px; padding:1px 5px;">📱 ' + escapeHtml(t.deviceName) + '</span>' : '';
                return '<li class="thread-item' + activeClass + '" onclick="selectThread(\'' + escapeHtml(t.address) + '\')">' +
                    '<div class="thread-avatar">' + initial + '</div>' +
                    '<div class="thread-info">' +
                        '<div class="thread-title-row">' +
                            '<div style="display:flex; align-items:center; gap:6px; overflow:hidden;">' +
                                '<span class="thread-name">' + escapeHtml(t.name) + '</span>' +
                                devBadge +
                            '</div>' +
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
            const devName = (threadMsgs[0] && threadMsgs[0].device_name) ? (' • 📱 ' + threadMsgs[0].device_name) : '';
            document.getElementById('activeChatName').innerText = contactName;
            document.getElementById('activeChatNumber').innerText = address + devName;
            document.getElementById('activeChatAvatar').innerText = contactName.charAt(0).toUpperCase();

            // Auto switch send device to match this thread's device if known
            if (threadMsgs[0] && threadMsgs[0].device_id) {
                const sendDevEl = document.getElementById('smsSendDeviceSelector');
                if (sendDevEl && Array.from(sendDevEl.options).some(o => o.value === threadMsgs[0].device_id)) {
                    sendDevEl.value = threadMsgs[0].device_id;
                }
            }

            const chatEl = document.getElementById('chatMessages');
            chatEl.innerHTML = threadMsgs.map(function(m) {
                const bubbleClass = m.is_incoming ? 'incoming' : 'outgoing';
                const devTag = m.device_name ? '<span class="device-tag" style="font-size:9px; margin-left:4px;">' + escapeHtml(m.device_name) + '</span>' : '';
                return '<div class="msg-bubble ' + bubbleClass + '">' +
                    '<div>' + escapeHtml(m.body) + '</div>' +
                    '<div class="msg-meta">' + formatTime(m.timestamp) + (!m.is_incoming ? ' ✓' : '') + devTag + '</div>' +
                '</div>';
            }).join('');

            chatEl.scrollTop = chatEl.scrollHeight;
        }

        async function sendChatMsg() {
            const input = document.getElementById('chatInput');
            const body = input.value.trim();
            if (!body || !activeThreadAddress) return;

            const sendDevEl = document.getElementById('smsSendDeviceSelector');
            const devId = (sendDevEl && sendDevEl.value !== 'all') ? sendDevEl.value : (activeGlobalDeviceId !== 'all' ? activeGlobalDeviceId : '');

            input.value = '';
            try {
                let postBody = 'recipient=' + encodeURIComponent(activeThreadAddress) + '&body=' + encodeURIComponent(body);
                if (devId) postBody += '&device_id=' + encodeURIComponent(devId);
                await fetch('/sms/send', {
                    method: 'POST',
                    headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
                    body: postBody
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
            }
        }

        async function seekPhoneMedia(val, devId) {
            try {
                devId = devId || activeGlobalDeviceId;
                let url = '/phone/command?action=SEEK_PERCENT&percent=' + encodeURIComponent(val);
                if (devId && devId !== 'all') url += '&device_id=' + encodeURIComponent(devId);
                await fetch(url);
            } catch (e) {}
            setTimeout(function() { isSeekingPhoneMedia = false; }, 800);
        }

        async function seekPhoneRelative(deltaSec, devId) {
            try {
                devId = devId || activeGlobalDeviceId;
                const action = deltaSec > 0 ? 'SEEK_FORWARD' : 'SEEK_BACKWARD';
                let url = '/phone/command?action=' + action;
                if (devId && devId !== 'all') url += '&device_id=' + encodeURIComponent(devId);
                await fetch(url);
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

        async function sendPhoneCmd(action, devId) {
            try {
                devId = devId || activeGlobalDeviceId;
                let url = '/phone/command?action=' + encodeURIComponent(action);
                if (devId && devId !== 'all') url += '&device_id=' + encodeURIComponent(devId);
                await fetch(url);
            } catch (e) {}
        }

        async function ringPhone(devId) {
            isPhoneRinging = !isPhoneRinging;
            await sendPhoneCmd(isPhoneRinging ? 'RING' : 'STOP_RING', devId);
        }

        async function sendCustomClipText() {
            const input = document.getElementById('customClipInput');
            const val = input.value.trim();
            if (!val) return;
            const devId = document.getElementById('clipDeviceSelector')?.value || activeGlobalDeviceId;
            try {
                let url = '/clipboard/send?text=' + encodeURIComponent(val);
                if (devId && devId !== 'all') url += '&device_id=' + encodeURIComponent(devId);
                await fetch(url);
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

        async function sendNotificationReply(key, id, devId) {
            const input = document.getElementById('replyInput_' + id);
            const text = input ? input.value.trim() : '';
            if (!text) return;
            try {
                let postBody = 'key=' + encodeURIComponent(key) + '&text=' + encodeURIComponent(text);
                if (devId) postBody += '&device_id=' + encodeURIComponent(devId);
                await fetch('/notification/reply', {
                    method: 'POST',
                    headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
                    body: postBody
                });
                if (input) input.value = '';
                alert('Yanıt telefona iletildi ✅');
            } catch (e) {
                console.error(e);
                alert('Hata: ' + e.message);
            }
        }

        async function triggerNotificationAction(key, index, btn, devId) {
            try {
                if (btn) {
                    btn.disabled = true;
                    btn.style.opacity = '0.6';
                }
                let url = '/notification/action?key=' + encodeURIComponent(key) + '&index=' + index;
                if (devId) url += '&device_id=' + encodeURIComponent(devId);
                const res = await fetch(url, { method: 'POST' });
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

        async function dismissNotification(key, id, btn, devId) {
            if (btn) {
                btn.disabled = true;
                const card = btn.closest('.notif-item-card');
                if (card) {
                    card.style.opacity = '0';
                    card.style.transform = 'translateX(20px)';
                    setTimeout(() => card.remove(), 280);
                }
            }
            try {
                let url = '/notification/dismiss?key=' + encodeURIComponent(key || '') + '&id=' + encodeURIComponent(id || '');
                if (devId) url += '&device_id=' + encodeURIComponent(devId);
                await fetch(url, { method: 'POST' });
            } catch (e) {
                console.error('Bildirim kapatma hatası:', e);
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

        async function loadTransferredFiles(devId) {
            try {
                devId = devId || activeGlobalDeviceId;
                let url = '/file/list';
                if (devId && devId !== 'all') url += '?device_id=' + encodeURIComponent(devId);
                const res = await fetch(url);
                const data = await res.json();
                const files = data.files || [];
                const tbody = document.getElementById('filesTableBody');
                const badge = document.getElementById('filesNavBadge');
                if (badge) badge.innerText = files.length;
                if (!tbody) return;
                if (!files.length) {
                    tbody.innerHTML = '<tr><td colspan="6" style="padding:24px; text-align:center; color:var(--text-muted);">Henüz aktarılmış dosya yok</td></tr>';
                    return;
                }
                tbody.innerHTML = files.map(function(f) {
                    const isInc = f.direction === 'incoming';
                    const dirIcon = isInc ? '📥 Gelen' : '📤 Giden';
                    const dirColor = isInc ? 'var(--accent-green)' : 'var(--accent-blue)';
                    const sizeStr = (f.file_size / (1024 * 1024)).toFixed(2) + ' MB';
                    const devTag = f.device_name ? '<span class="device-tag">📱 ' + escapeHtml(f.device_name) + '</span>' : '<span style="color:var(--text-muted); font-size:11px;">Varsayılan</span>';
                    const dlBtn = isInc 
                        ? '<button class="btn btn-secondary" style="font-size:11px; padding:4px 10px;" onclick="openDownloadsFolder()">Klasörde Göster</button>'
                        : '<a href="/file/download/' + f.id + '/' + encodeURIComponent(f.file_name) + '" class="btn btn-secondary" style="font-size:11px; padding:4px 10px; text-decoration:none;">İndir</a>';
                    return '<tr style="border-bottom:1px solid rgba(255,255,255,0.04);">' +
                        '<td style="padding:12px 14px; color:' + dirColor + '; font-weight:700;">' + dirIcon + '</td>' +
                        '<td style="padding:12px 14px;">' + devTag + '</td>' +
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

            const fileDevEl = document.getElementById('fileDeviceSelector');
            const targetDevId = (fileDevEl && fileDevEl.value !== 'all') ? fileDevEl.value : (activeGlobalDeviceId !== 'all' ? activeGlobalDeviceId : '');

            if (box) box.style.display = 'block';
            for (let i = 0; i < files.length; i++) {
                const file = files[i];
                if (nameEl) nameEl.innerText = file.name + ' (' + (i + 1) + '/' + files.length + ') telefona gönderiliyor...';
                if (barEl) barEl.style.width = '30%';
                if (pctEl) pctEl.innerText = '30%';

                const formData = new FormData();
                formData.append('file', file);
                if (targetDevId) {
                    formData.append('device_id', targetDevId);
                    formData.append('target_device_id', targetDevId);
                }

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

        async function loadContactsList(devId) {
            try {
                devId = devId || document.getElementById('contactsDeviceSelector')?.value || activeGlobalDeviceId;
                let url = '/contacts';
                if (devId && devId !== 'all') url += '?device_id=' + encodeURIComponent(devId);
                const res = await fetch(url);
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

        function onContactsDeviceChange(devId) {
            loadContactsList(devId);
        }

        async function refreshContacts() {
            try {
                const devId = document.getElementById('contactsDeviceSelector')?.value || activeGlobalDeviceId;
                let url = '/contacts/refresh';
                if (devId && devId !== 'all') url += '?device_id=' + encodeURIComponent(devId);
                await fetch(url, { method: 'POST' });
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
                var devTag = c.device_name ? '<span class="device-tag" style="font-size:9px; padding:1px 5px;">📱 ' + escapeHtml(c.device_name) + '</span>' : '';

                return '<div style="background: rgba(18, 24, 38, 0.7); border: 1px solid var(--border-card); border-radius: var(--radius-md); padding: 16px; display: flex; flex-direction: column; gap: 12px; transition: all 0.2s;">' +
                    '<div style="display: flex; align-items: center; gap: 12px;">' +
                        '<div style="width: 44px; height: 44px; border-radius: 50%; background: ' + grad + '; display: flex; align-items: center; justify-content: center; font-weight: 800; font-size: 15px; color: white; flex-shrink: 0;">' +
                            initials +
                        '</div>' +
                        '<div style="overflow: hidden; flex: 1;">' +
                            '<div style="font-weight: 700; font-size: 14px; color: var(--text-primary); white-space: nowrap; overflow: hidden; text-overflow: ellipsis; display:flex; align-items:center; gap:6px;">' + 
                                escapeHtml(c.name) + devTag +
                            '</div>' +
                            '<div style="font-size: 12px; color: var(--text-secondary); margin-top: 2px;">' + escapeHtml(c.number) + '</div>' +
                        '</div>' +
                    '</div>' +
                    '<div style="display: flex; gap: 6px; margin-top: auto; padding-top: 10px; border-top: 1px solid rgba(255,255,255,0.06);">' +
                        '<button class="btn btn-success" style="flex: 1; padding: 6px 0; font-size: 11px;" onclick="callAction(\'DIAL\', \'' + safeNum + '\', undefined, \'' + (c.device_id || '') + '\')" title="Telefonla Ara">📞 Ara</button>' +
                        '<button class="btn btn-primary" style="flex: 1; padding: 6px 0; font-size: 11px;" onclick="startSmsWith(\'' + safeNum + '\', \'' + safeName + '\', \'' + (c.device_id || '') + '\')" title="SMS Gönder">💬 SMS</button>' +
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

        function startSmsWith(number, name, deviceId) {
            if (deviceId) {
                const sendDevEl = document.getElementById('smsSendDeviceSelector');
                if (sendDevEl && Array.from(sendDevEl.options).some(o => o.value === deviceId)) {
                    sendDevEl.value = deviceId;
                }
            }
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
            const tabDevEl = document.getElementById('tabDeviceSelector');
            const targetDevId = (tabDevEl && tabDevEl.value !== 'all') ? tabDevEl.value : (activeGlobalDeviceId !== 'all' ? activeGlobalDeviceId : '');
            try {
                let reqUrl = '/url/send_to_phone?url=' + encodeURIComponent(url);
                if (targetDevId) reqUrl += '&device_id=' + encodeURIComponent(targetDevId);
                const res = await fetch(reqUrl, { method: 'POST' });
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

        async function loadPhotosList(devId) {
            try {
                devId = devId || document.getElementById('photosDeviceSelector')?.value || activeGlobalDeviceId;
                let url = '/photos';
                if (devId && devId !== 'all') url += '?device_id=' + encodeURIComponent(devId);
                const res = await fetch(url);
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

        function onPhotosDeviceChange(devId) {
            loadPhotosList(devId);
        }

        async function refreshPhotos() {
            try {
                const devId = document.getElementById('photosDeviceSelector')?.value || activeGlobalDeviceId;
                let url = '/photos/refresh';
                if (devId && devId !== 'all') url += '?device_id=' + encodeURIComponent(devId);
                await fetch(url, { method: 'POST' });
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
                var devTag = p.device_name ? '<span class="device-tag" style="position:absolute; top:6px; left:6px; z-index:5; font-size:9px; background:rgba(15,23,42,0.85); border:1px solid rgba(56,189,248,0.4);">📱 ' + escapeHtml(p.device_name) + '</span>' : '';

                var imgHtml = thumbSrc ? 
                    '<img src="' + thumbSrc + '" alt="' + safeName + '" style="width: 100%; height: 160px; object-fit: cover; display: block; transition: transform 0.3s;">' :
                    '<div style="width: 100%; height: 160px; background: rgba(255,255,255,0.04); display: flex; align-items: center; justify-content: center; font-size: 36px;">🖼</div>';

                return '<div style="background: rgba(18, 24, 38, 0.7); border: 1px solid var(--border-card); border-radius: var(--radius-md); overflow: hidden; display: flex; flex-direction: column; transition: all 0.2s;" class="photo-card">' +
                    '<div style="position: relative; overflow: hidden; cursor: pointer;" onclick="openPhotoLightbox(' + p.id + ')">' +
                        devTag +
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
                            '<button class="btn btn-primary" style="flex: 1; font-size: 11px; padding: 5px 0;" onclick="downloadPhotoToPc(' + p.id + ', \'' + (p.device_id || '') + '\')">⬇️ İndir</button>' +
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
            info.textContent = (photo.mime_type || 'image/jpeg') + (photo.device_name ? ' • ' + photo.device_name : '');

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

        async function downloadPhotoToPc(photoId, devId) {
            try {
                devId = devId || document.getElementById('photosDeviceSelector')?.value || activeGlobalDeviceId;
                let url = '/photos/download?id=' + photoId;
                if (devId && devId !== 'all') url += '&device_id=' + encodeURIComponent(devId);
                const res = await fetch(url, { method: 'POST' });
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
                downloadPhotoToPc(activeLightboxPhoto.id, activeLightboxPhoto.device_id);
            }
        }

        function copyLightboxImage() {
            if (!activeLightboxPhoto) return;
            alert('Fotoğraf adı: ' + activeLightboxPhoto.name);
        }

        function escapeHtml(str) {
            return String(str || '').replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;').replace(/"/g, '&quot;');
        }

        function formatTime(ts) {
            if (!ts) return '';
            const d = new Date(ts);
            return d.getHours().toString().padStart(2, '0') + ':' + d.getMinutes().toString().padStart(2, '0');
        }

        // Screen Mirroring & Remote Touch Control
        let isScreenMirroring = false;
        let mirrorQuality = 75;
        let mirrorPollInterval = null;

        function onScreenDeviceChange(devId) {
            if (isScreenMirroring) {
                toggleScreenMirror(false);
                setTimeout(() => toggleScreenMirror(true), 300);
            }
        }

        function toggleScreenMirror(start, devId) {
            isScreenMirroring = start;
            devId = devId || document.getElementById('screenDeviceSelector')?.value || (activeGlobalDeviceId !== 'all' ? activeGlobalDeviceId : '');

            let url = '/screen/mirror?action=' + (start ? 'START' : 'STOP') + '&quality=' + mirrorQuality;
            if (devId) url += '&device_id=' + encodeURIComponent(devId);
            fetch(url);

            const btnStart = document.getElementById('btnMirrorStart');
            const btnStop = document.getElementById('btnMirrorStop');
            const img = document.getElementById('screenMirrorImg');
            const placeholder = document.getElementById('screenMirrorPlaceholder');
            const badge = document.getElementById('screenNavBadge');

            if (start) {
                if (btnStart) btnStart.style.display = 'none';
                if (btnStop) btnStop.style.display = 'block';
                if (img) img.style.display = 'block';
                if (placeholder) placeholder.style.display = 'none';
                if (badge) badge.style.display = 'inline-block';
                startMirrorPolling();
                setupScreenTouch();
                setupScreenKeyboard();
                setupScreenDragAndDrop();
            } else {
                if (btnStart) btnStart.style.display = 'block';
                if (btnStop) btnStop.style.display = 'none';
                if (img) img.style.display = 'none';
                if (placeholder) placeholder.style.display = 'flex';
                if (badge) badge.style.display = 'none';
                stopMirrorPolling();
            }
        }

        function setMirrorQuality(q) {
            mirrorQuality = q;
            [60, 75, 90].forEach(function(val) {
                const b = document.getElementById('btnQual' + val);
                if (b) b.className = (val === q ? 'btn btn-primary' : 'btn btn-secondary');
            });
            if (isScreenMirroring) {
                const devId = document.getElementById('screenDeviceSelector')?.value || activeGlobalDeviceId;
                let url = '/screen/mirror?action=START&quality=' + mirrorQuality;
                if (devId && devId !== 'all') url += '&device_id=' + encodeURIComponent(devId);
                fetch(url);
            }
        }

        function startMirrorPolling() {
            stopMirrorPolling();
            mirrorPollInterval = setInterval(async function() {
                if (!isScreenMirroring) return;
                try {
                    const img = document.getElementById('screenMirrorImg');
                    const devId = document.getElementById('screenDeviceSelector')?.value || (activeGlobalDeviceId !== 'all' ? activeGlobalDeviceId : '');
                    if (img) {
                        let frameUrl = '/screen/frame?format=jpeg&_t=' + Date.now();
                        if (devId) frameUrl += '&device_id=' + encodeURIComponent(devId);
                        img.src = frameUrl;
                    }
                } catch (e) {}
            }, 120);
        }

        function stopMirrorPolling() {
            if (mirrorPollInterval) {
                clearInterval(mirrorPollInterval);
                mirrorPollInterval = null;
            }
        }

        function sendScreenTouchAction(act, devId) {
            devId = devId || document.getElementById('screenDeviceSelector')?.value || activeGlobalDeviceId;
            let url = '/screen/touch?action=' + encodeURIComponent(act);
            if (devId && devId !== 'all') url += '&device_id=' + encodeURIComponent(devId);
            fetch(url);
        }

        function setupScreenTouch() {
            const wrapper = document.getElementById('screenViewWrapper');
            if (!wrapper || wrapper.dataset.bound) return;
            wrapper.dataset.bound = 'true';

            let isPointerDown = false;

            function sendPointerEvent(act, e) {
                const rect = wrapper.getBoundingClientRect();
                const x = Math.min(1.0, Math.max(0.0, (e.clientX - rect.left) / rect.width));
                const y = Math.min(1.0, Math.max(0.0, (e.clientY - rect.top) / rect.height));
                const devId = document.getElementById('screenDeviceSelector')?.value || activeGlobalDeviceId;
                let url = '/screen/touch?action=' + act + '&x=' + x.toFixed(3) + '&y=' + y.toFixed(3);
                if (devId && devId !== 'all') url += '&device_id=' + encodeURIComponent(devId);
                fetch(url);
            }

            wrapper.addEventListener('mousedown', function(e) {
                isPointerDown = true;
                sendPointerEvent('down', e);
            });
            window.addEventListener('mousemove', function(e) {
                if (!isPointerDown) return;
                sendPointerEvent('move', e);
            });
            window.addEventListener('mouseup', function(e) {
                if (!isPointerDown) return;
                isPointerDown = false;
                sendPointerEvent('up', e);
            });
        }

        // Live Keyboard Input Injection
        function setupScreenKeyboard() {
            const container = document.getElementById('phoneScreenContainer');
            const badge = document.getElementById('keyboardActiveBadge');
            if (!container || container.dataset.kbBound) return;
            container.dataset.kbBound = 'true';

            container.addEventListener('focus', function() {
                if (badge) badge.style.display = 'block';
                container.style.boxShadow = '0 25px 60px rgba(0,0,0,0.8), 0 0 35px rgba(56, 189, 248, 0.6)';
            });

            container.addEventListener('blur', function() {
                if (badge) badge.style.display = 'none';
                container.style.boxShadow = '0 25px 60px rgba(0,0,0,0.8), 0 0 25px rgba(56, 189, 248, 0.2)';
            });

            container.addEventListener('keydown', function(e) {
                if (!isScreenMirroring) return;
                const devId = document.getElementById('screenDeviceSelector')?.value || activeGlobalDeviceId;
                const devParam = (devId && devId !== 'all') ? '&device_id=' + encodeURIComponent(devId) : '';

                if (e.key === 'Backspace') {
                    e.preventDefault();
                    fetch('/screen/key?code=67' + devParam);
                } else if (e.key === 'Enter') {
                    e.preventDefault();
                    fetch('/screen/key?code=66' + devParam);
                } else if (e.key === 'Tab') {
                    e.preventDefault();
                    fetch('/screen/key?code=61' + devParam);
                } else if (e.key === 'Escape') {
                    e.preventDefault();
                    fetch('/screen/key?code=111' + devParam);
                } else if (e.key === 'ArrowUp') {
                    e.preventDefault();
                    fetch('/screen/key?code=19' + devParam);
                } else if (e.key === 'ArrowDown') {
                    e.preventDefault();
                    fetch('/screen/key?code=20' + devParam);
                } else if (e.key === 'ArrowLeft') {
                    e.preventDefault();
                    fetch('/screen/key?code=21' + devParam);
                } else if (e.key === 'ArrowRight') {
                    e.preventDefault();
                    fetch('/screen/key?code=22' + devParam);
                } else if (e.key.length === 1 && !e.ctrlKey && !e.altKey && !e.metaKey) {
                    e.preventDefault();
                    fetch('/screen/text?text=' + encodeURIComponent(e.key) + devParam);
                }
            });

            container.addEventListener('paste', function(e) {
                e.preventDefault();
                const text = (e.clipboardData || window.clipboardData).getData('text');
                const devId = document.getElementById('screenDeviceSelector')?.value || activeGlobalDeviceId;
                if (text) {
                    let postBody = 'text=' + encodeURIComponent(text);
                    if (devId && devId !== 'all') postBody += '&device_id=' + encodeURIComponent(devId);
                    fetch('/screen/text', {
                        method: 'POST',
                        headers: {'Content-Type': 'application/x-www-form-urlencoded'},
                        body: postBody
                    });
                }
            });
        }

        // Screen Mirror Drag & Drop Upload
        function setupScreenDragAndDrop() {
            const container = document.getElementById('phoneScreenContainer');
            const overlay = document.getElementById('screenDropOverlay');
            if (!container || container.dataset.dropBound) return;
            container.dataset.dropBound = 'true';

            container.addEventListener('dragover', function(e) {
                e.preventDefault();
                e.stopPropagation();
                if (overlay) overlay.style.display = 'flex';
            });

            container.addEventListener('dragleave', function(e) {
                e.preventDefault();
                e.stopPropagation();
                if (overlay && (e.target === container || e.relatedTarget === null)) overlay.style.display = 'none';
            });

            container.addEventListener('drop', async function(e) {
                e.preventDefault();
                e.stopPropagation();
                if (overlay) overlay.style.display = 'none';
                const files = e.dataTransfer.files;
                if (!files || files.length === 0) return;

                const devId = document.getElementById('screenDeviceSelector')?.value || activeGlobalDeviceId;

                for (let i = 0; i < files.length; i++) {
                    const file = files[i];
                    const formData = new FormData();
                    formData.append('file', file);
                    if (devId && devId !== 'all') {
                        formData.append('device_id', devId);
                        formData.append('target_device_id', devId);
                    }
                    try {
                        const notif = document.createElement('div');
                        notif.style = 'position:fixed; bottom:20px; right:20px; background:#0284c7; color:#fff; padding:12px 20px; border-radius:10px; font-size:13px; font-weight:700; z-index:99999; box-shadow:0 10px 25px rgba(0,0,0,0.5);';
                        notif.innerText = '📤 Gönderiliyor: ' + file.name;
                        document.body.appendChild(notif);

                        const res = await fetch('/file/upload', { method: 'POST', body: formData });
                        const data = await res.json();
                        notif.remove();
                        if (data.success) {
                            const successNotif = document.createElement('div');
                            successNotif.style = 'position:fixed; bottom:20px; right:20px; background:#10b981; color:#fff; padding:12px 20px; border-radius:10px; font-size:13px; font-weight:700; z-index:99999; box-shadow:0 10px 25px rgba(0,0,0,0.5);';
                            successNotif.innerText = '✅ Dosya Telefona Aktarıldı: ' + file.name;
                            document.body.appendChild(successNotif);
                            setTimeout(function() { successNotif.remove(); }, 3500);
                        }
                    } catch (err) {
                        alert('Gönderim hatası: ' + err.message);
                    }
                }
            });
        }

        // AMOLED Screen Dimming (Screen-Off Power Saving)
        let isScreenDimmed = false;
        async function toggleScreenDim(devId) {
            isScreenDimmed = !isScreenDimmed;
            const btn = document.getElementById('btnDimScreen');
            devId = devId || document.getElementById('screenDeviceSelector')?.value || activeGlobalDeviceId;
            try {
                let url = '/screen/dim?enabled=' + isScreenDimmed;
                if (devId && devId !== 'all') url += '&device_id=' + encodeURIComponent(devId);
                await fetch(url);
                if (btn) {
                    if (isScreenDimmed) {
                        btn.innerHTML = '☀️ Ekranı Aç';
                        btn.style.background = 'rgba(16, 185, 129, 0.2)';
                        btn.style.color = '#10B981';
                        btn.style.borderColor = '#10B981';
                    } else {
                        btn.innerHTML = '🌙 Ekranı Karart';
                        btn.style.background = '';
                        btn.style.color = '';
                        btn.style.borderColor = '';
                    }
                }
            } catch (e) {
                console.error('Screen dim error:', e);
            }
        }

        // Phone Ringer Mode Control
        async function setPhoneRinger(mode, devId) {
            try {
                devId = devId || document.getElementById('screenDeviceSelector')?.value || activeGlobalDeviceId;
                let url = '/ringer/set?mode=' + encodeURIComponent(mode);
                if (devId && devId !== 'all') url += '&device_id=' + encodeURIComponent(devId);
                await fetch(url);
                const notif = document.createElement('div');
                notif.style = 'position:fixed; bottom:20px; right:20px; background:#38bdf8; color:#0f172a; padding:12px 20px; border-radius:10px; font-size:13px; font-weight:700; z-index:99999; box-shadow:0 10px 25px rgba(0,0,0,0.5);';
                notif.innerText = '🔔 Telefon Ses Modu: ' + mode;
                document.body.appendChild(notif);
                setTimeout(function() { notif.remove(); }, 2500);
            } catch (e) {
                console.error('Ringer error:', e);
            }
        }

        // App Streaming (Uygulama Listesi ve Başlatma)
        let phoneInstalledApps = [];
        async function loadPhoneApps(devId) {
            const listEl = document.getElementById('phoneAppsList');
            if (listEl) listEl.innerHTML = '<div style="grid-column: 1 / -1; text-align: center; color: var(--text-muted); font-size: 12px; padding: 12px;">Uygulamalar telefondan yükleniyor...</div>';
            try {
                devId = devId || document.getElementById('screenDeviceSelector')?.value || activeGlobalDeviceId;
                let url = '/apps/list';
                if (devId && devId !== 'all') url += '?device_id=' + encodeURIComponent(devId);
                const res = await fetch(url);
                const data = await res.json();
                if (data.apps && data.apps.length > 0) {
                    phoneInstalledApps = data.apps;
                    renderApps(phoneInstalledApps);
                } else {
                    setTimeout(async () => {
                        const r2 = await fetch(url);
                        const d2 = await r2.json();
                        phoneInstalledApps = d2.apps || [];
                        renderApps(phoneInstalledApps);
                    }, 1200);
                }
            } catch (e) {
                console.error('Apps load error:', e);
            }
        }

        function renderApps(apps) {
            const listEl = document.getElementById('phoneAppsList');
            if (!listEl) return;
            if (!apps || apps.length === 0) {
                listEl.innerHTML = '<div style="grid-column: 1 / -1; text-align: center; color: var(--text-muted); font-size: 12px; padding: 12px;">Henüz uygulama bulunamadı. "Yenile" butonuna tıklayın.</div>';
                return;
            }
            listEl.innerHTML = apps.map(function(app) {
                return '<button class="btn btn-secondary" onclick="launchPhoneApp(\'' + app.package_name + '\')" style="display:flex; align-items:center; gap: 6px; padding: 7px 8px; font-size: 11px; text-align: left; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; border-radius: 6px;">' +
                    '<span>📱</span>' +
                    '<span style="overflow: hidden; text-overflow: ellipsis; white-space: nowrap;">' + (app.name || app.package_name) + '</span>' +
                '</button>';
            }).join('');
        }

        function filterApps() {
            const query = (document.getElementById('appSearchInput')?.value || '').toLowerCase();
            const filtered = phoneInstalledApps.filter(function(a) {
                return a.name.toLowerCase().indexOf(query) !== -1 || a.package_name.toLowerCase().indexOf(query) !== -1;
            });
            renderApps(filtered);
        }

        async function launchPhoneApp(pkg, devId) {
            try {
                devId = devId || document.getElementById('screenDeviceSelector')?.value || activeGlobalDeviceId;
                let url = '/apps/launch?pkg=' + encodeURIComponent(pkg);
                if (devId && devId !== 'all') url += '&device_id=' + encodeURIComponent(devId);
                await fetch(url);
                if (!isScreenMirroring) {
                    toggleScreenMirror(true, devId);
                }
                const notif = document.createElement('div');
                notif.style = 'position:fixed; bottom:20px; right:20px; background:#10b981; color:#fff; padding:12px 20px; border-radius:10px; font-size:13px; font-weight:700; z-index:99999; box-shadow:0 10px 25px rgba(0,0,0,0.5);';
                notif.innerText = '📱 Uygulama Başlatıldı: ' + pkg;
                document.body.appendChild(notif);
                setTimeout(function() { notif.remove(); }, 3000);
            } catch (e) {
                console.error('Launch error:', e);
            }
        }

        // WebDAV Storage Mount (Z:\)
        function onNetworkDeviceChange(devId) {
            // Update network targets if needed
        }

        async function mountStorageDrive(drive, devId) {
            try {
                devId = devId || document.getElementById('networkDeviceSelector')?.value || activeGlobalDeviceId;
                let url = '/storage/mount?drive=' + (drive || 'Z:');
                if (devId && devId !== 'all') url += '&device_id=' + encodeURIComponent(devId);
                const res = await fetch(url);
                const json = await res.json();
                if (json.success) {
                    alert('✅ ' + json.drive + ' Sürücüsü Windows Gezgini\'ne başarıyla bağlandı ve açıldı!');
                } else {
                    alert('Bağlama sonucu: ' + (json.output || 'İşlem tamamlandı'));
                }
            } catch (e) {
                alert('Ağ hatası: ' + e.message);
            }
        }

        async function unmountStorageDrive(drive, devId) {
            try {
                devId = devId || document.getElementById('networkDeviceSelector')?.value || activeGlobalDeviceId;
                let url = '/storage/unmount?drive=' + (drive || 'Z:');
                if (devId && devId !== 'all') url += '&device_id=' + encodeURIComponent(devId);
                const res = await fetch(url);
                const json = await res.json();
                alert('🔌 ' + (drive || 'Z:') + ' sürücüsü bağlantısı kesildi.');
            } catch (e) {
                alert('Hata: ' + e.message);
            }
        }

        function openStorageDrive(drive) {
            fetch('/file/open_folder?drive=' + (drive || 'Z:'));
        }

        // Instant Hotspot
        async function toggleHotspot(action, devId) {
            try {
                devId = devId || document.getElementById('networkDeviceSelector')?.value || activeGlobalDeviceId;
                let url = '/hotspot/toggle?action=' + (action || '');
                if (devId && devId !== 'all') url += '&device_id=' + encodeURIComponent(devId);
                const res = await fetch(url);
                const json = await res.json();
                alert('📡 Hotspot komutu iletildi: ' + json.action);
            } catch (e) {
                alert('Hata: ' + e.message);
            }
        }

        async function connectInstantHotspot(devId) {
            try {
                devId = devId || document.getElementById('networkDeviceSelector')?.value || activeGlobalDeviceId;
                let url = '/hotspot/connect';
                if (devId && devId !== 'all') url += '?device_id=' + encodeURIComponent(devId);
                const res = await fetch(url);
                const json = await res.json();
                if (json.success) {
                    alert('🚀 Hotspot başlatıldı! PC Wi-Fi ' + json.ssid + ' ağına otomatik bağlanıyor...');
                } else {
                    alert(json.message || 'Lütfen birkaç saniye sonra tekrar deneyin.');
                }
            } catch (e) {
                alert('Hata: ' + e.message);
            }
        }

        // Hands-Free Call Audio
        let isHandsFreeActive = false;
        let pcAudioContext = null;
        let pcMediaStream = null;
        let isPCMicMuted = false;

        async function toggleHandsFreeAudio() {
            isHandsFreeActive = !isHandsFreeActive;
            const btn = document.getElementById('btnToggleHandsFree');
            const micBtn = document.getElementById('btnTogglePCMic');
            const badge = document.getElementById('callAudioActiveBadge');

            if (isHandsFreeActive) {
                try {
                    await fetch('/call/audio?action=START');
                    if (btn) btn.innerText = '⏹️ Hands-Free Modunu Durdur';
                    if (micBtn) micBtn.style.display = 'inline-block';
                    if (badge) {
                        badge.innerText = 'Aktif 🎙️';
                        badge.style.background = 'rgba(16,185,129,0.2)';
                        badge.style.color = '#10B981';
                    }
                    startPCAudioCapture();
                } catch (e) {
                    alert('Ses köprüsü başlatılamadı: ' + e.message);
                }
            } else {
                await fetch('/call/audio?action=STOP');
                if (btn) btn.innerText = '🎙️ PC\'den Konuş (Hands-Free Başlat)';
                if (micBtn) micBtn.style.display = 'none';
                if (badge) {
                    badge.innerText = 'Beklemede ⚪';
                    badge.style.background = 'rgba(255,255,255,0.06)';
                    badge.style.color = 'inherit';
                }
                stopPCAudioCapture();
            }
        }

        function togglePCMic() {
            isPCMicMuted = !isPCMicMuted;
            const micBtn = document.getElementById('btnTogglePCMic');
            if (pcMediaStream) {
                pcMediaStream.getAudioTracks().forEach(function(t) { t.enabled = !isPCMicMuted; });
            }
            if (micBtn) {
                micBtn.innerText = isPCMicMuted ? '🎙️ PC Mikrofonunu Aç' : '🔇 PC Mikrofonunu Sustur';
            }
        }

        async function startPCAudioCapture() {
            try {
                pcAudioContext = new (window.AudioContext || window.webkitAudioContext)({ sampleRate: 16000 });
                pcMediaStream = await navigator.mediaDevices.getUserMedia({ audio: true });
                const source = pcAudioContext.createMediaStreamSource(pcMediaStream);
                const processor = pcAudioContext.createScriptProcessor(2048, 1, 1);

                processor.onaudioprocess = function(e) {
                    if (!isHandsFreeActive || isPCMicMuted) return;
                    const inputData = e.inputBuffer.getChannelData(0);
                    const pcm16 = new Int16Array(inputData.length);
                    for (let i = 0; i < inputData.length; i++) {
                        const s = Math.max(-1, Math.min(1, inputData[i]));
                        pcm16[i] = s < 0 ? s * 0x8000 : s * 0x7FFF;
                    }
                    const uint8 = new Uint8Array(pcm16.buffer);
                    let binary = '';
                    for (let i = 0; i < uint8.byteLength; i++) {
                        binary += String.fromCharCode(uint8[i]);
                    }
                    const b64 = btoa(binary);
                    fetch('/call/audio?action=DATA', {
                        method: 'POST',
                        headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
                        body: 'data=' + encodeURIComponent(b64)
                    }).catch(function() {});
                };

                source.connect(processor);
                processor.connect(pcAudioContext.destination);
            } catch (err) {
                console.log('Mikrofon erişimi:', err);
            }
        }

        function stopPCAudioCapture() {
            if (pcMediaStream) {
                pcMediaStream.getTracks().forEach(function(t) { t.stop(); });
                pcMediaStream = null;
            }
            if (pcAudioContext) {
                try { pcAudioContext.close(); } catch (_) {}
                pcAudioContext = null;
            }
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
