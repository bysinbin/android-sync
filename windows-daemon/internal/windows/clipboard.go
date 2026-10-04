package windows

import (
	"context"
	"fmt"
	"sync"
	"syscall"
	"time"
	"unsafe"
)

var (
	user32                 = syscall.NewLazyDLL("user32.dll")
	kernel32               = syscall.NewLazyDLL("kernel32.dll")
	procGetClipboardSeqNum = user32.NewProc("GetClipboardSequenceNumber")
	procOpenClipboard      = user32.NewProc("OpenClipboard")
	procCloseClipboard     = user32.NewProc("CloseClipboard")
	procEmptyClipboard     = user32.NewProc("EmptyClipboard")
	procGetClipboardData   = user32.NewProc("GetClipboardData")
	procSetClipboardData   = user32.NewProc("SetClipboardData")
	procGlobalAlloc        = kernel32.NewProc("GlobalAlloc")
	procGlobalLock         = kernel32.NewProc("GlobalLock")
	procGlobalUnlock       = kernel32.NewProc("GlobalUnlock")
)

const (
	CF_UNICODETEXT = 13
	GMEM_MOVEABLE  = 0x0002
)

type ClipboardManager struct {
	mu            sync.Mutex
	lastKnownText string
	lastSeqNum    uintptr
}

func NewClipboardManager() *ClipboardManager {
	cm := &ClipboardManager{}
	initial, _ := cm.GetClipboard()
	cm.lastKnownText = initial
	seq, _, _ := procGetClipboardSeqNum.Call()
	cm.lastSeqNum = seq
	return cm
}

// GetClipboard reads the current Unicode text from the Windows clipboard.
func (cm *ClipboardManager) GetClipboard() (string, error) {
	// Retry up to 3 times in case another app holds the clipboard
	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		r, _, err := procOpenClipboard.Call(0)
		if r != 0 {
			defer procCloseClipboard.Call()

			hData, _, err := procGetClipboardData.Call(CF_UNICODETEXT)
			if hData == 0 {
				return "", err
			}

			ptr, _, err := procGlobalLock.Call(hData)
			if ptr == 0 {
				return "", err
			}
			defer procGlobalUnlock.Call(hData)

			text := syscall.UTF16ToString((*[1 << 20]uint16)(unsafe.Pointer(ptr))[:])
			return text, nil
		}
		lastErr = err
		time.Sleep(20 * time.Millisecond)
	}
	return "", fmt.Errorf("panoya erişilemedi: %w", lastErr)
}

// SetClipboard writes Unicode text to the Windows clipboard.
func (cm *ClipboardManager) SetClipboard(text string) error {
	cm.mu.Lock()
	cm.lastKnownText = text
	cm.mu.Unlock()

	utf16Chars, err := syscall.UTF16FromString(text)
	if err != nil {
		return err
	}
	bytesCount := len(utf16Chars) * 2

	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		r, _, err := procOpenClipboard.Call(0)
		if r != 0 {
			procEmptyClipboard.Call()

			hMem, _, allocErr := procGlobalAlloc.Call(GMEM_MOVEABLE, uintptr(bytesCount))
			if hMem == 0 {
				procCloseClipboard.Call()
				return fmt.Errorf("global alloc failed: %w", allocErr)
			}

			ptr, _, lockErr := procGlobalLock.Call(hMem)
			if ptr == 0 {
				procCloseClipboard.Call()
				return fmt.Errorf("global lock failed: %w", lockErr)
			}

			// Copy UTF-16 bytes into locked global memory
			dst := (*[1 << 20]byte)(unsafe.Pointer(ptr))[:bytesCount:bytesCount]
			src := (*[1 << 20]byte)(unsafe.Pointer(&utf16Chars[0]))[:bytesCount:bytesCount]
			copy(dst, src)

			procGlobalUnlock.Call(hMem)
			procSetClipboardData.Call(CF_UNICODETEXT, hMem)
			procCloseClipboard.Call()

			// Update sequence number so watcher ignores our own write
			seq, _, _ := procGetClipboardSeqNum.Call()
			cm.mu.Lock()
			cm.lastSeqNum = seq
			cm.mu.Unlock()
			return nil
		}
		lastErr = err
		time.Sleep(20 * time.Millisecond)
	}
	return fmt.Errorf("panoya yazılamadı: %w", lastErr)
}

// StartWatcher inspects the Windows clipboard sequence number and notifies on change.
func (cm *ClipboardManager) StartWatcher(ctx context.Context, onChange func(text string)) {
	ticker := time.NewTicker(400 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			seq, _, _ := procGetClipboardSeqNum.Call()
			cm.mu.Lock()
			if seq == cm.lastSeqNum {
				cm.mu.Unlock()
				continue
			}
			cm.lastSeqNum = seq
			cm.mu.Unlock()

			current, err := cm.GetClipboard()
			if err != nil || current == "" {
				continue
			}

			cm.mu.Lock()
			if current != cm.lastKnownText {
				cm.lastKnownText = current
				cm.mu.Unlock()
				onChange(current)
			} else {
				cm.mu.Unlock()
			}
		}
	}
}
