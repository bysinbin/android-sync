package windows

import (
	"log"
	"strings"
	"sync"
	"time"
	"unsafe"

	"windows-sync/internal/protocol"
)

var (
	procMouseEvent   = user32.NewProc("mouse_event")
	procSetCursorPos = user32.NewProc("SetCursorPos")

	cursorMu    sync.Mutex
	accumX      float64
	accumY      float64
	scrollAccum float64
)



const (
	MOUSEEVENTF_MOVE       = 0x0001
	MOUSEEVENTF_LEFTDOWN   = 0x0002
	MOUSEEVENTF_LEFTUP     = 0x0004
	MOUSEEVENTF_RIGHTDOWN  = 0x0008
	MOUSEEVENTF_RIGHTUP    = 0x0010
	MOUSEEVENTF_MIDDLEDOWN = 0x0020
	MOUSEEVENTF_MIDDLEUP   = 0x0040
	MOUSEEVENTF_WHEEL      = 0x0800

	VK_BACK   = 0x08
	VK_TAB    = 0x09
	VK_RETURN = 0x0D
	VK_ESCAPE = 0x1B
	VK_SPACE  = 0x20
	VK_PRIOR  = 0x21 // Page Up
	VK_NEXT   = 0x22 // Page Down
	VK_END    = 0x23
	VK_HOME   = 0x24
	VK_LEFT   = 0x25
	VK_UP     = 0x26
	VK_RIGHT  = 0x27
	VK_DOWN   = 0x28
	VK_F5     = 0x74
	VK_F11    = 0x7A
)

// HandleTouchpadEvent processes mouse movement, clicks, scrolling, or presentation hotkeys.
func HandleTouchpadEvent(p protocol.TouchpadEventPayload) {
	switch strings.ToLower(p.Type) {
	case "move":
		cursorMu.Lock()
		accumX += float64(p.DX)
		accumY += float64(p.DY)

		moveX := int32(accumX)
		moveY := int32(accumY)

		if moveX != 0 || moveY != 0 {
			accumX -= float64(moveX)
			accumY -= float64(moveY)

			var pt POINT
			r, _, _ := procGetCursorPos.Call(uintptr(unsafe.Pointer(&pt)))
			if r != 0 {
				// SetCursorPos is 100% reliable across RDP, virtual displays, and physical console
				procSetCursorPos.Call(uintptr(pt.X+moveX), uintptr(pt.Y+moveY))
			} else {
				procMouseEvent.Call(MOUSEEVENTF_MOVE, uintptr(moveX), uintptr(moveY), 0, 0)
			}
		}
		cursorMu.Unlock()

	case "click":
		switch strings.ToLower(p.Button) {
		case "left", "":
			procMouseEvent.Call(MOUSEEVENTF_LEFTDOWN, 0, 0, 0, 0)
			time.Sleep(15 * time.Millisecond)
			procMouseEvent.Call(MOUSEEVENTF_LEFTUP, 0, 0, 0, 0)
		case "right":
			procMouseEvent.Call(MOUSEEVENTF_RIGHTDOWN, 0, 0, 0, 0)
			time.Sleep(15 * time.Millisecond)
			procMouseEvent.Call(MOUSEEVENTF_RIGHTUP, 0, 0, 0, 0)
		case "middle":
			procMouseEvent.Call(MOUSEEVENTF_MIDDLEDOWN, 0, 0, 0, 0)
			time.Sleep(15 * time.Millisecond)
			procMouseEvent.Call(MOUSEEVENTF_MIDDLEUP, 0, 0, 0, 0)
		}
	case "scroll":
		cursorMu.Lock()
		amount := float64(p.ScrollY)
		if amount == 0 {
			amount = float64(p.DY) * 20.0
		}
		scrollAccum += amount

		for scrollAccum >= 50 {
			procMouseEvent.Call(MOUSEEVENTF_WHEEL, 0, 0, uintptr(120), 0)
			scrollAccum -= 50
		}
		for scrollAccum <= -50 {
			procMouseEvent.Call(MOUSEEVENTF_WHEEL, 0, 0, uintptr(0xFFFFFF88), 0)
			scrollAccum += 50
		}

		cursorMu.Unlock()

	case "key":
		k := strings.ToUpper(strings.TrimSpace(p.Key))
		var vk byte
		switch k {
		case "LEFT":
			vk = VK_LEFT
		case "RIGHT":
			vk = VK_RIGHT
		case "UP":
			vk = VK_UP
		case "DOWN":
			vk = VK_DOWN
		case "F5":
			vk = VK_F5
		case "ESC", "ESCAPE":
			vk = VK_ESCAPE
		case "ENTER", "RETURN":
			vk = VK_RETURN
		case "SPACE":
			vk = VK_SPACE
		case "PAGE_UP", "PGUP":
			vk = VK_PRIOR
		case "PAGE_DOWN", "PGDN":
			vk = VK_NEXT
		case "VOL_UP":
			vk = VK_VOLUME_UP
		case "VOL_DOWN":
			vk = VK_VOLUME_DOWN
		case "MUTE":
			vk = VK_VOLUME_MUTE
		case "PLAY_PAUSE":
			vk = VK_MEDIA_PLAY_PAUSE
		default:
			log.Printf("[Touchpad] Bilinmeyen tuş: %s", p.Key)
			return
		}
		sendVirtualKey(vk)
	}
}

// HandleBiometricUnlock wakes up the screen and unlocks Windows if authenticated.
func HandleBiometricUnlock(p protocol.BiometricUnlockPayload) {
	if strings.ToUpper(p.Status) != "AUTHENTICATED" {
		log.Printf("[Biometric] Kilit açma yetkisiz veya reddedildi: status=%s", p.Status)
		return
	}

	log.Printf("[Biometric] Biyometrik kilit açma isteği alındı (PIN uzunluğu: %d)", len(p.UnlockPin))

	// 1. Ekranı uyandır ve kilit perdesini kaldır (Space tuşu)
	sendVirtualKey(VK_SPACE)
	time.Sleep(120 * time.Millisecond)
	sendVirtualKey(VK_SPACE)

	// 2. Eğer PIN/Şifre varsa, kilit kutusunun odağı alması için kısa bir süre bekle ve yaz
	if p.UnlockPin != "" {
		time.Sleep(350 * time.Millisecond) // Lockscreen animasyon süresi

		for _, ch := range p.UnlockPin {
			// KEYEVENTF_UNICODE = 0x0004, KEYEVENTF_KEYUP = 0x0002
			procKeybdEvent.Call(0, uintptr(ch), 0x0004, 0)
			time.Sleep(15 * time.Millisecond)
			procKeybdEvent.Call(0, uintptr(ch), 0x0004|KEYEVENTF_KEYUP, 0)
			time.Sleep(30 * time.Millisecond)
		}

		time.Sleep(100 * time.Millisecond)
		// Enter tuşu ile gönder
		sendVirtualKey(VK_RETURN)
	}

	// 3. Kullanıcıya bildirim göster
	go ShowToast(
		"Biyometrik Kilit Açıldı 🔓",
		"Telefonunuzun parmak izi/yüz tanımasıyla Windows kilidi başarıyla açıldı.",
		"Android Sync",
	)
}


