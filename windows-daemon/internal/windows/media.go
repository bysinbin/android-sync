package windows

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"syscall"
	"time"

	"windows-sync/internal/protocol"
)

var (
	procKeybdEvent = user32.NewProc("keybd_event")
)

const (
	VK_VOLUME_MUTE         = 0xAD
	VK_VOLUME_DOWN         = 0xAE
	VK_VOLUME_UP           = 0xAF
	VK_MEDIA_NEXT_TRACK    = 0xB0
	VK_MEDIA_PREV_TRACK    = 0xB1
	VK_MEDIA_STOP          = 0xB2
	VK_MEDIA_PLAY_PAUSE    = 0xB3
	KEYEVENTF_KEYUP uintptr = 0x0002
)

// sendVirtualKey presses and releases a virtual key on Windows.
func sendVirtualKey(vk byte) {
	procKeybdEvent.Call(uintptr(vk), 0, 0, 0)
	time.Sleep(10 * time.Millisecond)
	procKeybdEvent.Call(uintptr(vk), 0, KEYEVENTF_KEYUP, 0)
}

// executeGSMTCOperation sends playback commands (Play/Pause, Next, Previous) directly to Windows GSMTC sessions.
func executeGSMTCOperation(opType string) error {
	psScript := fmt.Sprintf(`
try {
    Add-Type -AssemblyName System.Runtime.WindowsRuntime
    [Windows.Media.Control.GlobalSystemMediaTransportControlsSessionManager, Windows.Media.Control, ContentType = WindowsRuntime] | Out-Null
    $asyncOp = [Windows.Media.Control.GlobalSystemMediaTransportControlsSessionManager]::RequestAsync()
    $asTaskGeneric = ([System.WindowsRuntimeSystemExtensions].GetMethods() | ? { $_.Name -eq 'AsTask' -and $_.GetParameters().Count -eq 1 -and $_.GetParameters()[0].ParameterType.Name -eq 'IAsyncOperation`+"`"+`1' })[0]
    $netTask = $asTaskGeneric.MakeGenericMethod([Windows.Media.Control.GlobalSystemMediaTransportControlsSessionManager]).Invoke($null, @($asyncOp))
    $netTask.Wait(1200) | Out-Null
    $mgr = $netTask.Result

    if ($mgr) {
        $sessions = @($mgr.GetSessions())
        $targetSession = $null
        foreach ($s in $sessions) {
            $pb = $s.GetPlaybackInfo()
            if ($pb -and $pb.PlaybackStatus -like '*Playing*') {
                $targetSession = $s
                break
            }
        }
        if (-not $targetSession -and $sessions.Count -gt 0) {
            $targetSession = $sessions[0]
        }
        if ($targetSession) {
            $op = $null
            $t = '%s'
            if ($t -eq 'PLAY_PAUSE') {
                $op = $targetSession.TryTogglePlayPauseAsync()
            } elseif ($t -eq 'PLAY') {
                $op = $targetSession.TryPlayAsync()
            } elseif ($t -eq 'PAUSE') {
                $op = $targetSession.TryPauseAsync()
            } elseif ($t -eq 'NEXT') {
                $op = $targetSession.TrySkipNextAsync()
            } elseif ($t -eq 'PREV') {
                $op = $targetSession.TrySkipPreviousAsync()
            }
            if ($op) {
                $netOp = $asTaskGeneric.MakeGenericMethod([bool]).Invoke($null, @($op))
                $netOp.Wait(1500) | Out-Null
            }
        }
    }
} catch {}
`, opType)

	cmd := exec.Command("powershell", "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-Command", psScript)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	if err := cmd.Run(); err != nil {
		switch opType {
		case "PLAY_PAUSE", "PLAY", "PAUSE":
			sendVirtualKey(VK_MEDIA_PLAY_PAUSE)
		case "NEXT":
			sendVirtualKey(VK_MEDIA_NEXT_TRACK)
		case "PREV":
			sendVirtualKey(VK_MEDIA_PREV_TRACK)
		}
	}
	return nil
}

// ExecuteMediaAction handles playback and volume controls on Windows.
func ExecuteMediaAction(action string) error {
	action = strings.ToUpper(action)

	switch action {
	case "VOLUME_UP":
		sendVirtualKey(VK_VOLUME_UP)
		sendVirtualKey(VK_VOLUME_UP)
		return nil

	case "VOLUME_DOWN":
		sendVirtualKey(VK_VOLUME_DOWN)
		sendVirtualKey(VK_VOLUME_DOWN)
		return nil

	case "MUTE":
		sendVirtualKey(VK_VOLUME_MUTE)
		return nil

	case "PLAY_PAUSE", "PLAY", "PAUSE", "TOGGLE":
		return executeGSMTCOperation("PLAY_PAUSE")

	case "NEXT":
		return executeGSMTCOperation("NEXT")

	case "PREVIOUS", "PREV":
		return executeGSMTCOperation("PREV")

	case "SEEK_FORWARD", "FORWARD", "FORWARD_15":
		return ExecuteMediaSeekRelative(15)

	case "SEEK_BACKWARD", "REWIND", "REWIND_15":
		return ExecuteMediaSeekRelative(-15)

	default:
		return fmt.Errorf("bilinmeyen medya aksiyonu: %s", action)
	}
}

// ExecuteMediaSeekPercent seeks active media player on Windows to a percentage (0.0 to 100.0).
func ExecuteMediaSeekPercent(percent float64) error {
	if percent < 0 {
		percent = 0
	} else if percent > 100 {
		percent = 100
	}

	psScript := fmt.Sprintf(`
try {
    Add-Type -AssemblyName System.Runtime.WindowsRuntime
    [Windows.Media.Control.GlobalSystemMediaTransportControlsSessionManager, Windows.Media.Control, ContentType = WindowsRuntime] | Out-Null
    $asyncOp = [Windows.Media.Control.GlobalSystemMediaTransportControlsSessionManager]::RequestAsync()
    $asTaskGeneric = ([System.WindowsRuntimeSystemExtensions].GetMethods() | ? { $_.Name -eq 'AsTask' -and $_.GetParameters().Count -eq 1 -and $_.GetParameters()[0].ParameterType.Name -eq 'IAsyncOperation`+"`"+`1' })[0]
    $netTask = $asTaskGeneric.MakeGenericMethod([Windows.Media.Control.GlobalSystemMediaTransportControlsSessionManager]).Invoke($null, @($asyncOp))
    $netTask.Wait(1200) | Out-Null
    $mgr = $netTask.Result

    if ($mgr) {
        $sessions = @($mgr.GetSessions())
        $targetSession = $null
        foreach ($s in $sessions) {
            $pb = $s.GetPlaybackInfo()
            if ($pb -and $pb.PlaybackStatus -like '*Playing*') {
                $targetSession = $s
                break
            }
        }
        if (-not $targetSession -and $sessions.Count -gt 0) {
            $targetSession = $sessions[0]
        }
        if ($targetSession) {
            $timeline = $targetSession.GetTimelineProperties()
            if ($timeline -and $timeline.EndTime) {
                $durMs = [int64]$timeline.EndTime.TotalMilliseconds
                $targetMs = [int64]($durMs * (%.2f / 100.0))
                $ticks = [int64]($targetMs * 10000)
                $op = $targetSession.TryChangePlaybackPositionAsync($ticks)
                $t = $asTaskGeneric.MakeGenericMethod([bool]).Invoke($null, @($op))
                $t.Wait(1500) | Out-Null
            }
        }
    }
} catch {}
`, percent)

	cmd := exec.Command("powershell", "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-Command", psScript)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	return cmd.Run()
}

// ExecuteMediaSeekRelative seeks active media by +/- seconds.
func ExecuteMediaSeekRelative(deltaSec int) error {
	psScript := fmt.Sprintf(`
try {
    Add-Type -AssemblyName System.Runtime.WindowsRuntime
    [Windows.Media.Control.GlobalSystemMediaTransportControlsSessionManager, Windows.Media.Control, ContentType = WindowsRuntime] | Out-Null
    $asyncOp = [Windows.Media.Control.GlobalSystemMediaTransportControlsSessionManager]::RequestAsync()
    $asTaskGeneric = ([System.WindowsRuntimeSystemExtensions].GetMethods() | ? { $_.Name -eq 'AsTask' -and $_.GetParameters().Count -eq 1 -and $_.GetParameters()[0].ParameterType.Name -eq 'IAsyncOperation`+"`"+`1' })[0]
    $netTask = $asTaskGeneric.MakeGenericMethod([Windows.Media.Control.GlobalSystemMediaTransportControlsSessionManager]).Invoke($null, @($asyncOp))
    $netTask.Wait(1200) | Out-Null
    $mgr = $netTask.Result

    if ($mgr) {
        $sessions = @($mgr.GetSessions())
        $targetSession = $null
        foreach ($s in $sessions) {
            $pb = $s.GetPlaybackInfo()
            if ($pb -and $pb.PlaybackStatus -like '*Playing*') {
                $targetSession = $s
                break
            }
        }
        if (-not $targetSession -and $sessions.Count -gt 0) {
            $targetSession = $sessions[0]
        }
        if ($targetSession) {
            $timeline = $targetSession.GetTimelineProperties()
            $basePos = if ($timeline -and $timeline.Position) { [int64]$timeline.Position.TotalMilliseconds } else { 0 }
            $curMs = $basePos
            $pb = $targetSession.GetPlaybackInfo()
            if ($pb -and $pb.PlaybackStatus -like '*Playing*' -and $timeline -and $timeline.LastUpdatedTime) {
                try {
                    $elapsed = [System.DateTimeOffset]::Now.Subtract($timeline.LastUpdatedTime).TotalMilliseconds
                    if ($elapsed -gt 0) { $curMs = [int64]($basePos + $elapsed) }
                } catch {}
            }
            $durMs = if ($timeline -and $timeline.EndTime) { [int64]$timeline.EndTime.TotalMilliseconds } else { 0 }
            $targetMs = $curMs + (%d * 1000)
            if ($targetMs -lt 0) { $targetMs = 0 }
            if ($durMs -gt 0 -and $targetMs -gt $durMs) { $targetMs = $durMs }
            $ticks = [int64]($targetMs * 10000)
            $op = $targetSession.TryChangePlaybackPositionAsync($ticks)
            $t = $asTaskGeneric.MakeGenericMethod([bool]).Invoke($null, @($op))
            $t.Wait(1500) | Out-Null
        }
    }
} catch {}
`, deltaSec)

	cmd := exec.Command("powershell", "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-Command", psScript)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	return cmd.Run()
}

// PauseAllMedia pauses any active media player on Windows (e.g. on incoming call).
func PauseAllMedia() {
	sendVirtualKey(VK_MEDIA_PLAY_PAUSE)
}

// QueryWindowsMedia polls Windows GSMTC for active media playback info from any app without application filter.
func QueryWindowsMedia() (*protocol.MediaInfoPayload, error) {
	psScript := `
try {
    Add-Type -AssemblyName System.Runtime.WindowsRuntime
    [Windows.Media.Control.GlobalSystemMediaTransportControlsSessionManager, Windows.Media.Control, ContentType = WindowsRuntime] | Out-Null
    $asyncOp = [Windows.Media.Control.GlobalSystemMediaTransportControlsSessionManager]::RequestAsync()
    $asTaskGeneric = ([System.WindowsRuntimeSystemExtensions].GetMethods() | ? { $_.Name -eq 'AsTask' -and $_.GetParameters().Count -eq 1 -and $_.GetParameters()[0].ParameterType.Name -eq 'IAsyncOperation` + "`" + `1' })[0]
    $netTask = $asTaskGeneric.MakeGenericMethod([Windows.Media.Control.GlobalSystemMediaTransportControlsSessionManager]).Invoke($null, @($asyncOp))
    $netTask.Wait(1200) | Out-Null
    $mgr = $netTask.Result

    $best = $null
    if ($mgr) {
        $sessions = @($mgr.GetSessions())
        foreach ($s in $sessions) {
            try {
                $propOp = $s.TryGetMediaPropertiesAsync()
                $asTaskProps = $asTaskGeneric.MakeGenericMethod([Windows.Media.Control.GlobalSystemMediaTransportControlsSessionMediaProperties]).Invoke($null, @($propOp))
                $asTaskProps.Wait(800) | Out-Null
                $props = $asTaskProps.Result

                if ($props -and ($props.Title -or $props.Artist)) {
                    $playback = $s.GetPlaybackInfo()
                    $isPlaying = ($playback -and $playback.PlaybackStatus -like '*Playing*')
                    $timeline = $s.GetTimelineProperties()
                    $durMs = if ($timeline -and $timeline.EndTime) { [int64]$timeline.EndTime.TotalMilliseconds } else { 0 }
                    $basePos = if ($timeline -and $timeline.Position) { [int64]$timeline.Position.TotalMilliseconds } else { 0 }
                    $posMs = $basePos
                    if ($isPlaying -and $timeline -and $timeline.LastUpdatedTime) {
                        try {
                            $elapsed = [System.DateTimeOffset]::Now.Subtract($timeline.LastUpdatedTime).TotalMilliseconds
                            if ($elapsed -gt 0) {
                                $posMs = [int64]($basePos + $elapsed)
                            }
                        } catch {}
                    }
                    if ($durMs -gt 0 -and $posMs -gt $durMs) { $posMs = $durMs }
                    $pct = if ($durMs -gt 0) { [math]::Round(($posMs / $durMs) * 100.0, 1) } else { 0.0 }

                    $item = @{
                        app = $s.SourceAppUserModelId
                        title = $props.Title
                        artist = $props.Artist
                        album = $props.AlbumTitle
                        is_playing = [bool]$isPlaying
                        position_ms = $posMs
                        duration_ms = $durMs
                        percent = $pct
                        source = 'windows'
                    }

                    if ($isPlaying) {
                        $best = $item
                        break
                    } elseif ($best -eq $null) {
                        $best = $item
                    }
                }
            } catch {}
        }
    }

    if ($best) {
        ConvertTo-Json $best -Compress
        exit 0
    }
} catch {}
Write-Output '{"title":"","artist":"","is_playing":false,"position_ms":0,"duration_ms":0,"percent":0,"source":"windows"}'
`

	cmd := exec.Command("powershell", "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-Command", psScript)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}

	var info protocol.MediaInfoPayload
	trimmed := strings.TrimSpace(string(out))
	if err := json.Unmarshal([]byte(trimmed), &info); err != nil {
		return nil, err
	}
	info.Source = "windows"
	return &info, nil
}

// StartMediaTracker launches a background poller for Windows Media Transport Controls.
func StartMediaTracker(ctx context.Context, onUpdate func(info protocol.MediaInfoPayload)) {
	go func() {
		ticker := time.NewTicker(1500 * time.Millisecond)
		defer ticker.Stop()

		var lastTitle, lastArtist string
		var lastPlaying bool
		var lastPosSec int64

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				info, err := QueryWindowsMedia()
				if err == nil && info != nil {
					posSec := info.PositionMs / 1000
					if info.Title != lastTitle || info.Artist != lastArtist || info.IsPlaying != lastPlaying || (info.IsPlaying && posSec != lastPosSec) {
						lastTitle = info.Title
						lastArtist = info.Artist
						lastPlaying = info.IsPlaying
						lastPosSec = posSec
						onUpdate(*info)
					}
				}
			}
		}
	}()
}
