package windows

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"runtime"
	"sync"
	"syscall"
	"unsafe"
)

var (
	shell32              = syscall.NewLazyDLL("shell32.dll")
	procShellNotifyIconW = shell32.NewProc("Shell_NotifyIconW")
	procRegisterClassExW = user32.NewProc("RegisterClassExW")
	procCreateWindowExW  = user32.NewProc("CreateWindowExW")
	procDefWindowProcW   = user32.NewProc("DefWindowProcW")
	procDestroyWindow    = user32.NewProc("DestroyWindow")
	procPostQuitMessage  = user32.NewProc("PostQuitMessage")
	procGetMessageW      = user32.NewProc("GetMessageW")
	procTranslateMessage = user32.NewProc("TranslateMessage")
	procDispatchMessageW = user32.NewProc("DispatchMessageW")
	procLoadIconW        = user32.NewProc("LoadIconW")
	procCreatePopupMenu  = user32.NewProc("CreatePopupMenu")
	procAppendMenuW      = user32.NewProc("AppendMenuW")
	procTrackPopupMenu   = user32.NewProc("TrackPopupMenu")
	procGetCursorPos     = user32.NewProc("GetCursorPos")
	procSetForegroundWnd = user32.NewProc("SetForegroundWindow")
)

const (
	NIM_ADD          = 0x00000000
	NIM_MODIFY       = 0x00000001
	NIM_DELETE       = 0x00000002
	NIF_MESSAGE      = 0x00000001
	NIF_ICON         = 0x00000002
	NIF_TIP          = 0x00000004
	NIF_INFO         = 0x00000010
	WM_USER          = 0x0400
	WM_TRAYICON      = WM_USER + 42
	WM_COMMAND       = 0x0111
	WM_DESTROY       = 0x0002
	WM_RBUTTONUP     = 0x0205
	WM_LBUTTONUP     = 0x0202
	WM_LBUTTONDBLCLK = 0x0203
	MF_STRING        = 0x00000000
	MF_SEPARATOR     = 0x00000800
	MF_GRAYED        = 0x00000001
	TPM_BOTTOMALIGN  = 0x0020
	TPM_RIGHTALIGN   = 0x0008
	IDI_APPLICATION  = 32512
	IDI_INFORMATION  = 32516

	CMD_STATUS         = 1001
	CMD_OPEN_DASHBOARD = 1002
	CMD_RING_PHONE     = 1003
	CMD_STOP_RING      = 1004
	CMD_PLAY_PAUSE     = 1005
	CMD_NEXT_PHONE     = 1006
	CMD_SEND_CLIPBOARD = 1007
	CMD_RESCAN         = 1008
	CMD_EXIT           = 1009
	CMD_OPEN_APP_MODE  = 1010
)

type WNDCLASSEXW struct {
	cbSize        uint32
	style         uint32
	lpfnWndProc   uintptr
	cbClsExtra    int32
	cbWndExtra    int32
	hInstance     uintptr
	hIcon         uintptr
	hCursor       uintptr
	hbrBackground uintptr
	lpszMenuName  *uint16
	lpszClassName *uint16
	hIconSm       uintptr
}

type POINT struct {
	X int32
	Y int32
}

type MSG struct {
	HWnd    uintptr
	Message uint32
	WParam  uintptr
	LParam  uintptr
	Time    uint32
	Pt      POINT
}

type NOTIFYICONDATAW struct {
	cbSize           uint32
	hWnd             uintptr
	uID              uint32
	uFlags           uint32
	uCallbackMessage uint32
	hIcon            uintptr
	szTip            [128]uint16
	dwState          uint32
	dwStateMask      uint32
	szInfo           [256]uint16
	uTimeoutOrVersion uint32
	szInfoTitle      [64]uint16
	dwInfoFlags      uint32
	guidItem         [16]byte
	hBalloonIcon     uintptr
}

type TrayCallbacks struct {
	OnOpenUI        func()
	OnOpenAppMode   func()
	OnRingPhone     func()
	OnStopRingPhone func()
	OnPlayPause     func()
	OnNextPhone     func()
	OnSendClipboard func()
	OnRescan        func()
	OnExit          func()
}

type TrayManager struct {
	hWnd       uintptr
	nid        NOTIFYICONDATAW
	mu         sync.Mutex
	statusText string
	callbacks  TrayCallbacks
}

var globalTray *TrayManager

func NewTrayManager(cb TrayCallbacks) *TrayManager {
	tm := &TrayManager{
		statusText: "Android-Windows Sync: Bağlantı Aranıyor 🟡",
		callbacks:  cb,
	}
	globalTray = tm
	return tm
}

func (tm *TrayManager) UpdateStatus(text string) {
	tm.mu.Lock()
	defer tm.mu.Unlock()
	tm.statusText = text

	if tm.hWnd != 0 {
		var tip [128]uint16
		utf16Tip, _ := syscall.UTF16FromString(text)
		copy(tip[:], utf16Tip)
		tm.nid.szTip = tip
		tm.nid.uFlags = NIF_TIP
		procShellNotifyIconW.Call(NIM_MODIFY, uintptr(unsafe.Pointer(&tm.nid)))
	}
}

func wndProc(hWnd uintptr, msg uint32, wParam, lParam uintptr) uintptr {
	switch msg {
	case WM_TRAYICON:
		switch lParam {
		case WM_LBUTTONDBLCLK, WM_LBUTTONUP:
			if globalTray != nil && globalTray.callbacks.OnOpenUI != nil {
				go globalTray.callbacks.OnOpenUI()
			}
		case WM_RBUTTONUP:
			var pt POINT
			procGetCursorPos.Call(uintptr(unsafe.Pointer(&pt)))
			procSetForegroundWnd.Call(hWnd)

			hMenu, _, _ := procCreatePopupMenu.Call()

			globalTray.mu.Lock()
			status := globalTray.statusText
			globalTray.mu.Unlock()

			statusStr, _ := syscall.UTF16PtrFromString("📱 " + status)
			openDashStr, _ := syscall.UTF16PtrFromString("🌐 Kontrol Panelini Aç (Tarayıcı)")
			openAppStr, _ := syscall.UTF16PtrFromString("📱 Bağımsız Pencerede Aç (App Mode)")
			playStr, _ := syscall.UTF16PtrFromString("⏯ Telefondaki Müziği Oynat/Durdur")
			nextStr, _ := syscall.UTF16PtrFromString("⏭ Telefonda Sonraki Şarkı")
			ringStr, _ := syscall.UTF16PtrFromString("🔔 Telefonumu Çaldır (Bul)")
			stopRingStr, _ := syscall.UTF16PtrFromString("🔕 Telefonu Sustur")
			clipStr, _ := syscall.UTF16PtrFromString("📋 Bilgisayar Panosunu Telefona Gönder")
			rescanStr, _ := syscall.UTF16PtrFromString("⟳ Ağ Keşfini / Bağlantıyı Yenile")
			exitStr, _ := syscall.UTF16PtrFromString("❌ Çıkış")

			procAppendMenuW.Call(hMenu, MF_STRING|MF_GRAYED, CMD_STATUS, uintptr(unsafe.Pointer(statusStr)))
			procAppendMenuW.Call(hMenu, MF_SEPARATOR, 0, 0)
			procAppendMenuW.Call(hMenu, MF_STRING, CMD_OPEN_DASHBOARD, uintptr(unsafe.Pointer(openDashStr)))
			procAppendMenuW.Call(hMenu, MF_STRING, CMD_OPEN_APP_MODE, uintptr(unsafe.Pointer(openAppStr)))
			procAppendMenuW.Call(hMenu, MF_SEPARATOR, 0, 0)
			procAppendMenuW.Call(hMenu, MF_STRING, CMD_PLAY_PAUSE, uintptr(unsafe.Pointer(playStr)))
			procAppendMenuW.Call(hMenu, MF_STRING, CMD_NEXT_PHONE, uintptr(unsafe.Pointer(nextStr)))
			procAppendMenuW.Call(hMenu, MF_STRING, CMD_RING_PHONE, uintptr(unsafe.Pointer(ringStr)))
			procAppendMenuW.Call(hMenu, MF_STRING, CMD_STOP_RING, uintptr(unsafe.Pointer(stopRingStr)))
			procAppendMenuW.Call(hMenu, MF_STRING, CMD_SEND_CLIPBOARD, uintptr(unsafe.Pointer(clipStr)))
			procAppendMenuW.Call(hMenu, MF_SEPARATOR, 0, 0)
			procAppendMenuW.Call(hMenu, MF_STRING, CMD_RESCAN, uintptr(unsafe.Pointer(rescanStr)))
			procAppendMenuW.Call(hMenu, MF_SEPARATOR, 0, 0)
			procAppendMenuW.Call(hMenu, MF_STRING, CMD_EXIT, uintptr(unsafe.Pointer(exitStr)))

			procTrackPopupMenu.Call(hMenu, TPM_BOTTOMALIGN|TPM_RIGHTALIGN, uintptr(pt.X), uintptr(pt.Y), 0, hWnd, 0)
		}
		return 0

	case WM_COMMAND:
		cmdID := uint32(wParam & 0xffff)
		switch cmdID {
		case CMD_OPEN_DASHBOARD:
			if globalTray != nil && globalTray.callbacks.OnOpenUI != nil {
				go globalTray.callbacks.OnOpenUI()
			}
		case CMD_OPEN_APP_MODE:
			if globalTray != nil && globalTray.callbacks.OnOpenAppMode != nil {
				go globalTray.callbacks.OnOpenAppMode()
			}
		case CMD_RING_PHONE:
			if globalTray != nil && globalTray.callbacks.OnRingPhone != nil {
				go globalTray.callbacks.OnRingPhone()
			}
		case CMD_STOP_RING:
			if globalTray != nil && globalTray.callbacks.OnStopRingPhone != nil {
				go globalTray.callbacks.OnStopRingPhone()
			}
		case CMD_PLAY_PAUSE:
			if globalTray != nil && globalTray.callbacks.OnPlayPause != nil {
				go globalTray.callbacks.OnPlayPause()
			}
		case CMD_NEXT_PHONE:
			if globalTray != nil && globalTray.callbacks.OnNextPhone != nil {
				go globalTray.callbacks.OnNextPhone()
			}
		case CMD_SEND_CLIPBOARD:
			if globalTray != nil && globalTray.callbacks.OnSendClipboard != nil {
				go globalTray.callbacks.OnSendClipboard()
			}
		case CMD_RESCAN:
			if globalTray != nil && globalTray.callbacks.OnRescan != nil {
				go globalTray.callbacks.OnRescan()
			}
		case CMD_EXIT:
			if globalTray != nil && globalTray.callbacks.OnExit != nil {
				go globalTray.callbacks.OnExit()
			}
			procPostQuitMessage.Call(0)
		}
		return 0

	case WM_DESTROY:
		if globalTray != nil {
			procShellNotifyIconW.Call(NIM_DELETE, uintptr(unsafe.Pointer(&globalTray.nid)))
		}
		procPostQuitMessage.Call(0)
		return 0
	}

	ret, _, _ := procDefWindowProcW.Call(hWnd, uintptr(msg), wParam, lParam)
	return ret
}

func (tm *TrayManager) Start() {
	go func() {
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()

		className, _ := syscall.UTF16PtrFromString("AndroidWindowsSyncTray")
		hInstance := uintptr(0)
		hIcon, _, _ := procLoadIconW.Call(0, uintptr(IDI_APPLICATION))
		if hIcon == 0 {
			hIcon, _, _ = procLoadIconW.Call(0, uintptr(IDI_INFORMATION))
		}

		wcex := WNDCLASSEXW{
			cbSize:        uint32(unsafe.Sizeof(WNDCLASSEXW{})),
			lpfnWndProc:   syscall.NewCallback(wndProc),
			hInstance:     hInstance,
			hIcon:         hIcon,
			lpszClassName: className,
		}
		procRegisterClassExW.Call(uintptr(unsafe.Pointer(&wcex)))

		hWnd, _, _ := procCreateWindowExW.Call(
			0,
			uintptr(unsafe.Pointer(className)),
			uintptr(unsafe.Pointer(className)),
			0,
			0, 0, 0, 0,
			0, 0, hInstance, 0,
		)
		if hWnd == 0 {
			log.Printf("[Tray] Win32 penceresi oluşturulamadı.")
			return
		}
		tm.hWnd = hWnd

		var tip [128]uint16
		utf16Tip, _ := syscall.UTF16FromString("Android-Windows Sync")
		copy(tip[:], utf16Tip)

		tm.nid = NOTIFYICONDATAW{
			hWnd:             hWnd,
			uID:              1,
			uFlags:           NIF_MESSAGE | NIF_ICON | NIF_TIP,
			uCallbackMessage: WM_TRAYICON,
			hIcon:            hIcon,
			szTip:            tip,
		}

		sizes := []uint32{
			uint32(unsafe.Sizeof(NOTIFYICONDATAW{})),
			968, // Win10/11 64-bit V3
			936, // XP/Vista 64-bit V2
			504,
		}

		var res uintptr
		for _, sz := range sizes {
			tm.nid.cbSize = sz
			r, _, _ := procShellNotifyIconW.Call(NIM_ADD, uintptr(unsafe.Pointer(&tm.nid)))
			if r != 0 {
				res = r
				break
			}
		}

		if res == 0 {
			tm.nid.uFlags = NIF_MESSAGE | NIF_TIP
			tm.nid.cbSize = 936
			r, _, _ := procShellNotifyIconW.Call(NIM_ADD, uintptr(unsafe.Pointer(&tm.nid)))
			if r == 0 {
				log.Printf("[Tray] Shell_NotifyIconW eklenemedi.")
				return
			}
		}
		log.Printf("[Tray] Windows Görev Çubuğu (System Tray) ikonu eklendi.")

		var msg MSG
		for {
			r, _, _ := procGetMessageW.Call(uintptr(unsafe.Pointer(&msg)), 0, 0, 0)
			if r == 0 || int32(r) == -1 {
				break
			}
			procTranslateMessage.Call(uintptr(unsafe.Pointer(&msg)))
			procDispatchMessageW.Call(uintptr(unsafe.Pointer(&msg)))
		}

		procShellNotifyIconW.Call(NIM_DELETE, uintptr(unsafe.Pointer(&tm.nid)))
	}()
}

// OpenURL opens the web dashboard in the user's default Windows browser.
func OpenURL(url string) {
	cmd := exec.Command("cmd", "/c", "start", url)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	_ = cmd.Start()
}

// OpenAppMode opens the dashboard in standalone window mode (via Edge or Chrome).
func OpenAppMode(url string) {
	edgePaths := []string{
		`C:\Program Files (x86)\Microsoft\Edge\Application\msedge.exe`,
		`C:\Program Files\Microsoft\Edge\Application\msedge.exe`,
		`C:\Program Files\Google\Chrome\Application\chrome.exe`,
		`C:\Program Files (x86)\Google\Chrome\Application\chrome.exe`,
	}
	for _, p := range edgePaths {
		if _, err := os.Stat(p); err == nil {
			cmd := exec.Command(p, fmt.Sprintf("--app=%s", url), "--window-size=1280,840")
			_ = cmd.Start()
			return
		}
	}
	OpenURL(url)
}

func (tm *TrayManager) Stop() {
	if tm.hWnd != 0 {
		procDestroyWindow.Call(tm.hWnd)
		tm.hWnd = 0
	}
}
