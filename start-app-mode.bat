@echo off
chcp 65001 >nul
echo ==================================================
echo   Android Sync - Masaüstü Uygulama Modu (App Mode)
echo ==================================================

set "URL=http://localhost:42424"

:: 1. Microsoft Edge Denetimi
if exist "%ProgramFiles(x86)%\Microsoft\Edge\Application\msedge.exe" (
    start "" "%ProgramFiles(x86)%\Microsoft\Edge\Application\msedge.exe" --app=%URL% --window-size=1280,840
    exit /b 0
)

if exist "%ProgramFiles%\Microsoft\Edge\Application\msedge.exe" (
    start "" "%ProgramFiles%\Microsoft\Edge\Application\msedge.exe" --app=%URL% --window-size=1280,840
    exit /b 0
)

:: 2. Google Chrome Denetimi
if exist "%ProgramFiles%\Google\Chrome\Application\chrome.exe" (
    start "" "%ProgramFiles%\Google\Chrome\Application\chrome.exe" --app=%URL% --window-size=1280,840
    exit /b 0
)

if exist "%ProgramFiles(x86)%\Google\Chrome\Application\chrome.exe" (
    start "" "%ProgramFiles(x86)%\Google\Chrome\Application\chrome.exe" --app=%URL% --window-size=1280,840
    exit /b 0
)

:: 3. Varsayılan Tarayıcı Fallback
start %URL%
