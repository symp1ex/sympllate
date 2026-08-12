[CmdletBinding()]
param(
    [ValidateSet('Lite', 'Portable', 'All')]
    [string]$Edition = 'Lite',
    [string]$ModelPath,
    [string]$LlamaRuntimePath,
    [string]$InpaintModelPath,
    [string]$OnnxRuntimePath
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

$gcc = Get-Command gcc -ErrorAction SilentlyContinue
if ($null -eq $gcc) {
    $msysGcc = 'C:\msys64\ucrt64\bin\gcc.exe'
    if (Test-Path -LiteralPath $msysGcc -PathType Leaf) {
        $env:Path = (Split-Path -Parent $msysGcc) + [IO.Path]::PathSeparator + $env:Path
        $gcc = Get-Command gcc -ErrorAction SilentlyContinue
    }
}
if ($null -eq $gcc) {
    throw 'Для сборки ONNX Runtime binding нужен MinGW-w64 GCC (например C:\msys64\ucrt64\bin\gcc.exe).'
}
$env:CGO_ENABLED = '1'
$env:CC = $gcc.Source

$projectRoot = $PSScriptRoot
$frontendRoot = Join-Path $projectRoot 'frontend'
$distRoot = Join-Path $projectRoot 'dist'
$bundledOCRSource = Join-Path $projectRoot 'dist\portable\bin\OCR'

$inpaintModelProvided = $PSBoundParameters.ContainsKey('InpaintModelPath')
$onnxRuntimeProvided = $PSBoundParameters.ContainsKey('OnnxRuntimePath')
if ($inpaintModelProvided -xor $onnxRuntimeProvided) {
    throw 'Параметры -InpaintModelPath и -OnnxRuntimePath необходимо указывать вместе.'
}

$includeInpaintAssets = $inpaintModelProvided
$resolvedInpaintModel = $null
$resolvedOnnxRuntime = $null
if ($includeInpaintAssets) {
    if ([IO.Path]::GetFileName($InpaintModelPath) -cne 'inpainting_lama.onnx' -or -not (Test-Path -LiteralPath $InpaintModelPath -PathType Leaf)) {
        throw "Укажите существующую LaMa-модель с именем inpainting_lama.onnx: $InpaintModelPath"
    }
    if ([IO.Path]::GetFileName($OnnxRuntimePath) -cne 'onnxruntime.dll' -or -not (Test-Path -LiteralPath $OnnxRuntimePath -PathType Leaf)) {
        throw "Укажите существующий ONNX Runtime 1.26.0 DLL с именем onnxruntime.dll: $OnnxRuntimePath"
    }
    $resolvedInpaintModel = (Resolve-Path -LiteralPath $InpaintModelPath).Path
    $resolvedOnnxRuntime = (Resolve-Path -LiteralPath $OnnxRuntimePath).Path
}

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
    $preservedOCR = $null
    if ($Name -eq 'portable' -and (Test-Path -LiteralPath $bundledOCRSource -PathType Container)) {
        $preservedOCR = Join-Path ([IO.Path]::GetTempPath()) ('sympllate-ocr-' + [Guid]::NewGuid().ToString('N'))
        Copy-Item -LiteralPath $bundledOCRSource -Destination $preservedOCR -Recurse -Force
    }
    if (Test-Path -LiteralPath $target) {
        Remove-Item -LiteralPath $target -Recurse -Force
    }
    New-Item -ItemType Directory -Path $target | Out-Null
    if ($null -ne $preservedOCR) {
        $ocrParent = Join-Path $target 'bin'
        New-Item -ItemType Directory -Path $ocrParent -Force | Out-Null
        Copy-Item -LiteralPath $preservedOCR -Destination (Join-Path $ocrParent 'OCR') -Recurse -Force
        Remove-Item -LiteralPath $preservedOCR -Recurse -Force
    }
    return $target
}

function Build-Translator([string]$OutputDirectory) {
    $output = Join-Path $OutputDirectory 'translator.exe'
    go build -trimpath -ldflags='-H windowsgui' -o $output .\cmd\translator
    if ($LASTEXITCODE -ne 0) {
        throw "Go build завершился с кодом $LASTEXITCODE"
    }
    $fontDirectory = Join-Path $OutputDirectory 'bin\fonts'
    New-Item -ItemType Directory -Path $fontDirectory -Force | Out-Null
    go run .\cmd\fontasset -output (Join-Path $fontDirectory 'regular.ttf')
    if ($LASTEXITCODE -ne 0) {
        throw "Подготовка шрифта завершилась с кодом $LASTEXITCODE"
    }
    Copy-Item -LiteralPath (Join-Path $projectRoot 'assets\fonts\GO-FONT-LICENSE.txt') -Destination (Join-Path $fontDirectory 'LICENSE.txt')

    if ($includeInpaintAssets) {
        $inpaintDirectory = Join-Path $OutputDirectory 'bin\inpaint'
        New-Item -ItemType Directory -Path $inpaintDirectory -Force | Out-Null
        Copy-Item -LiteralPath $resolvedInpaintModel -Destination (Join-Path $inpaintDirectory 'inpainting_lama.onnx')
        $onnxDirectory = Join-Path $OutputDirectory 'runtime\onnx'
        New-Item -ItemType Directory -Path $onnxDirectory -Force | Out-Null
        Copy-Item -LiteralPath $resolvedOnnxRuntime -Destination (Join-Path $onnxDirectory 'onnxruntime.dll')

        if (Test-Path -LiteralPath $bundledOCRSource -PathType Container) {
            $ocrTarget = Join-Path $OutputDirectory 'bin\OCR'
            New-Item -ItemType Directory -Path $ocrTarget -Force | Out-Null
            $resolvedOCRSource = (Resolve-Path -LiteralPath $bundledOCRSource).Path
            $resolvedOCRTarget = (Resolve-Path -LiteralPath $ocrTarget).Path
            if ($resolvedOCRSource -ne $resolvedOCRTarget) {
                Get-ChildItem -LiteralPath $bundledOCRSource -File | Where-Object { $_.Extension -in @('.onnx', '.yml') } | ForEach-Object {
                    Copy-Item -LiteralPath $_.FullName -Destination $ocrTarget
                }
            }
        }
    }
}

function Build-Lite {
    $target = New-CleanEditionDirectory 'lite'
    Build-Translator $target
    if ($includeInpaintAssets) {
        Copy-Item -LiteralPath (Join-Path $projectRoot 'config.example.json') -Destination (Join-Path $target 'config.example.json')
    }
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
