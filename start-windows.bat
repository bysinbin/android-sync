@echo off
setlocal
cd /d "%~dp0"

if not exist "%~dp0windows-sync.exe" (
    echo [INFO] windows-sync.exe not found, building now...
    call "%~dp0build-windows.bat"
)

echo [INFO] Starting Android-Windows Sync...
start "" "%~dp0windows-sync.exe"
echo [SUCCESS] Service started in background and System Tray.
echo [WEB] Dashboard available at: http://localhost:42424
timeout /t 3 >nul
