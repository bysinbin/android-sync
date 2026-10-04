package macos

import (
	"fmt"
	"log"
	"os/exec"
	"strings"
	"time"

	"mac-sync/internal/protocol"
)


// HandleTouchpadEvent processes touchpad gestures and presentation hotkeys on macOS.
func HandleTouchpadEvent(p protocol.TouchpadEventPayload) {
	switch strings.ToLower(p.Type) {
	case "click":
		switch strings.ToLower(p.Button) {
		case "left", "":
			_ = exec.Command("osascript", "-e", `tell application "System Events" to click`).Run()
		case "right":
			_ = exec.Command("osascript", "-e", `tell application "System Events" to key code 0 using control down`).Run()
		}
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


