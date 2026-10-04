package macos

import (
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

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
}

func NewClipboardManager() *ClipboardManager {
	initial, _ := GetClipboard()
	return &ClipboardManager{
		lastKnownText: initial,
	}
}

// GetClipboard reads the current text from the macOS pasteboard.
func GetClipboard() (string, error) {
	cmd := exec.Command("pbpaste")
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return string(out), nil
}

// GetClipboardImage checks if the macOS pasteboard contains PNG image data and extracts it.
func GetClipboardImage() ([]byte, error) {
	tmpFile := filepath.Join(os.TempDir(), "android_sync_mac_clip_read.png")
	defer os.Remove(tmpFile)

	script := fmt.Sprintf(`
		try
			set imgData to (the clipboard as «class PNGf»)
			set f to open for access (POSIX file "%s") with write permission
			set eof f to 0
			write imgData to f
			close access f
			return "OK"
		on error
			return "NONE"
		end try
	`, tmpFile)

	cmd := exec.Command("osascript", "-e", script)
	out, err := cmd.Output()
	if err != nil || !strings.Contains(string(out), "OK") {
		return nil, fmt.Errorf("no image on clipboard")
	}

	data, err := os.ReadFile(tmpFile)
	if err != nil || len(data) == 0 {
		return nil, fmt.Errorf("failed to read clipboard image file")
	}
	return data, nil
}

// SetClipboard writes text to the macOS pasteboard.
func (cm *ClipboardManager) SetClipboard(text string) error {
	cm.mu.Lock()
	cm.lastKnownText = text
	cm.mu.Unlock()

	cmd := exec.Command("pbcopy")
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return err
	}

	if err := cmd.Start(); err != nil {
		return err
	}

	if _, err := io.WriteString(stdin, text); err != nil {
		return err
	}
	_ = stdin.Close()

	return cmd.Wait()
}

// SetClipboardImage writes PNG image data to the macOS pasteboard using osascript.
func (cm *ClipboardManager) SetClipboardImage(pngBytes []byte) error {
	cm.mu.Lock()
	cm.lastImageLen = len(pngBytes)
	cm.mu.Unlock()

	tmpFile, err := os.CreateTemp("", "android_sync_clip_*.png")
	if err != nil {
		return err
	}
	defer os.Remove(tmpFile.Name())

	if _, err := tmpFile.Write(pngBytes); err != nil {
		_ = tmpFile.Close()
		return err
	}
	_ = tmpFile.Close()

	script := fmt.Sprintf(`set the clipboard to (read (POSIX file "%s") as «class PNGf»)`, tmpFile.Name())
	cmd := exec.Command("osascript", "-e", script)
	return cmd.Run()
}

// StartWatcher periodically inspects macOS pasteboard for both image and text changes.
func (cm *ClipboardManager) StartWatcher(ctx context.Context, onChange func(item ClipItem)) {
	ticker := time.NewTicker(750 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			// 1. Önce görsel kontrolü
			imgBytes, err := GetClipboardImage()
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

			// 2. Metin kontrolü
			current, err := GetClipboard()
			if err != nil {
				continue
			}

			cm.mu.Lock()
			if current != "" && current != cm.lastKnownText {
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
