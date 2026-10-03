package macos

import (
	"context"
	"io"
	"os/exec"
	"sync"
	"time"
)

type ClipboardManager struct {
	mu            sync.Mutex
	lastKnownText string
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

// StartWatcher periodically inspects pbpaste and notifies on change.
func (cm *ClipboardManager) StartWatcher(ctx context.Context, onChange func(text string)) {
	ticker := time.NewTicker(750 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			current, err := GetClipboard()
			if err != nil {
				continue
			}

			cm.mu.Lock()
			if current != "" && current != cm.lastKnownText {
				cm.lastKnownText = current
				cm.mu.Unlock()
				onChange(current)
			} else {
				cm.mu.Unlock()
			}
		}
	}
}
