@echo off
echo ==================================================
echo Android-Windows Sync Durduruluyor...
echo ==================================================
taskkill /F /IM windows-sync.exe >nul 2>&1
if %ERRORLEVEL% EQU 0 (
    echo [OK] windows-sync.exe basariyla durduruldu.
) else (
    echo [BILGI] Calisan bir windows-sync.exe bulunamadi.
)
echo ==================================================
