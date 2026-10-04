@echo off
setlocal
echo ==================================================
echo Removing Android-Windows Sync from Windows Startup
echo ==================================================

powershell -NoProfile -ExecutionPolicy Bypass -Command "$path = [Environment]::GetFolderPath('Startup') + '\AndroidSync.lnk'; if (Test-Path $path) { Remove-Item $path; Write-Output '[SUCCESS] Startup shortcut removed.' } else { Write-Output '[INFO] Startup shortcut does not exist.' }"

echo.
echo Finished.
echo ==================================================
pause
