package windows

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os/exec"
	"strings"
	"syscall"
	"time"

	"windows-sync/internal/protocol"
)

// escapePS escapes strings for PowerShell single-quote literal strings.
func escapePS(s string) string {
	return strings.ReplaceAll(s, "'", "''")
}

// ShowToast displays a native Windows 10/11 Toast Notification.
func ShowToast(title, body, appName string) error {
	displayTitle := title
	if appName != "" && !strings.Contains(title, appName) {
		displayTitle = fmt.Sprintf("[%s] %s", appName, title)
	}

	psScript := fmt.Sprintf(`
[Windows.UI.Notifications.ToastNotificationManager, Windows.UI.Notifications, ContentType = WindowsRuntime] | Out-Null
$template = [Windows.UI.Notifications.ToastNotificationManager]::GetTemplateContent([Windows.UI.Notifications.ToastTemplateType]::ToastText02)
$nodes = $template.GetElementsByTagName('text')
$nodes.Item(0).AppendChild($template.CreateTextNode('%s')) | Out-Null
$nodes.Item(1).AppendChild($template.CreateTextNode('%s')) | Out-Null
$toast = [Windows.UI.Notifications.ToastNotification]::new($template)
$notifier = [Windows.UI.Notifications.ToastNotificationManager]::CreateToastNotifier('Android Sync')
$notifier.Show($toast)
`, escapePS(displayTitle), escapePS(body))

	cmd := exec.Command("powershell", "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-Command", psScript)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	return cmd.Start() // Non-blocking!
}

// ShowCallAlert notifies the user of an incoming phone call on Windows.
func ShowCallAlert(callerName, phoneNumber string) error {
	display := callerName
	if display == "" {
		display = phoneNumber
	}
	if display == "" {
		display = "Bilinmeyen Numara"
	}

	subtext := "Gelen Arama..."
	if callerName != "" && phoneNumber != "" {
		subtext = fmt.Sprintf("Gelen Arama: %s", phoneNumber)
	}

	psScript := fmt.Sprintf(`
[Windows.UI.Notifications.ToastNotificationManager, Windows.UI.Notifications, ContentType = WindowsRuntime] | Out-Null
$template = [Windows.UI.Notifications.ToastNotificationManager]::GetTemplateContent([Windows.UI.Notifications.ToastText02])
$nodes = $template.GetElementsByTagName('text')
$nodes.Item(0).AppendChild($template.CreateTextNode('📞 %s')) | Out-Null
$nodes.Item(1).AppendChild($template.CreateTextNode('%s')) | Out-Null
$toast = [Windows.UI.Notifications.ToastNotification]::new($template)
$notifier = [Windows.UI.Notifications.ToastNotificationManager]::CreateToastNotifier('Android Sync')
$notifier.Show($toast)
`, escapePS(display), escapePS(subtext))

	cmd := exec.Command("powershell", "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-Command", psScript)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	return cmd.Start()
}

// DismissCallAlert is a stub for call end on Windows.
func DismissCallAlert() {
	// Call dismissed
}

// StartWindowsNotificationListener starts a persistent PowerShell listener that detects
// incoming Windows Action Center notifications (WhatsApp, Telegram, Slack, Mail, Browser, etc.)
// and forwards them to onNotification.
func StartWindowsNotificationListener(ctx context.Context, onNotification func(protocol.NotificationPayload)) {
	go func() {
		log.Println("[Bildirim] 🔔 Windows bildirim dinleyicisi başlatılıyor...")
		for {
			select {
			case <-ctx.Done():
				return
			default:
			}

			psScript := `
[Console]::OutputEncoding = [System.Text.Encoding]::UTF8
Add-Type -AssemblyName System.Runtime.WindowsRuntime
[Windows.UI.Notifications.Management.UserNotificationListener, Windows.UI.Notifications, ContentType = WindowsRuntime] | Out-Null
[Windows.UI.Notifications.UserNotification, Windows.UI.Notifications, ContentType = WindowsRuntime] | Out-Null

$listener = [Windows.UI.Notifications.Management.UserNotificationListener]::Current
if (-not $listener) { 
    Write-Error "UserNotificationListener not supported"
    exit 1 
}

$accessOp = $listener.RequestAccessAsync()
$asTaskGeneric = ([System.WindowsRuntimeSystemExtensions].GetMethods() | Where-Object { $_.Name -eq 'AsTask' -and $_.GetParameters().Count -eq 1 -and $_.GetParameters()[0].ParameterType.Name -like '*IAsyncOperation*1*' })[0]
$accessTask = $asTaskGeneric.MakeGenericMethod([Windows.UI.Notifications.Management.UserNotificationListenerAccessStatus]).Invoke($null, @($accessOp))
$accessTask.Wait(2000) | Out-Null

if ($accessTask.Result -ne 'Allowed') {
    Write-Error "Notification access not allowed: $($accessTask.Result)"
    exit 1
}

$listType = [System.Collections.Generic.IReadOnlyList[Windows.UI.Notifications.UserNotification]]
$seen = [System.Collections.Generic.HashSet[uint32]]::new()

# Seed existing notifications so we do not spam old history
try {
    $getOp = $listener.GetNotificationsAsync([Windows.UI.Notifications.NotificationKinds]::Toast)
    $getTask = $asTaskGeneric.MakeGenericMethod($listType).Invoke($null, @($getOp))
    $getTask.Wait(2000) | Out-Null
    if ($getTask.Result) {
        foreach ($n in @($getTask.Result)) {
            $seen.Add($n.Id) | Out-Null
        }
    }
} catch {}

[Console]::WriteLine("LISTENER_READY")
[Console]::Out.Flush()

while ($true) {
    Start-Sleep -Milliseconds 600
    try {
        $getOp = $listener.GetNotificationsAsync([Windows.UI.Notifications.NotificationKinds]::Toast)
        $getTask = $asTaskGeneric.MakeGenericMethod($listType).Invoke($null, @($getOp))
        $getTask.Wait(1200) | Out-Null
        $current = @($getTask.Result)
        foreach ($n in $current) {
            if (-not $seen.Contains($n.Id)) {
                $seen.Add($n.Id) | Out-Null
                $appName = ""
                try { $appName = $n.AppInfo.DisplayInfo.DisplayName } catch {}
                if (-not $appName) {
                    try { $appName = $n.AppInfo.Id } catch {}
                }
                if ($appName -eq "Android Sync" -or $appName -like "*android-sync*") { continue }

                $title = ""
                $body = ""
                try {
                    $bind = $n.Notification.Visual.GetBinding([Windows.UI.Notifications.KnownNotificationBindings]::ToastGeneric)
                    if ($bind) {
                        $elems = @($bind.GetTextElements())
                        if ($elems.Count -gt 0) { $title = $elems[0].Text }
                        if ($elems.Count -gt 1) { $body = $elems[1].Text }
                    }
                } catch {}

                if (-not $title -and -not $body) {
                    try {
                        foreach ($b in $n.Notification.Visual.Bindings) {
                            $elems = @($b.GetTextElements())
                            if ($elems.Count -gt 0 -and -not $title) { $title = $elems[0].Text }
                            if ($elems.Count -gt 1 -and -not $body) { $body = $elems[1].Text }
                        }
                    } catch {}
                }

                if (-not $appName -and $title) {
                    $appName = "Windows"
                }

                if ($title -or $body) {
                    $payload = @{
                        id = "$($n.Id)"
                        app_name = $appName
                        package_name = if ($n.AppInfo.Id) { $n.AppInfo.Id } else { $appName }
                        title = $title
                        text = $body
                        timestamp = [DateTimeOffset]::UtcNow.ToUnixTimeMilliseconds()
                    }
                    [Console]::WriteLine((ConvertTo-Json $payload -Compress))
                    [Console]::Out.Flush()
                }
            }
        }
    } catch {}
}
`
			cmd := exec.CommandContext(ctx, "powershell", "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-Command", psScript)
			cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}

			stdout, err := cmd.StdoutPipe()
			if err != nil {
				log.Printf("[Bildirim Hata] StdoutPipe acilamadi: %v", err)
				time.Sleep(3 * time.Second)
				continue
			}

			stderr, _ := cmd.StderrPipe()
			if stderr != nil {
				go func() {
					errScanner := bufio.NewScanner(stderr)
					for errScanner.Scan() {
						log.Printf("[Bildirim PS Hata] %s", errScanner.Text())
					}
				}()
			}

			if err := cmd.Start(); err != nil {
				log.Printf("[Bildirim Hata] PowerShell baslatilamadi: %v", err)
				time.Sleep(3 * time.Second)
				continue
			}

			scanner := bufio.NewScanner(stdout)
			for scanner.Scan() {
				line := strings.TrimSpace(scanner.Text())
				if line == "LISTENER_READY" {
					log.Println("[Bildirim] ✅ Windows Bildirim Dinleyicisi hazir ve dinliyor.")
					continue
				}
				if line == "" || !strings.HasPrefix(line, "{") {
					continue
				}
				var notif protocol.NotificationPayload
				if err := json.Unmarshal([]byte(line), &notif); err == nil && (notif.Title != "" || notif.Text != "") {
					if onNotification != nil {
						onNotification(notif)
					}
				}
			}

			_ = cmd.Wait()
			time.Sleep(2 * time.Second)
		}
	}()
}


