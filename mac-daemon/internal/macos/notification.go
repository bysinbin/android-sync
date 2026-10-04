package macos

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"mac-sync/internal/protocol"
)

// ShowNotification displays a native macOS system notification using osascript.
func ShowNotification(title, subtitle, message, soundName string) error {
	// Escape quotes and backslashes for AppleScript
	escape := func(s string) string {
		s = strings.ReplaceAll(s, "\\", "\\\\")
		s = strings.ReplaceAll(s, "\"", "\\\"")
		return s
	}

	title = escape(title)
	subtitle = escape(subtitle)
	message = escape(message)

	script := fmt.Sprintf(`display notification "%s" with title "%s"`, message, title)
	if subtitle != "" {
		script += fmt.Sprintf(` subtitle "%s"`, subtitle)
	}
	if soundName != "" {
		script += fmt.Sprintf(` sound name "%s"`, soundName)
	}

	cmd := exec.Command("osascript", "-e", script)
	return cmd.Run()
}

// ShowCallAlert notifies the user of an incoming phone call.
func ShowCallAlert(callerName, phoneNumber string) error {
	display := callerName
	if display == "" {
		display = phoneNumber
	}
	if display == "" {
		display = "Bilinmeyen Numara"
	}

	subtitle := "Gelen Arama..."
	if callerName != "" && phoneNumber != "" {
		subtitle = phoneNumber
	}

	// 1. Notify MacSync.app via IPC (Port 42426)
	go func() {
		client := &http.Client{Timeout: 800 * time.Millisecond}
		u := fmt.Sprintf("http://127.0.0.1:42426/call?state=RINGING&caller=%s&number=%s",
			url.QueryEscape(display), url.QueryEscape(phoneNumber))
		_, _ = client.Get(u)
	}()

	return ShowNotification("📞 Telefon Çalıyor", subtitle, display, "Glass")
}

// DismissCallAlert notifies MacSync.app that the call ended.
func DismissCallAlert() {
	go func() {
		client := &http.Client{Timeout: 800 * time.Millisecond}
		_, _ = client.Get("http://127.0.0.1:42426/call?state=IDLE")
	}()
}

// ShowSmsAlert notifies the user of an incoming SMS message.
func ShowSmsAlert(sender, body string) error {
	display := sender
	if display == "" {
		display = "Bilinmeyen Numara"
	}
	// Notify MacSync.app via IPC (Port 42426)
	go func() {
		client := &http.Client{Timeout: 800 * time.Millisecond}
		u := fmt.Sprintf("http://127.0.0.1:42426/sms?sender=%s&body=%s",
			url.QueryEscape(display), url.QueryEscape(body))
		_, _ = client.Get(u)
	}()
	return ShowNotification("💬 Yeni Mesaj ("+display+")", "", body, "Ping")
}

func getMacNotificationDBPath() string {
	out, err := exec.Command("getconf", "DARWIN_USER_DIR").Output()
	if err == nil {
		p := filepath.Join(strings.TrimSpace(string(out)), "0", "com.apple.notificationcenter", "db2", "db")
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	if tmp := os.Getenv("TMPDIR"); tmp != "" {
		parent := filepath.Dir(strings.TrimRight(tmp, "/\\"))
		p := filepath.Join(parent, "0", "com.apple.notificationcenter", "db2", "db")
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return ""
}

func mapMacBundleIdToAppName(bundleId string) string {
	mapping := map[string]string{
		"com.apple.MobileSMS":               "Mesajlar (iMessage)",
		"com.apple.mail":                    "Mail",
		"net.whatsapp.WhatsApp":             "WhatsApp",
		"com.tdesktop.Telegram":             "Telegram",
		"ru.keepcoder.Telegram":             "Telegram",
		"com.tinyspeck.slackmacgap":         "Slack",
		"com.google.Chrome":                 "Google Chrome",
		"com.apple.Safari":                  "Safari",
		"com.spotify.client":                "Spotify",
		"com.apple.Music":                   "Apple Music",
		"com.apple.reminders":               "Anımsatıcılar",
		"com.apple.iCal":                    "Takvim",
		"com.microsoft.Outlook":             "Outlook",
		"com.microsoft.teams2":              "Microsoft Teams",
		"com.hnc.Discord":                   "Discord",
		"org.whispersystems.signal-desktop": "Signal",
	}
	if name, ok := mapping[bundleId]; ok {
		return name
	}
	if bundleId == "" {
		return "macOS"
	}
	parts := strings.Split(bundleId, ".")
	last := parts[len(parts)-1]
	if len(last) > 2 {
		return last
	}
	return bundleId
}

func extractNotificationText(data []byte) (string, string) {
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		return "", ""
	}
	title := ""
	body := ""

	if req, ok := m["req"].(map[string]any); ok {
		if t, ok := req["titl"].(string); ok && t != "" {
			title = t
		} else if t, ok := req["title"].(string); ok && t != "" {
			title = t
		}
		if b, ok := req["body"].(string); ok && b != "" {
			body = b
		} else if b, ok := req["message"].(string); ok && b != "" {
			body = b
		}
		if title == "" {
			if s, ok := req["subt"].(string); ok && s != "" {
				title = s
			}
		}
	}

	if title == "" {
		if t, ok := m["titl"].(string); ok && t != "" {
			title = t
		} else if t, ok := m["title"].(string); ok && t != "" {
			title = t
		}
	}
	if body == "" {
		if b, ok := m["body"].(string); ok && b != "" {
			body = b
		} else if b, ok := m["message"].(string); ok && b != "" {
			body = b
		}
	}
	return title, body
}

// StartMacNotificationListener monitors macOS Notification Center SQLite database in real time
// and forwards all incoming native Mac notifications (WhatsApp, Telegram, Mail, Messages etc.) to the phone.
func StartMacNotificationListener(ctx context.Context, onNotification func(protocol.NotificationPayload)) {
	go func() {
		log.Println("[Bildirim] 🍏 Mac bildirim dinleyicisi başlatılıyor...")
		dbPath := getMacNotificationDBPath()
		if dbPath == "" {
			log.Println("[Bildirim] ⚠️ Mac Notification Center veritabanı bulunamadı. IPC modu devrede.")
			return
		}

		var lastRecID int64 = 0
		// Seed max existing rec_id to avoid spamming past notifications
		seedOut, err := exec.Command("sqlite3", "-readonly", dbPath, "SELECT COALESCE(MAX(rec_id), 0) FROM record;").Output()
		if err == nil {
			_, _ = fmt.Sscanf(strings.TrimSpace(string(seedOut)), "%d", &lastRecID)
			log.Printf("[Bildirim] ✅ Mac bildirim veritabanı bağlandı. Başlangıç ID: %d", lastRecID)
		}

		ticker := time.NewTicker(800 * time.Millisecond)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				q := fmt.Sprintf("SELECT r.rec_id, COALESCE(a.identifier, ''), hex(r.data) FROM record r LEFT JOIN app a ON r.app_id = a.app_id WHERE r.rec_id > %d ORDER BY r.rec_id ASC LIMIT 15;", lastRecID)
				out, err := exec.Command("sqlite3", "-readonly", "-separator", "|||", dbPath, q).Output()
				if err != nil {
					continue
				}

				lines := strings.Split(strings.TrimSpace(string(out)), "\n")
				for _, line := range lines {
					if line == "" {
						continue
					}
					parts := strings.Split(line, "|||")
					if len(parts) < 3 {
						continue
					}

					var recID int64
					_, _ = fmt.Sscanf(strings.TrimSpace(parts[0]), "%d", &recID)
					if recID > lastRecID {
						lastRecID = recID
					}

					bundleID := strings.TrimSpace(parts[1])
					hexData := strings.TrimSpace(parts[2])

					if strings.Contains(bundleID, "com.sync") || strings.Contains(bundleID, "MacSync") || strings.Contains(bundleID, "android-sync") {
						continue
					}

					rawBytes, err := hex.DecodeString(hexData)
					if err != nil || len(rawBytes) == 0 {
						continue
					}

					// Convert binary plist to JSON using macOS built-in plutil
					cmd := exec.Command("plutil", "-convert", "json", "-o", "-", "-")
					cmd.Stdin = bytes.NewReader(rawBytes)
					jsonBytes, err := cmd.Output()
					if err != nil {
						continue
					}

					title, body := extractNotificationText(jsonBytes)
					if title == "" && body == "" {
						continue
					}

					appName := mapMacBundleIdToAppName(bundleID)
					if title == "" {
						title = appName
					}

					payload := protocol.NotificationPayload{
						ID:          fmt.Sprintf("mac_%d", recID),
						PackageName: bundleID,
						AppName:     appName,
						Title:       title,
						Text:        body,
						Timestamp:   time.Now().UnixMilli(),
					}

					if onNotification != nil {
						onNotification(payload)
					}
				}
			}
		}
	}()
}


