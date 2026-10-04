@echo off
setlocal
echo ==================================================
echo Building Android-Windows Sync...
echo ==================================================

set "ROOT=%~dp0"
cd /d "%ROOT%windows-daemon"
echo [Go] Compiling windows-sync.exe...
go build -ldflags="-s -w" -o "%ROOT%windows-sync.exe" ./cmd/windows-sync
if %ERRORLEVEL% NEQ 0 (
    echo [ERROR] Build failed! Code: %ERRORLEVEL%
    exit /b %ERRORLEVEL%
)

echo.
echo [SUCCESS] windows-sync.exe has been built successfully!
echo Path: %ROOT%windows-sync.exe
echo ==================================================
