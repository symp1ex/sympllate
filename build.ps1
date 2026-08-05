[CmdletBinding()]
param(
    [ValidateSet('Lite', 'Portable', 'All')]
    [string]$Edition = 'Lite',
    [string]$ModelPath,
    [string]$LlamaRuntimePath
)

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

$resolvedModel = $null
$resolvedRuntime = $null
if ($Edition -in @('Portable', 'All')) {
    if ([string]::IsNullOrWhiteSpace($ModelPath)) {
        throw 'Для Portable укажите -ModelPath к существующей GGUF-модели.'
    }
    if ([IO.Path]::GetExtension($ModelPath) -ine '.gguf' -or -not (Test-Path -LiteralPath $ModelPath -PathType Leaf)) {
        throw "Portable-модель должна быть существующим .gguf файлом: $ModelPath"
    }
    if ([string]::IsNullOrWhiteSpace($LlamaRuntimePath) -or -not (Test-Path -LiteralPath $LlamaRuntimePath -PathType Container)) {
        throw "Для Portable укажите -LlamaRuntimePath к существующему каталогу llama.cpp runtime: $LlamaRuntimePath"
    }
    $serverPath = Join-Path $LlamaRuntimePath 'llama-server.exe'
    if (-not (Test-Path -LiteralPath $serverPath -PathType Leaf)) {
        throw "В каталоге llama.cpp runtime отсутствует llama-server.exe: $serverPath"
    }
    $resolvedModel = (Resolve-Path -LiteralPath $ModelPath).Path
    $resolvedRuntime = (Resolve-Path -LiteralPath $LlamaRuntimePath).Path
}

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

function New-CleanEditionDirectory([string]$Name) {
    $target = Join-Path $distRoot $Name
    if (Test-Path -LiteralPath $target) {
        Remove-Item -LiteralPath $target -Recurse -Force
    }
    New-Item -ItemType Directory -Path $target | Out-Null
    return $target
}

function Build-Translator([string]$OutputDirectory) {
    $output = Join-Path $OutputDirectory 'translator.exe'
    go build -trimpath -ldflags='-H windowsgui' -o $output .\cmd\translator
    if ($LASTEXITCODE -ne 0) {
        throw "Go build завершился с кодом $LASTEXITCODE"
    }
}

function Build-Lite {
    $target = New-CleanEditionDirectory 'lite'
    Build-Translator $target
    Copy-Item -LiteralPath (Join-Path $projectRoot 'config.example.json') -Destination (Join-Path $target 'config.example.json')
    Write-Host "Lite готов: $target"
}

function Build-Portable {
    $target = New-CleanEditionDirectory 'portable'
    Build-Translator $target

    $modelsTarget = Join-Path $target 'models'
    $runtimeTarget = Join-Path $target 'runtime\llama'
    New-Item -ItemType Directory -Path $modelsTarget | Out-Null
    New-Item -ItemType Directory -Path $runtimeTarget -Force | Out-Null
    $modelName = [IO.Path]::GetFileName($resolvedModel)
    Copy-Item -LiteralPath $resolvedModel -Destination (Join-Path $modelsTarget $modelName)
    Get-ChildItem -LiteralPath $resolvedRuntime -Force | ForEach-Object {
        Copy-Item -LiteralPath $_.FullName -Destination $runtimeTarget -Recurse -Force
    }

    $portableConfig = Get-Content -LiteralPath (Join-Path $projectRoot 'config.example.json') -Raw | ConvertFrom-Json
    $portableConfig.provider.active = 'local'
    $portableConfig.localModel.modelFile = ('models/' + $modelName)
    $configJSON = $portableConfig | ConvertTo-Json -Depth 10
    $utf8WithoutBOM = New-Object System.Text.UTF8Encoding($false)
    [IO.File]::WriteAllText((Join-Path $target 'config.json'), $configJSON + [Environment]::NewLine, $utf8WithoutBOM)
    Write-Host "Portable готов: $target"
}

switch ($Edition) {
    'Lite' { Build-Lite }
    'Portable' { Build-Portable }
    'All' {
        Build-Lite
        Build-Portable
    }
}
