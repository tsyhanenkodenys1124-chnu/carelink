# Запуск «Догляду» однією командою на Windows.
#
#   .\start.ps1          звичайний запуск, база data\doglyad.db
#   .\start.ps1 -Demo    запуск із демонстраційними даними, база data\demo.db
#   .\start.ps1 -Port 9000
#
# Скрипт збирає інтерфейс, збирає сервер і запускає його. Сервер сам роздає
# і статику, і API, тому окремо запускати Vite не потрібно.

param(
    [ValidateRange(1024, 65535)][int]$Port = 8080,
    [switch]$Demo
)

$ErrorActionPreference = 'Stop'
$projectRoot = $PSScriptRoot
$backendPath = Join-Path $projectRoot 'backend'
$webPath = Join-Path $projectRoot 'web'
$dataPath = Join-Path $backendPath 'data'

Get-Command go, npm -ErrorAction Stop | Out-Null

if (Get-NetTCPConnection -LocalPort $Port -State Listen -ErrorAction SilentlyContinue) {
    throw "Порт $Port уже зайнятий. Відкрийте http://127.0.0.1:$Port або вкажіть інший: -Port 9000"
}

New-Item -ItemType Directory -Force -Path $dataPath | Out-Null

Write-Host 'Збираємо інтерфейс…' -ForegroundColor Cyan
Push-Location $webPath
try {
    if (!(Test-Path (Join-Path $webPath 'node_modules'))) {
        npm ci
        if ($LASTEXITCODE -ne 0) { throw 'Не вдалося встановити залежності інтерфейсу.' }
    }
    npm run build
    if ($LASTEXITCODE -ne 0) { throw 'Помилка збірки інтерфейсу. Сервер не запущено.' }
} finally {
    Pop-Location
}

Write-Host 'Збираємо сервер…' -ForegroundColor Cyan
$binary = Join-Path $dataPath 'doglyad.exe'
Push-Location $backendPath
try {
    go build -o $binary ./cmd/api
    if ($LASTEXITCODE -ne 0) { throw 'Помилка збірки сервера.' }

    if ($Demo) {
        Write-Host 'Створюємо демонстраційні дані…' -ForegroundColor Cyan
        $env:DATABASE_PATH = Join-Path $dataPath 'demo.db'
        go run ./cmd/seed
        if ($LASTEXITCODE -ne 0) { throw 'Не вдалося створити демонстраційні дані.' }
    }
} finally {
    Pop-Location
}

$databaseFile = if ($Demo) { 'demo.db' } else { 'doglyad.db' }
$env:DATABASE_PATH = Join-Path $dataPath $databaseFile
$env:MEDIA_DIR = Join-Path $dataPath 'media'
$env:WEB_DIR = Join-Path $webPath 'dist'
$env:ADDR = ":$Port"
$env:PUBLIC_URL = "http://127.0.0.1:$Port"

Write-Host ''
Write-Host "Застосунок працює: http://127.0.0.1:$Port" -ForegroundColor Green
if ($Demo) {
    Write-Host 'Демо-вхід: olena.demo@doglyad.local / DemoCare2026!' -ForegroundColor Green
}
Write-Host 'Зупинити: Ctrl+C' -ForegroundColor DarkGray
Write-Host ''

& $binary
