@echo off
chcp 65001 >nul
echo ==================================================
echo   Android Sync - Windows Sağ Tık Menüsü Kurulumu
echo ==================================================

set "EXE_PATH=%~dp0windows-sync.exe"

if not exist "%EXE_PATH%" (
    echo [HATA] windows-sync.exe dosyasi bulunamadi: %EXE_PATH%
    pause
    exit /b 1
)

echo.
echo [*] Windows Gezgini sağ tık menüsüne ekleniyor...

:: Dosyalar için sağ tık menüsü
reg add "HKCU\Software\Classes\*\shell\SendToAndroidSync" /ve /d "Android Telefona Gönder" /f >nul
reg add "HKCU\Software\Classes\*\shell\SendToAndroidSync" /v "Icon" /d "%EXE_PATH%" /f >nul
reg add "HKCU\Software\Classes\*\shell\SendToAndroidSync\command" /ve /d "\"%EXE_PATH%\" --send-file \"%%1\"" /f >nul

:: Klasörler için sağ tık menüsü
reg add "HKCU\Software\Classes\Directory\shell\SendToAndroidSync" /ve /d "Android Telefona Gönder" /f >nul
reg add "HKCU\Software\Classes\Directory\shell\SendToAndroidSync" /v "Icon" /d "%EXE_PATH%" /f >nul
reg add "HKCU\Software\Classes\Directory\shell\SendToAndroidSync\command" /ve /d "\"%EXE_PATH%\" --send-file \"%%1\"" /f >nul

echo [BAŞARILI] Sağ tık menüsü eklendi!
echo Artık herhangi bir dosyaya sağ tıklayıp "Android Telefona Gönder" seçebilirsiniz.
echo.
pause
