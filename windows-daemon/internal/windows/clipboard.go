package windows

import (
	"context"
	"encoding/base64"
	"fmt"
	"sync"
	"syscall"
	"time"
	"unsafe"
)

var (
	user32                       = syscall.NewLazyDLL("user32.dll")
	kernel32                     = syscall.NewLazyDLL("kernel32.dll")
	procGetClipboardSeqNum       = user32.NewProc("GetClipboardSequenceNumber")
	procOpenClipboard            = user32.NewProc("OpenClipboard")
	procCloseClipboard           = user32.NewProc("CloseClipboard")
	procEmptyClipboard           = user32.NewProc("EmptyClipboard")
	procGetClipboardData         = user32.NewProc("GetClipboardData")
	procSetClipboardData         = user32.NewProc("SetClipboardData")
	procIsClipboardFormatAvail   = user32.NewProc("IsClipboardFormatAvailable")
	procRegisterClipboardFormatW = user32.NewProc("RegisterClipboardFormatW")
	procGlobalAlloc              = kernel32.NewProc("GlobalAlloc")
	procGlobalLock               = kernel32.NewProc("GlobalLock")
	procGlobalUnlock             = kernel32.NewProc("GlobalUnlock")
	procGlobalSize               = kernel32.NewProc("GlobalSize")
)

const (
	CF_UNICODETEXT = 13
	GMEM_MOVEABLE  = 0x0002
)

var (
	cfPNG uint32
)

func init() {
	pngStr, _ := syscall.UTF16PtrFromString("PNG")
	f, _, _ := procRegisterClipboardFormatW.Call(uintptr(unsafe.Pointer(pngStr)))
	cfPNG = uint32(f)
}

type ClipItem struct {
	Type        string // "text" or "image"
	Text        string
	ImageBase64 string
	MimeType    string
}

type ClipboardManager struct {
	mu            sync.Mutex
	lastKnownText string
	lastImageLen  int
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

// GetClipboardImage reads raw PNG bytes from Windows clipboard if available.
func (cm *ClipboardManager) GetClipboardImage() ([]byte, error) {
	if cfPNG == 0 {
		return nil, fmt.Errorf("PNG formatı desteklenmiyor")
	}

	for attempt := 0; attempt < 3; attempt++ {
		r, _, err := procOpenClipboard.Call(0)
		if r != 0 {
			defer procCloseClipboard.Call()

			avail, _, _ := procIsClipboardFormatAvail.Call(uintptr(cfPNG))
			if avail == 0 {
				return nil, fmt.Errorf("panoda PNG görseli yok")
			}

			hData, _, _ := procGetClipboardData.Call(uintptr(cfPNG))
			if hData == 0 {
				return nil, fmt.Errorf("PNG verisi okunamadı")
			}

			ptr, _, _ := procGlobalLock.Call(hData)
			if ptr == 0 {
				return nil, fmt.Errorf("PNG verisi kilitlenemedi")
			}
			defer procGlobalUnlock.Call(hData)

			size, _, _ := procGlobalSize.Call(hData)
			if size == 0 {
				return nil, fmt.Errorf("PNG boyutu 0")
			}

			data := make([]byte, size)
			copy(data, (*[1 << 28]byte)(unsafe.Pointer(ptr))[:size:size])
			return data, nil
		}
		time.Sleep(20 * time.Millisecond)
		_ = err
	}
	return nil, fmt.Errorf("panoya erişilemedi")
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

			dst := (*[1 << 20]byte)(unsafe.Pointer(ptr))[:bytesCount:bytesCount]
			src := (*[1 << 20]byte)(unsafe.Pointer(&utf16Chars[0]))[:bytesCount:bytesCount]
			copy(dst, src)

			procGlobalUnlock.Call(hMem)
			procSetClipboardData.Call(CF_UNICODETEXT, hMem)
			procCloseClipboard.Call()

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

// SetClipboardImage writes PNG image data to the Windows clipboard.
func (cm *ClipboardManager) SetClipboardImage(pngBytes []byte) error {
	if cfPNG == 0 || len(pngBytes) == 0 {
		return fmt.Errorf("geçersiz PNG görsel verisi")
	}

	cm.mu.Lock()
	cm.lastImageLen = len(pngBytes)
	cm.mu.Unlock()

	for attempt := 0; attempt < 3; attempt++ {
		r, _, _ := procOpenClipboard.Call(0)
		if r != 0 {
			procEmptyClipboard.Call()

			hMem, _, allocErr := procGlobalAlloc.Call(GMEM_MOVEABLE, uintptr(len(pngBytes)))
			if hMem == 0 {
				procCloseClipboard.Call()
				return fmt.Errorf("global alloc failed: %w", allocErr)
			}

			ptr, _, lockErr := procGlobalLock.Call(hMem)
			if ptr == 0 {
				procCloseClipboard.Call()
				return fmt.Errorf("global lock failed: %w", lockErr)
			}

			dst := (*[1 << 28]byte)(unsafe.Pointer(ptr))[:len(pngBytes):len(pngBytes)]
			copy(dst, pngBytes)

			procGlobalUnlock.Call(hMem)
			procSetClipboardData.Call(uintptr(cfPNG), hMem)
			procCloseClipboard.Call()

			seq, _, _ := procGetClipboardSeqNum.Call()
			cm.mu.Lock()
			cm.lastSeqNum = seq
			cm.mu.Unlock()
			return nil
		}
		time.Sleep(20 * time.Millisecond)
	}
	return fmt.Errorf("panoya görsel yazılamadı")
}

// StartWatcher inspects the Windows clipboard sequence number and notifies on change (both text & image).
func (cm *ClipboardManager) StartWatcher(ctx context.Context, onChange func(item ClipItem)) {
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

			// 1. Önce panoda bir görsel (PNG) olup olmadığını kontrol et
			if cfPNG != 0 {
				imgBytes, err := cm.GetClipboardImage()
				if err == nil && len(imgBytes) > 0 {
					cm.mu.Lock()
					isSame := len(imgBytes) == cm.lastImageLen
					if !isSame {
						cm.lastImageLen = len(imgBytes)
					}
					cm.mu.Unlock()

					if !isSame {
						b64 := base64.StdEncoding.EncodeToString(imgBytes)
						onChange(ClipItem{
							Type:        "image",
							ImageBase64: b64,
							MimeType:    "image/png",
						})
						continue
					}
				}
			}

			// 2. Görsel yoksa metin kontrolü yap
			current, err := cm.GetClipboard()
			if err != nil || current == "" {
				continue
			}

			cm.mu.Lock()
			if current != cm.lastKnownText {
				cm.lastKnownText = current
				cm.mu.Unlock()
				onChange(ClipItem{
					Type: "text",
					Text: current,
				})
			} else {
				cm.mu.Unlock()
			}
		}
	}
}
