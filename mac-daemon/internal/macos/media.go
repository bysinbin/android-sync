package macos

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"mac-sync/internal/protocol"
)

// sendGlobalMediaKey sends an official macOS MediaRemote command using mediactl.
func sendGlobalMediaKey(action string) error {
	candidates := []string{
		"./mediactl",
		"./mac-daemon/mediactl",
		"mediactl",
	}

	if exe, err := os.Executable(); err == nil {
		candidates = append(candidates, filepath.Join(filepath.Dir(exe), "mediactl"))
	}

	for _, p := range candidates {
		if _, err := os.Stat(p); err == nil {
			cmd := exec.Command(p, strings.ToLower(action))
			return cmd.Run()
		}
	}

	cmd := exec.Command("/Users/feritetem/Desktop/android-mac-sync/mediactl", strings.ToLower(action))
	return cmd.Run()
}

// tryBrowserControl attempts to find and control any open YouTube or web media tab.
func tryBrowserControl(action string) bool {
	// Google Chrome YouTube & Web Controller
	chromeScript := fmt.Sprintf(`
		tell application "System Events"
			if not (exists (process "Google Chrome")) then return false
		end tell
		tell application "Google Chrome"
			try
				repeat with w in windows
					repeat with t in tabs of w
						set u to URL of t
						if u contains "youtube.com" or u contains "music.youtube.com" or u contains "soundcloud.com" then
							if "%s" is "PLAY_PAUSE" then
								execute t javascript "var v = document.querySelector('video') || document.querySelector('audio'); if(v){ v.paused ? v.play() : v.pause(); } else { var btn = document.querySelector('.ytp-play-button'); if(btn) btn.click(); }"
							else if "%s" is "NEXT" then
								execute t javascript "var btn = document.querySelector('.ytp-next-button'); if(btn) btn.click();"
							else if "%s" is "PREVIOUS" then
								execute t javascript "var btn = document.querySelector('.ytp-prev-button'); if(btn) btn.click(); else history.back();"
							end if
							return true
						end if
					end repeat
				end repeat
			end try
		end tell
		return false
	`, action, action, action)

	out, err := exec.Command("osascript", "-e", chromeScript).Output()
	if err == nil && strings.TrimSpace(string(out)) == "true" {
		return true
	}

	// Safari YouTube & Web Controller
	safariScript := fmt.Sprintf(`
		tell application "System Events"
			if not (exists (process "Safari")) then return false
		end tell
		tell application "Safari"
			try
				repeat with w in windows
					repeat with t in tabs of w
						set u to URL of t
						if u contains "youtube.com" or u contains "music.youtube.com" or u contains "soundcloud.com" then
							if "%s" is "PLAY_PAUSE" then
								do JavaScript "var v = document.querySelector('video') || document.querySelector('audio'); if(v){ v.paused ? v.play() : v.pause(); } else { var btn = document.querySelector('.ytp-play-button'); if(btn) btn.click(); }" in t
							else if "%s" is "NEXT" then
								do JavaScript "var btn = document.querySelector('.ytp-next-button'); if(btn) btn.click();" in t
							else if "%s" is "PREVIOUS" then
								do JavaScript "var btn = document.querySelector('.ytp-prev-button'); if(btn) btn.click(); else history.back();" in t
							end if
							return true
						end if
					end repeat
				end repeat
			end try
		end tell
		return false
	`, action, action, action)

	outSafari, errSafari := exec.Command("osascript", "-e", safariScript).Output()
	if errSafari == nil && strings.TrimSpace(string(outSafari)) == "true" {
		return true
	}

	return false
}

// ExecuteMediaAction handles playback controls on macOS.
func ExecuteMediaAction(action string) error {
	action = strings.ToUpper(action)

	// 1. Try sending to MacSync.app menu bar process via local IPC (Port 42426)
	// Because MacSync.app has macOS Accessibility and TCC permissions, this executes with full system trust!
	client := http.Client{Timeout: 300 * time.Millisecond}
	resp, err := client.Get(fmt.Sprintf("http://127.0.0.1:42426/media?action=%s", url.QueryEscape(action)))
	if err == nil {
		_ = resp.Body.Close()
		if resp.StatusCode == http.StatusOK {
			return nil
		}
	}

	switch action {
	case "VOLUME_UP":
		script := `set volume output volume ((output volume of (get volume settings)) + 6)`
		_ = exec.Command("osascript", "-e", script).Run()
		return sendGlobalMediaKey("VOLUME_UP")

	case "VOLUME_DOWN":
		script := `set volume output volume ((output volume of (get volume settings)) - 6)`
		_ = exec.Command("osascript", "-e", script).Run()
		return sendGlobalMediaKey("VOLUME_DOWN")

	case "PLAY_PAUSE", "PLAY", "PAUSE", "NEXT", "PREVIOUS":
		// Direct system-wide media key dispatch (Controls Spotify, YouTube in browser, Apple Music, VLC, etc.)
		return sendGlobalMediaKey(action)

	case "SEEK_FORWARD", "FORWARD", "FORWARD_15":
		return ExecuteMediaSeekRelative(15)

	case "SEEK_BACKWARD", "REWIND", "REWIND_15":
		return ExecuteMediaSeekRelative(-15)

	default:
		return fmt.Errorf("unknown media action: %s", action)
	}
}

// ExecuteMediaSeekRelative seeks the current media on macOS by +/- seconds.
func ExecuteMediaSeekRelative(deltaSec int) error {
	client := http.Client{Timeout: 500 * time.Millisecond}
	actionName := "SEEK_FORWARD"
	if deltaSec < 0 {
		actionName = "SEEK_BACKWARD"
	}
	resp, err := client.Get(fmt.Sprintf("http://127.0.0.1:42426/media?action=%s&seconds=%d", actionName, int(math.Abs(float64(deltaSec)))))
	if err == nil {
		_ = resp.Body.Close()
		if resp.StatusCode == http.StatusOK {
			return nil
		}
	}

	sec := deltaSec
	script := fmt.Sprintf(`
		try
			tell application "Spotify" to if player state is playing then set player position to ((player position) + %d)
		end try
		try
			tell application "Music" to if player state is playing then set player position to ((player position) + %d)
		end try
		try
			tell application "Google Chrome"
				repeat with w in windows
					repeat with t in tabs of w
						try
							execute t javascript "var v=document.querySelector('video')||document.querySelector('audio'); if(v) v.currentTime += %d;"
						end try
					end repeat
				end repeat
			end tell
		end try
		try
			tell application "Safari"
				repeat with w in windows
					repeat with t in tabs of w
						try
							do JavaScript "var v=document.querySelector('video')||document.querySelector('audio'); if(v) v.currentTime += %d;" in t
						end try
					end repeat
				end repeat
			end tell
		end try
	`, sec, sec, sec, sec)
	return exec.Command("osascript", "-e", script).Run()
}

// ExecuteMediaSeekPercent seeks the current media on macOS to a specific percentage (0.0 to 100.0).
func ExecuteMediaSeekPercent(percent float64) error {
	client := http.Client{Timeout: 500 * time.Millisecond}
	resp, err := client.Get(fmt.Sprintf("http://127.0.0.1:42426/media?action=SEEK_PERCENT&percent=%.2f", percent))
	if err == nil {
		_ = resp.Body.Close()
		if resp.StatusCode == http.StatusOK {
			return nil
		}
	}

	p := percent
	if p < 0 {
		p = 0
	} else if p > 100 {
		p = 100
	}

	script := fmt.Sprintf(`
		try
			tell application "Spotify" to if player state is playing then set player position to ((duration of current track / 1000) * %f / 100)
		end try
		try
			tell application "Music" to if player state is playing then set player position to ((duration of current track) * %f / 100)
		end try
		try
			tell application "Google Chrome"
				repeat with w in windows
					repeat with t in tabs of w
						try
							execute t javascript "var v=document.querySelector('video')||document.querySelector('audio'); if(v && v.duration) v.currentTime = v.duration * (%f / 100);"
						end try
					end repeat
				end repeat
			end tell
		end try
		try
			tell application "Safari"
				repeat with w in windows
					repeat with t in tabs of w
						try
							do JavaScript "var v=document.querySelector('video')||document.querySelector('audio'); if(v && v.duration) v.currentTime = v.duration * (%f / 100);" in t
						end try
					end repeat
				end repeat
			end tell
		end try
	`, p, p, p, p)
	return exec.Command("osascript", "-e", script).Run()
}

// PauseAllMedia pauses any active media player (e.g. on incoming phone call).
func PauseAllMedia() {
	client := http.Client{Timeout: 300 * time.Millisecond}
	resp, err := client.Get("http://127.0.0.1:42426/media?action=PAUSE")
	if err == nil {
		_ = resp.Body.Close()
		return
	}
	_ = sendGlobalMediaKey("PAUSE")
}

// GetCurrentTrack returns what is currently playing on macOS (if Music.app is running).
func GetCurrentTrack() (*protocol.MediaInfoPayload, error) {
	script := `
		tell application "System Events"
			if not (exists (process "Music")) then return "NOT_RUNNING"
		end tell
		tell application "Music"
			set pState to (player state as string)
			if pState is "playing" then
				set tName to name of current track
				set tArtist to artist of current track
				set tAlbum to album of current track
				return "PLAYING::" & tName & "::" & tArtist & "::" & tAlbum
			else
				return "STOPPED"
			end if
		end tell
	`
	out, err := exec.Command("osascript", "-e", script).Output()
	if err != nil {
		return nil, err
	}
	res := strings.TrimSpace(string(out))
	if strings.HasPrefix(res, "PLAYING::") {
		parts := strings.Split(res, "::")
		if len(parts) >= 4 {
			return &protocol.MediaInfoPayload{
				Title:     parts[1],
				Artist:    parts[2],
				Album:     parts[3],
				IsPlaying: true,
			}, nil
		}
	}
	return &protocol.MediaInfoPayload{IsPlaying: false}, nil
}

// StartMediaTracker launches the background swift media tracking process.
// It receives streaming JSON lines from swift and passes them to onUpdate.
func StartMediaTracker(ctx context.Context, onUpdate func(info protocol.MediaInfoPayload)) {
	candidates := []string{
		"./mac_media_tracker.swift",
		"./mac-daemon/mac_media_tracker.swift",
		"/Users/feritetem/Desktop/android-mac-sync/mac-daemon/mac_media_tracker.swift",
	}

	if exe, err := os.Executable(); err == nil {
		candidates = append([]string{filepath.Join(filepath.Dir(exe), "mac_media_tracker.swift")}, candidates...)
	}

	var scriptPath string
	for _, p := range candidates {
		if _, err := os.Stat(p); err == nil {
			scriptPath = p
			break
		}
	}
	if scriptPath == "" {
		scriptPath = "/Users/feritetem/Desktop/android-mac-sync/mac-daemon/mac_media_tracker.swift"
	}
	fmt.Printf("[Medya] Swift takipçi betiği bulundu: %s\n", scriptPath)

	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			default:
			}

			cmd := exec.CommandContext(ctx, "/usr/bin/swift", scriptPath)
			cmd.Stderr = os.Stderr
			stdout, err := cmd.StdoutPipe()
			if err != nil {
				fmt.Printf("[Medya Hata] StdoutPipe hatası: %v\n", err)
				time.Sleep(2 * time.Second)
				continue
			}

			if err := cmd.Start(); err != nil {
				fmt.Printf("[Medya Hata] Swift başlatma hatası: %v\n", err)
				time.Sleep(2 * time.Second)
				continue
			}

			scanner := bufio.NewScanner(stdout)
			for scanner.Scan() {
				line := strings.TrimSpace(scanner.Text())
				if line == "" || !strings.HasPrefix(line, "{") {
					continue
				}
				var info protocol.MediaInfoPayload
				if err := json.Unmarshal([]byte(line), &info); err == nil {
					info.Source = "mac"
					onUpdate(info)
				}
			}

			if err := cmd.Wait(); err != nil {
				fmt.Printf("[Medya] Swift takipçi kapandı (%v), 2 saniye içinde yeniden başlatılacak...\n", err)
			}
			select {
			case <-ctx.Done():
				return
			case <-time.After(1 * time.Second):
			}
		}
	}()
}

