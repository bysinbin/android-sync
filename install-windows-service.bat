@echo off
setlocal
echo ==================================================
echo Installing Android-Windows Sync to Windows Startup
echo ==================================================

if not exist "%~dp0windows-sync.exe" (
    echo [INFO] windows-sync.exe not found, building first...
    call "%~dp0build-windows.bat"
)

powershell -NoProfile -ExecutionPolicy Bypass -Command "$ws = New-Object -ComObject WScript.Shell; $s = $ws.CreateShortcut([Environment]::GetFolderPath('Startup') + '\AndroidSync.lnk'); $s.TargetPath = '%~dp0windows-sync.exe'; $s.WorkingDirectory = '%~dp0'; $s.Save()"

if %ERRORLEVEL% EQU 0 (
    echo.
    echo [SUCCESS] Android-Windows Sync will automatically start on Windows boot!
) else (
    echo [ERROR] Failed to create startup shortcut.
)
echo ==================================================
pause
