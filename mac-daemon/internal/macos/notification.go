package macos

import (
	"fmt"
	"net/http"
	"net/url"
	"os/exec"
	"strings"
	"time"
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
