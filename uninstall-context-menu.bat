@echo off
chcp 65001 >nul
echo ==================================================
echo   Android Sync - Sağ Tık Menüsünü Kaldırma
echo ==================================================

echo.
echo [*] Windows Gezgini sağ tık menüsü kaldırılıyor...

reg delete "HKCU\Software\Classes\*\shell\SendToAndroidSync" /f >nul 2>&1
reg delete "HKCU\Software\Classes\Directory\shell\SendToAndroidSync" /f >nul 2>&1

echo [BAŞARILI] Sağ tık menüsü başarıyla kaldırıldı.
echo.
pause
