$ErrorActionPreference = 'Stop'

if (-not (Get-Command node -ErrorAction SilentlyContinue)) {
    throw 'Node.js не найден. Установите актуальную LTS-версию Node.js.'
}
if (-not (Get-Command npm -ErrorAction SilentlyContinue)) {
    throw 'npm не найден.'
}
if (-not (Get-Command go -ErrorAction SilentlyContinue)) {
    throw 'Go не найден. Установите Go 1.24 или новее.'
}

$projectRoot = $PSScriptRoot
$frontendRoot = Join-Path $projectRoot 'frontend'
$distRoot = Join-Path $projectRoot 'dist'

Push-Location $frontendRoot
try {
    if (-not (Test-Path (Join-Path $frontendRoot 'node_modules'))) {
        if (Test-Path (Join-Path $frontendRoot 'package-lock.json')) {
            npm ci
        } else {
            npm install
        }
    }
    npm run build
} finally {
    Pop-Location
}

New-Item -ItemType Directory -Force -Path $distRoot | Out-Null
go build -trimpath -ldflags='-H windowsgui' -o (Join-Path $distRoot 'translator.exe') .\cmd\translator
Copy-Item -Force (Join-Path $projectRoot 'config.example.json') (Join-Path $distRoot 'config.example.json')

Write-Host "Готово: $distRoot"

