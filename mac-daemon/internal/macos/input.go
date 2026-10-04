package macos

import (
	"fmt"
	"log"
	"net/http"
	"os/exec"
	"strings"
	"time"

	"mac-sync/internal/protocol"
)


var (
	macCursorClient = &http.Client{Timeout: 80 * time.Millisecond}
)

// HandleTouchpadEvent processes touchpad gestures, air mouse movement, and presentation hotkeys on macOS.
func HandleTouchpadEvent(p protocol.TouchpadEventPayload) {
	switch strings.ToLower(p.Type) {
	case "move":
		go func(x, y float64) {
			u := fmt.Sprintf("http://127.0.0.1:42426/mouse?action=move&dx=%.2f&dy=%.2f", x, y)
			if _, err := macCursorClient.Get(u); err != nil {
				_ = exec.Command("cliclick", fmt.Sprintf("m:%+d,%+d", int(x), int(y))).Start()
			}
		}(float64(p.DX), float64(p.DY))

	case "click":
		btn := strings.ToLower(p.Button)
		if btn == "" {
			btn = "left"
		}
		go func(b string) {
			u := fmt.Sprintf("http://127.0.0.1:42426/mouse?action=click&button=%s", b)
			if _, err := macCursorClient.Get(u); err != nil {
				if b == "right" {
					_ = exec.Command("osascript", "-e", `tell application "System Events" to key code 0 using control down`).Run()
				} else {
					_ = exec.Command("osascript", "-e", `tell application "System Events" to click`).Run()
				}
			}
		}(btn)

	case "scroll":
		sy := p.ScrollY
		go func(s int) {
			u := fmt.Sprintf("http://127.0.0.1:42426/mouse?action=scroll&dy=%d", s)
			_, _ = macCursorClient.Get(u)
		}(sy)

	case "key":
		k := strings.ToUpper(strings.TrimSpace(p.Key))
		var keyCode int
		switch k {
		case "LEFT":
			keyCode = 123 // Left arrow
		case "RIGHT":
			keyCode = 124 // Right arrow
		case "DOWN":
			keyCode = 125 // Down arrow
		case "UP":
			keyCode = 126 // Up arrow
		case "RETURN", "ENTER":
			keyCode = 36 // Return
		case "SPACE":
			keyCode = 49 // Space
		case "ESC", "ESCAPE":
			keyCode = 53 // Escape
		case "F5":
			// In Keynote / PowerPoint on Mac: Cmd+Option+P or Cmd+Shift+Enter
			_ = exec.Command("osascript", "-e", `tell application "System Events" to keystroke "p" using {command down, option down}`).Run()
			return
		case "VOL_UP":
			_ = exec.Command("osascript", "-e", `set volume output volume ((output volume of (get volume settings)) + 6)`).Run()
			return
		case "VOL_DOWN":
			_ = exec.Command("osascript", "-e", `set volume output volume ((output volume of (get volume settings)) - 6)`).Run()
			return
		case "MUTE":
			_ = exec.Command("osascript", "-e", `set volume output muted not (output muted of (get volume settings))`).Run()
			return
		case "PLAY_PAUSE":
			_ = exec.Command("osascript", "-e", `tell application "System Events" to key code 49`).Run()
			return
		default:
			log.Printf("[Touchpad macOS] Bilinmeyen tuş: %s", p.Key)
			return
		}
		if keyCode > 0 {
			script := fmt.Sprintf(`tell application "System Events" to key code %d`, keyCode)
			_ = exec.Command("osascript", "-e", script).Run()
		}
	}
}

// HandleBiometricUnlock wakes macOS display and enters password if authenticated.
func HandleBiometricUnlock(p protocol.BiometricUnlockPayload) {
	if strings.ToUpper(p.Status) != "AUTHENTICATED" {
		log.Printf("[Biometric macOS] Kilit açma yetkisiz veya reddedildi: status=%s", p.Status)
		return
	}

	log.Printf("[Biometric macOS] Biyometrik kilit açma isteği alındı (PIN uzunluğu: %d)", len(p.UnlockPin))

	// 1. Ekranı uyandır
	_ = exec.Command("caffeinate", "-u", "-t", "2").Run()
	_ = exec.Command("osascript", "-e", `tell application "System Events" to key code 49`).Run() // Space

	if p.UnlockPin != "" {
		time.Sleep(350 * time.Millisecond)
		safePin := strings.ReplaceAll(p.UnlockPin, `\`, `\\`)
		safePin = strings.ReplaceAll(safePin, `"`, `\"`)
		script := fmt.Sprintf(`tell application "System Events" to keystroke "%s" & return`, safePin)
		_ = exec.Command("osascript", "-e", script).Run()
	}

	go ShowNotification(
		"Biyometrik Kilit Açıldı 🔓",
		"",
		"Telefonunuzun parmak izi/yüz tanımasıyla Mac kilidiniz başarıyla açıldı.",
		"Glass",
	)
}


