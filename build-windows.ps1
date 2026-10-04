# Android-Windows Sync Build Script (PowerShell)
$ErrorActionPreference = "Stop"
$root = $PSScriptRoot

Write-Host "==================================================" -ForegroundColor Cyan
Write-Host "🔨 Android-Windows Sync Derleniyor..." -ForegroundColor Cyan
Write-Host "==================================================" -ForegroundColor Cyan

Push-Location "$root\windows-daemon"
try {
    Write-Host "[Go] windows-sync.exe derleniyor..." -ForegroundColor Yellow
    go build -ldflags="-s -w" -o "$root\windows-sync.exe" ./cmd/windows-sync
    Write-Host "`n✅ windows-sync.exe başarıyla oluşturuldu!" -ForegroundColor Green
    Write-Host "📍 Konum: $root\windows-sync.exe" -ForegroundColor Gray
} finally {
    Pop-Location
}
