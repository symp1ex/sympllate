# build-onefile-ffmpeg.ps1
# Minimal single-file FFmpeg for Sympllate:
#   PNG/JPEG/WebP/TIFF/BMP single-image decoding
#   OCR preprocessing with crop/scale/format/eq/unsharp
#   PNG/BMP/TIFF encoding via native FFmpeg encoders
#   Static WebP encoding via libwebp
#
# Output:
#   One standalone ffmpeg.exe is copied next to this script.
#
# Important:
#   This script DOES NOT run pacman -Sy or pacman -Suy.
#   It does not refresh MSYS2 package databases.
#
# Note about licenses:
#   This builds a statically linked FFmpeg executable.
#   --enable-gpl is required by FFmpeg's eq filter, so the resulting FFmpeg
#   executable is GPL. zlib/libwebp use permissive licenses. If you distribute
#   the executable, make sure the distribution process satisfies the applicable
#   license terms, including FFmpeg's static-linking requirements.

$ErrorActionPreference = "Stop"

$ScriptDir  = Split-Path -Parent $MyInvocation.MyCommand.Path
$MsysRoot   = "C:\msys64"
$BashExe    = Join-Path $MsysRoot "usr\bin\bash.exe"

$WorkDir    = Join-Path $ScriptDir "_ffmpeg_build"
$OutDir     = $ScriptDir

$MsysUrl    = "https://github.com/msys2/msys2-installer/releases/latest/download/msys2-x86_64-latest.exe"
$MsysSetup  = Join-Path $WorkDir "msys2-x86_64-latest.exe"

$FfmpegVer  = "8.1.2"
$FfmpegUrl  = "https://ffmpeg.org/releases/ffmpeg-$FfmpegVer.tar.xz"
$FfmpegArc  = Join-Path $WorkDir "ffmpeg-$FfmpegVer.tar.xz"
$FfmpegDir  = Join-Path $WorkDir "ffmpeg-$FfmpegVer"
$InstallDir = Join-Path $WorkDir "install-onefile"

New-Item -ItemType Directory -Force -Path $WorkDir | Out-Null
New-Item -ItemType Directory -Force -Path $InstallDir | Out-Null

function Write-Step {
    param([string]$Text)
    Write-Host ""
    Write-Host "==> $Text"
}

function Install-MSYS2-IfMissing {
    if (Test-Path $BashExe) {
        Write-Host "MSYS2 found: $BashExe"
        return
    }

    Write-Step "MSYS2 was not found. Downloading MSYS2 installer"

    if (-not (Test-Path $MsysSetup)) {
        Invoke-WebRequest -Uri $MsysUrl -OutFile $MsysSetup
    }

    Write-Step "Installing MSYS2 to $MsysRoot"

    if (Test-Path $MsysRoot) {
        throw "Directory exists but bash.exe was not found: $MsysRoot. Delete it completely or install MSYS2 manually."
    }

    $args = @(
        "install",
        "--root", $MsysRoot,
        "--confirm-command",
        "--accept-messages",
        "--accept-licenses"
    )

    $p = Start-Process -FilePath $MsysSetup -ArgumentList $args -Wait -PassThru

    if ($p.ExitCode -ne 0) {
        throw "MSYS2 installer failed with exit code $($p.ExitCode)"
    }

    if (-not (Test-Path $BashExe)) {
        throw "MSYS2 installation finished, but bash.exe was not found: $BashExe"
    }
}

function Invoke-MSYS2Script {
    param(
        [Parameter(Mandatory = $true)]
        [string]$ScriptPath
    )

    $env:MSYSTEM = "UCRT64"
    $env:CHERE_INVOKING = "1"

    $env:WIN_SCRIPT_DIR  = $ScriptDir
    $env:WIN_WORK_DIR    = $WorkDir
    $env:WIN_OUT_DIR     = $OutDir
    $env:WIN_FFMPEG_DIR  = $FfmpegDir
    $env:WIN_INSTALL_DIR = $InstallDir
    $env:WIN_FFMPEG_VER  = $FfmpegVer

    & $BashExe --login $ScriptPath

    if ($LASTEXITCODE -ne 0) {
        throw "MSYS2 script failed with exit code $LASTEXITCODE"
    }
}

function Save-TextUtf8NoBom {
    param(
        [Parameter(Mandatory = $true)]
        [string]$Path,

        [Parameter(Mandatory = $true)]
        [string]$Text
    )

    $utf8NoBom = New-Object System.Text.UTF8Encoding($false)
    [System.IO.File]::WriteAllText($Path, $Text, $utf8NoBom)
}

function Assert-NonEmptyFile {
    param(
        [Parameter(Mandatory = $true)]
        [string]$Path
    )

    $item = Get-Item -LiteralPath $Path -ErrorAction SilentlyContinue
    if ($null -eq $item -or $item.Length -le 0) {
        throw "Expected a non-empty file: $Path"
    }
}

function Invoke-FFmpegSmokeCommand {
    param(
        [Parameter(Mandatory = $true)]
        [string]$Description,

        [Parameter(Mandatory = $true)]
        [string[]]$Arguments,

        [Parameter(Mandatory = $true)]
        [string]$OutputPath
    )

    Write-Host "  $Description"
    & $ResultExe @Arguments
    if ($LASTEXITCODE -ne 0) {
        throw "FFmpeg smoke test failed: $Description (exit code $LASTEXITCODE)"
    }
    Assert-NonEmptyFile -Path $OutputPath
}

Install-MSYS2-IfMissing

Write-Step "Writing MSYS2 build scripts"

$CheckScript   = Join-Path $WorkDir "00_check_packages.sh"
$ExtractScript = Join-Path $WorkDir "01_extract_ffmpeg.sh"
$BuildScript   = Join-Path $WorkDir "02_build_ffmpeg_onefile.sh"
$VerifyScript  = Join-Path $WorkDir "03_verify_onefile.sh"

Save-TextUtf8NoBom -Path $CheckScript -Text @'
#!/usr/bin/env bash
set -euo pipefail

echo "Checking required build tools without refreshing package databases..."
echo ""

missing=0

for cmd in gcc make pkg-config nasm tar xz strip objdump windres; do
  if ! command -v "$cmd" >/dev/null 2>&1; then
    echo "missing command: $cmd"
    missing=1
  fi
done

for package in zlib libwebp; do
  if ! pkg-config --exists "$package"; then
    echo "missing pkg-config package: $package"
    missing=1
  fi

  if ! pkg-config --static --libs "$package" >/dev/null 2>&1; then
    echo "pkg-config cannot resolve static $package libs"
    missing=1
  fi
done

zlib_static_lib="$(pkg-config --variable=libdir zlib 2>/dev/null || true)/libz.a"
if [ ! -f "$zlib_static_lib" ]; then
  echo "missing static library: $zlib_static_lib"
  missing=1
fi

webp_static_lib="$(pkg-config --variable=libdir libwebp 2>/dev/null || true)/libwebp.a"
if [ ! -f "$webp_static_lib" ]; then
  echo "missing static library: $webp_static_lib"
  missing=1
fi

if [ "$missing" = "0" ]; then
  echo "All required build tools and static libraries are already installed."
  exit 0
fi

echo ""
echo "Some required packages are missing."
echo "Trying to install them WITHOUT pacman -Sy and WITHOUT pacman -Suy..."
echo ""

pacman -S --needed --noconfirm \
  make pkgconf diffutils tar xz \
  mingw-w64-ucrt-x86_64-cc \
  mingw-w64-ucrt-x86_64-binutils \
  mingw-w64-ucrt-x86_64-pkgconf \
  mingw-w64-ucrt-x86_64-nasm \
  mingw-w64-ucrt-x86_64-zlib \
  mingw-w64-ucrt-x86_64-libwebp

echo ""
echo "Re-checking required build tools and static libraries..."

missing=0

for cmd in gcc make pkg-config nasm tar xz strip objdump windres; do
  if ! command -v "$cmd" >/dev/null 2>&1; then
    echo "missing command after install attempt: $cmd"
    missing=1
  fi
done

for package in zlib libwebp; do
  if ! pkg-config --exists "$package"; then
    echo "missing pkg-config package after install attempt: $package"
    missing=1
  fi

  if ! pkg-config --static --libs "$package" >/dev/null 2>&1; then
    echo "pkg-config cannot resolve static $package libs after install attempt"
    missing=1
  fi
done

zlib_static_lib="$(pkg-config --variable=libdir zlib 2>/dev/null || true)/libz.a"
if [ ! -f "$zlib_static_lib" ]; then
  echo "missing static library after install attempt: $zlib_static_lib"
  missing=1
fi

webp_static_lib="$(pkg-config --variable=libdir libwebp 2>/dev/null || true)/libwebp.a"
if [ ! -f "$webp_static_lib" ]; then
  echo "missing static library after install attempt: $webp_static_lib"
  missing=1
fi

if [ "$missing" != "0" ]; then
  echo ""
  echo "Required packages/static libraries are still missing." >&2
  echo "Install these packages manually in MSYS2 UCRT64, then run this script again:" >&2
  echo "  pacman -S --needed make pkgconf diffutils tar xz mingw-w64-ucrt-x86_64-cc mingw-w64-ucrt-x86_64-binutils mingw-w64-ucrt-x86_64-pkgconf mingw-w64-ucrt-x86_64-nasm mingw-w64-ucrt-x86_64-zlib mingw-w64-ucrt-x86_64-libwebp" >&2
  exit 1
fi

echo "All required build tools and static libraries are installed."
exit 0
'@

Save-TextUtf8NoBom -Path $ExtractScript -Text @'
#!/usr/bin/env bash
set -euo pipefail

WORK_DIR="$(cygpath -u "$WIN_WORK_DIR")"
FFMPEG_VER="${WIN_FFMPEG_VER}"
FFMPEG_ARC="$WORK_DIR/ffmpeg-$FFMPEG_VER.tar.xz"

cd "$WORK_DIR"

rm -rf "ffmpeg-$FFMPEG_VER"

if [ ! -f "$FFMPEG_ARC" ]; then
  echo "FFmpeg archive was not found: $FFMPEG_ARC" >&2
  exit 1
fi

tar -xf "$FFMPEG_ARC"

if [ ! -d "$WORK_DIR/ffmpeg-$FFMPEG_VER" ]; then
  echo "FFmpeg source directory was not created after extract" >&2
  exit 1
fi

exit 0
'@

Save-TextUtf8NoBom -Path $BuildScript -Text @'
#!/usr/bin/env bash
set -euo pipefail

SRC_DIR="$(cygpath -u "$WIN_FFMPEG_DIR")"
INSTALL_DIR="$(cygpath -u "$WIN_INSTALL_DIR")"
WORK_DIR="$(cygpath -u "$WIN_WORK_DIR")"

MANIFEST_DIR="$WORK_DIR/manifest"
MANIFEST_FILE="$MANIFEST_DIR/ffmpeg.exe.manifest"
MANIFEST_RC="$MANIFEST_DIR/ffmpeg-manifest.rc"
MANIFEST_OBJ="$MANIFEST_DIR/ffmpeg-manifest.o"

rm -rf "$MANIFEST_DIR"
mkdir -p "$MANIFEST_DIR"

cat > "$MANIFEST_FILE" <<'EOF_MANIFEST'
<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<assembly xmlns="urn:schemas-microsoft-com:asm.v1" manifestVersion="1.0">
  <application xmlns="urn:schemas-microsoft-com:asm.v3">
    <windowsSettings>
      <dpiAwareness xmlns="http://schemas.microsoft.com/SMI/2016/WindowsSettings">
        PerMonitorV2
      </dpiAwareness>
      <dpiAware xmlns="http://schemas.microsoft.com/SMI/2005/WindowsSettings">
        true/pm
      </dpiAware>
    </windowsSettings>
  </application>
</assembly>
EOF_MANIFEST

cat > "$MANIFEST_RC" <<'EOF_RC'
1 24 "ffmpeg.exe.manifest"
EOF_RC

(
  cd "$MANIFEST_DIR"

  windres \
    --input-format=rc \
    --output-format=coff \
    --target=pe-x86-64 \
    --input="ffmpeg-manifest.rc" \
    --output="ffmpeg-manifest.o"
)

if [ ! -f "$MANIFEST_OBJ" ]; then
  echo "Manifest resource object was not created: $MANIFEST_OBJ" >&2
  exit 1
fi

echo "Windows manifest created: $MANIFEST_FILE"
echo "Windows manifest resource created: $MANIFEST_OBJ"
echo "Windows DPI awareness: PerMonitorV2"

cd "$SRC_DIR"

make distclean >/dev/null 2>&1 || true
rm -rf "$INSTALL_DIR"
mkdir -p "$INSTALL_DIR"

# The key one-file switches are:
#   --enable-static --disable-shared
#   --pkg-config-flags=--static
#   --extra-ldflags=-static
#   --extra-ldexeflags=-static
# They force FFmpeg, zlib, and libwebp to be linked into ffmpeg.exe instead
# of emitted as DLLs.
./configure \
  --prefix="$INSTALL_DIR" \
  --target-os=mingw32 \
  --arch=x86_64 \
  --enable-static \
  --disable-shared \
  --pkg-config-flags="--static" \
  --extra-ldflags="-static -static-libgcc -static-libstdc++" \
  --extra-ldexeflags="-static -static-libgcc -static-libstdc++ $MANIFEST_OBJ" \
  --disable-autodetect \
  --disable-debug \
  --disable-doc \
  --disable-network \
  --enable-small \
  --disable-runtime-cpudetect \
  --disable-everything \
  --disable-ffplay \
  --disable-ffprobe \
  --disable-avdevice \
  --disable-swresample \
  --enable-ffmpeg \
  --enable-gpl \
  --enable-zlib \
  --enable-libwebp \
  --enable-swscale \
  --enable-protocol=file \
  --enable-demuxer=image_png_pipe \
  --enable-demuxer=image_jpeg_pipe \
  --enable-demuxer=image_webp_pipe \
  --enable-demuxer=image_tiff_pipe \
  --enable-demuxer=image_bmp_pipe \
  --enable-muxer=image2 \
  --enable-muxer=webp \
  --enable-decoder=png \
  --enable-decoder=mjpeg \
  --enable-decoder=webp \
  --enable-decoder=tiff \
  --enable-decoder=bmp \
  --enable-encoder=png \
  --enable-encoder=bmp \
  --enable-encoder=tiff \
  --enable-encoder=libwebp \
  --enable-filter=crop \
  --enable-filter=scale \
  --enable-filter=format \
  --enable-filter=eq \
  --enable-filter=unsharp

require_config() {
  local name="$1"
  if ! grep -qx "CONFIG_${name}=yes" ffbuild/config.mak; then
    echo "ERROR: required FFmpeg component was not enabled: CONFIG_${name}" >&2
    exit 1
  fi
}

required_configs=(
  STATIC FFMPEG GPL ZLIB LIBWEBP SWSCALE FILE_PROTOCOL
  IMAGE_PNG_PIPE_DEMUXER IMAGE_JPEG_PIPE_DEMUXER IMAGE_WEBP_PIPE_DEMUXER
  IMAGE_TIFF_PIPE_DEMUXER IMAGE_BMP_PIPE_DEMUXER
  IMAGE2_MUXER WEBP_MUXER
  PNG_DECODER MJPEG_DECODER WEBP_DECODER TIFF_DECODER BMP_DECODER
  PNG_ENCODER BMP_ENCODER TIFF_ENCODER LIBWEBP_ENCODER
  CROP_FILTER SCALE_FILTER FORMAT_FILTER EQ_FILTER UNSHARP_FILTER
)

for config in "${required_configs[@]}"; do
  require_config "$config"
done

if grep -qx 'CONFIG_LIBWEBP_ANIM_ENCODER=yes' ffbuild/config.mak; then
  echo "ERROR: libwebp_anim was enabled; the minimal build must not require libwebpmux." >&2
  exit 1
fi

echo ""
echo "Configured linkage and libraries:"
grep -E '^!?CONFIG_(STATIC|SHARED|GPL|ZLIB|LIBWEBP|SWSCALE)=' ffbuild/config.mak || true

echo ""
echo "Configured programs:"
grep -E '^!?CONFIG_(FFMPEG|FFPLAY|FFPROBE)=' ffbuild/config.mak || true

echo ""
echo "Configured image decoders:"
grep -E '^!?CONFIG_(PNG|MJPEG|WEBP|TIFF|BMP)_DECODER=' ffbuild/config.mak || true

echo ""
echo "Configured image encoders:"
grep -E '^!?CONFIG_(PNG|BMP|TIFF|LIBWEBP|LIBWEBP_ANIM)_ENCODER=' ffbuild/config.mak || true

echo ""
echo "Configured image demuxers and muxers:"
grep -E '^!?CONFIG_IMAGE_(PNG|JPEG|WEBP|TIFF|BMP)_PIPE_DEMUXER=|^!?CONFIG_(IMAGE2|WEBP)_MUXER=' ffbuild/config.mak || true

echo ""
echo "Configured OCR filters and local-file protocol:"
grep -E '^!?CONFIG_(CROP|SCALE|FORMAT|EQ|UNSHARP)_FILTER=|^!?CONFIG_FILE_PROTOCOL=' ffbuild/config.mak || true

make -j"$(nproc)" V=0
make install

if [ ! -f "$INSTALL_DIR/bin/ffmpeg.exe" ]; then
  echo ""
  echo "ffmpeg.exe was not installed. Trying direct program build/install..."
  make ffmpeg.exe -j"$(nproc)" V=0
  mkdir -p "$INSTALL_DIR/bin"

  if [ -f "ffmpeg.exe" ]; then
    cp -f "ffmpeg.exe" "$INSTALL_DIR/bin/ffmpeg.exe"
  fi
fi

if [ ! -f "$INSTALL_DIR/bin/ffmpeg.exe" ]; then
  echo ""
  echo "ERROR: ffmpeg.exe was not produced." >&2
  echo "Check these lines from ffbuild/config.mak:" >&2
  grep -E '^!?CONFIG_FFMPEG=' ffbuild/config.mak >&2 || true
  grep -E '^!?CONFIG_(PNG|MJPEG|WEBP|TIFF|BMP|LIBWEBP).*_(DECODER|ENCODER)=' ffbuild/config.mak >&2 || true
  exit 1
fi

strip "$INSTALL_DIR/bin/ffmpeg.exe" 2>/dev/null || true

exit 0
'@

Save-TextUtf8NoBom -Path $VerifyScript -Text @'
#!/usr/bin/env bash
set -euo pipefail

INSTALL_DIR="$(cygpath -u "$WIN_INSTALL_DIR")"
OUT_DIR="$(cygpath -u "$WIN_OUT_DIR")"

mkdir -p "$OUT_DIR"

if [ ! -f "$INSTALL_DIR/bin/ffmpeg.exe" ]; then
  echo "ffmpeg.exe was not found: $INSTALL_DIR/bin/ffmpeg.exe" >&2
  exit 1
fi

cp -f "$INSTALL_DIR/bin/ffmpeg.exe" "$OUT_DIR/ffmpeg.exe"

# Remove stale DLLs from older dynamic builds in this output directory.
# This is intentionally conservative: it removes only DLLs that are normally
# produced/copied by previous FFmpeg build scripts.
rm -f "$OUT_DIR"/avcodec-*.dll \
      "$OUT_DIR"/avdevice-*.dll \
      "$OUT_DIR"/avfilter-*.dll \
      "$OUT_DIR"/avformat-*.dll \
      "$OUT_DIR"/avutil-*.dll \
      "$OUT_DIR"/postproc-*.dll \
      "$OUT_DIR"/swresample-*.dll \
      "$OUT_DIR"/swscale-*.dll \
      "$OUT_DIR"/libwebp*.dll \
      "$OUT_DIR"/libsharpyuv*.dll \
      "$OUT_DIR"/zlib*.dll \
      "$OUT_DIR"/libz*.dll \
      "$OUT_DIR"/libgcc_s_*.dll \
      "$OUT_DIR"/libstdc++-*.dll \
      "$OUT_DIR"/libwinpthread-*.dll 2>/dev/null || true

echo ""
echo "Checking imported DLLs for ffmpeg.exe..."

imports="$(objdump -p "$OUT_DIR/ffmpeg.exe" | sed -n 's/^[[:space:]]*DLL Name: //p' || true)"
printf '%s\n' "$imports"

# For standalone distribution the real problem is importing MSYS2/MinGW/FFmpeg
# DLLs. Windows system DLLs are expected and are not shipped next to ffmpeg.exe.
bad_imports="$(printf '%s\n' "$imports" | grep -Ei '^(avcodec|avdevice|avfilter|avformat|avutil|postproc|swresample|swscale)-[0-9]+\.dll$|^lib(webp|sharpyuv|gcc|stdc\+\+|winpthread|bz2|brotli|iconv|intl|lzma|z|zstd|xml2|ssl|crypto).*\.dll$|^(zlib1|libzlib|libssp).*\.dll$' || true)"

if [ -n "$bad_imports" ]; then
  echo ""
  echo "ERROR: ffmpeg.exe still imports non-system MSYS2/FFmpeg DLLs:" >&2
  printf '%s\n' "$bad_imports" >&2
  echo ""
  echo "This means one dependency was linked dynamically." >&2
  echo "Make sure MSYS2 UCRT64 has static zlib/libwebp libraries and configure used --pkg-config-flags=--static." >&2
  exit 1
fi

echo "One-file check passed: no FFmpeg/zlib/libwebp/MSYS2 DLL imports were detected."
echo "Note: Windows system DLL imports are normal and do not need to be bundled."

echo ""
echo "Checking embedded Windows manifest..."

if ! objdump -h "$OUT_DIR/ffmpeg.exe" | grep -q '\.rsrc'; then
  echo "ERROR: ffmpeg.exe does not contain a Windows resource section." >&2
  exit 1
fi

if ! grep -a -q "PerMonitorV2" "$OUT_DIR/ffmpeg.exe"; then
  echo "ERROR: PerMonitorV2 was not found inside ffmpeg.exe." >&2
  exit 1
fi

echo "Embedded manifest check passed: PerMonitorV2 was found inside ffmpeg.exe."

exit 0
'@

Write-Step "Checking MSYS2 packages without database update"
Invoke-MSYS2Script -ScriptPath $CheckScript

Write-Step "Downloading FFmpeg source archive"

if (-not (Test-Path $FfmpegArc)) {
    Invoke-WebRequest -Uri $FfmpegUrl -OutFile $FfmpegArc
}

Write-Step "Extracting FFmpeg source archive inside MSYS2"
Invoke-MSYS2Script -ScriptPath $ExtractScript

if (-not (Test-Path $FfmpegDir)) {
    throw "FFmpeg source directory was not found after extract: $FfmpegDir"
}

Write-Step "Building minimal one-file FFmpeg"
Invoke-MSYS2Script -ScriptPath $BuildScript

Write-Step "Copying and verifying standalone ffmpeg.exe"
Invoke-MSYS2Script -ScriptPath $VerifyScript

Write-Step "Verifying resulting binary"

$ResultExe = Join-Path $OutDir "ffmpeg.exe"

if (-not (Test-Path $ResultExe)) {
    throw "Result ffmpeg.exe was not found: $ResultExe"
}

Push-Location $OutDir
try {
    Write-Host ""
    Write-Host "Version:"
    & $ResultExe -hide_banner -version | Select-Object -First 5

    $Demuxers = (& $ResultExe -hide_banner -demuxers 2>&1) -join "`n"
    $Muxers = (& $ResultExe -hide_banner -muxers 2>&1) -join "`n"
    $Decoders = (& $ResultExe -hide_banner -decoders 2>&1) -join "`n"
    $Encoders = (& $ResultExe -hide_banner -encoders 2>&1) -join "`n"
    $Protocols = (& $ResultExe -hide_banner -protocols 2>&1) -join "`n"
    $Filters = (& $ResultExe -hide_banner -filters 2>&1) -join "`n"

    foreach ($Name in @("png_pipe", "jpeg_pipe", "webp_pipe", "tiff_pipe", "bmp_pipe")) {
        if ($Demuxers -notmatch "(?m)^\s*D\s+$([regex]::Escape($Name))\s") {
            throw "Required demuxer is missing: $Name"
        }
    }

    foreach ($Name in @("image2", "webp")) {
        if ($Muxers -notmatch "(?m)^\s*E\s+$([regex]::Escape($Name))\s") {
            throw "Required muxer is missing: $Name"
        }
    }

    foreach ($Name in @("png", "mjpeg", "webp", "tiff", "bmp")) {
        if ($Decoders -notmatch "(?m)^\s*V\S*\s+$([regex]::Escape($Name))\s") {
            throw "Required decoder is missing: $Name"
        }
    }

    foreach ($Name in @("png", "bmp", "tiff", "libwebp")) {
        if ($Encoders -notmatch "(?m)^\s*V\S*\s+$([regex]::Escape($Name))\s") {
            throw "Required encoder is missing: $Name"
        }
    }

    if ($Encoders -match "(?m)^\s*V\S*\s+libwebp_anim\s") {
        throw "Unexpected libwebp_anim encoder is enabled; the minimal build should not require libwebpmux."
    }

    if ($Protocols -notmatch "(?m)^\s*file\s*$") {
        throw "Required protocol is missing: file"
    }

    foreach ($Name in @("crop", "scale", "format", "eq", "unsharp")) {
        if ($Filters -notmatch "(?m)^\s*[TSC.]{2,3}\s+$([regex]::Escape($Name))\s") {
            throw "Required filter is missing: $Name"
        }
    }

    Write-Host ""
    Write-Host "Demuxers:"
    $Demuxers -split "`n" | Select-String "png_pipe|jpeg_pipe|webp_pipe|tiff_pipe|bmp_pipe"

    Write-Host ""
    Write-Host "Muxers:"
    $Muxers -split "`n" | Select-String "image2|webp"

    Write-Host ""
    Write-Host "Decoders:"
    $Decoders -split "`n" | Select-String "\b(png|mjpeg|webp|tiff|bmp)\b"

    Write-Host ""
    Write-Host "Encoders:"
    $Encoders -split "`n" | Select-String "\b(png|bmp|tiff|libwebp)\b"

    Write-Host ""
    Write-Host "Protocols:"
    $Protocols -split "`n" | Select-String "^\s*file\s*$"

    Write-Host ""
    Write-Host "Filters:"
    $Filters -split "`n" | Select-String "\b(crop|scale|format|eq|unsharp)\b"

    Write-Step "Running Sympllate image-pipeline smoke tests"

    $SmokeDir = Join-Path $WorkDir "sympllate-smoke"
    if (Test-Path -LiteralPath $SmokeDir) {
        Remove-Item -LiteralPath $SmokeDir -Recurse -Force
    }
    New-Item -ItemType Directory -Path $SmokeDir | Out-Null

    try {
        $InputPng = Join-Path $SmokeDir "input.png"
        $InputJpeg = Join-Path $SmokeDir "input.jpg"

        [IO.File]::WriteAllBytes($InputPng, [Convert]::FromBase64String("iVBORw0KGgoAAAANSUhEUgAAABAAAAAQCAIAAACQkWg2AAAACXBIWXMAAAABAAAAAQBPJcTWAAAAI0lEQVR4nGP8//8/AymAhSTVDKMaiAMsRKqDg1ENxACSQwkAg10DO7J+0fsAAAAASUVORK5CYII="))
        [IO.File]::WriteAllBytes($InputJpeg, [Convert]::FromBase64String("/9j/4AAQSkZJRgABAgAAAQABAAD//gAQTGF2YzYyLjI4LjEwMQD/2wBDAAgEBAQEBAUFBQUFBQYGBgYGBgYGBgYGBgYHBwcICAgHBwcGBgcHCAgICAkJCQgICAgJCQoKCgwMCwsODg4RERT/xABLAAEBAAAAAAAAAAAAAAAAAAAABwEBAAAAAAAAAAAAAAAAAAAAABABAAAAAAAAAAAAAAAAAAAAABEBAAAAAAAAAAAAAAAAAAAAAP/AABEIABAAEAMBIgACEQADEQD/2gAMAwEAAhEDEQA/AL+AD//Z"))

        foreach ($Input in @($InputPng, $InputJpeg)) {
            $Name = [IO.Path]::GetFileNameWithoutExtension($Input)
            $OcrOutput = Join-Path $SmokeDir "$Name-ocr.png"
            $OcrArguments = @(
                "-hide_banner", "-loglevel", "error", "-nostdin", "-y", "-noautorotate",
                "-i", $Input,
                "-vf", "crop=16:16:0:0,scale=32:32:flags=lanczos,format=gray,eq=contrast=1.08,unsharp=5:5:0.45:3:3:0.0",
                "-frames:v", "1", $OcrOutput
            )
            Invoke-FFmpegSmokeCommand -Description "OCR preprocess $([IO.Path]::GetExtension($Input)) -> PNG" -Arguments $OcrArguments -OutputPath $OcrOutput
        }

        foreach ($Extension in @("webp", "tiff", "bmp")) {
            $Encoded = Join-Path $SmokeDir "encoded.$Extension"
            $Normalized = Join-Path $SmokeDir "normalized-$Extension.png"

            $EncodeArguments = @(
                "-hide_banner", "-loglevel", "error", "-nostdin", "-y",
                "-i", $InputPng, "-frames:v", "1", $Encoded
            )
            Invoke-FFmpegSmokeCommand -Description "PNG -> $Extension" -Arguments $EncodeArguments -OutputPath $Encoded

            $NormalizeArguments = @(
                "-hide_banner", "-loglevel", "error", "-nostdin", "-noautorotate",
                "-i", $Encoded, "-frames:v", "1", $Normalized
            )
            Invoke-FFmpegSmokeCommand -Description "$Extension -> normalized PNG" -Arguments $NormalizeArguments -OutputPath $Normalized
        }
    }
    finally {
        if (Test-Path -LiteralPath $SmokeDir) {
            Remove-Item -LiteralPath $SmokeDir -Recurse -Force
        }
    }

    Write-Host "Sympllate smoke tests passed."
}
finally {
    Pop-Location
}

Write-Host ""
Write-Host "Done."
Write-Host ("Result standalone ffmpeg.exe: " + $ResultExe)
Write-Host "Embedded Windows DPI awareness: PerMonitorV2"
Write-Host "No separate ffmpeg.exe.manifest file is required next to ffmpeg.exe."
Write-Host "No FFmpeg/zlib/libwebp/MSYS2 DLL files should be required next to it."
Write-Host "Windows system DLL imports are expected."
Write-Host ""
Write-Host "Usage:"
Write-Host "  .\build-onefile-ffmpeg.ps1"
