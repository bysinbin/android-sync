# Android-Windows Sync Start Script (PowerShell)
$root = $PSScriptRoot

if (-not (Test-Path "$root\windows-sync.exe")) {
    Write-Host "[Bilgi] windows-sync.exe bulunamadı, derleniyor..." -ForegroundColor Yellow
    & "$root\build-windows.ps1"
}

Write-Host "🚀 Android-Windows Sync başlatılıyor..." -ForegroundColor Green
Start-Process -FilePath "$root\windows-sync.exe" -WorkingDirectory "$root"

Write-Host "✅ Servis arka planda ve Görev Çubuğunda (System Tray) başlatıldı." -ForegroundColor Cyan
Write-Host "🌐 Kontrol Paneli: http://localhost:42424" -ForegroundColor Cyan
Start-Sleep -Seconds 2
