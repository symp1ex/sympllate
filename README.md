# Sympllate

## Описание

Sympllate — локальный переводчик для Windows x64. Переводит текст, выделение в сторонних приложениях и изображения. Для модели используется установленный Ollama либо локальный комплект из GGUF-модели и `llama.cpp`.

Собираются две редакции:

- **Lite** — работает с Ollama, установленной в системе. В поставку не входят модель и runtime.
- **Portable** — запускает поставленные GGUF-модель и `llama-server.exe`; Ollama на целевом компьютере не требуется.

Технически можно собрать Lite-сборку и превратить её в Portable положив вручную все необходимые компоненты

### Быстрый перевод

В главном окне можно перевести текст вручную. Для работы с выделением в другой программе используются две комбинации из `config.json`:

- `hotkeys.showTranslation` — показывает перевод выделенного текста в отдельном окне;
- `hotkeys.replaceSelection` — заменяет выделение переводом.

Текст для быстрого перевода берётся из буфера обмена. Исходный язык определяется автоматически, целевой выбирается в окне перевода.

### Перевод изображений

Одиночное изображение PNG или JPEG можно вставить в окно через `Ctrl+V` или перетащить мышью. Для Lite изображение передаётся модели Ollama. Для Portable требуется минимум локальный OCR-комплект + onnx runtime.

Пакетный перевод открывается кнопкой с иконкой изображений рядом с **Copy**. Можно выбрать несколько PNG, JPEG, WebP, TIFF или BMP-файлов либо каталог. Результаты создаются рядом с `translator.exe`:

```text
_output/
└── YYYY-MM-DD_HH-MM-SS/
    ├── images/          # копии оригиналов
    ├── translated/      # изображения с переведённым текстом
    ├── ocr/             # результат распознавания
    ├── translations/    # результат перевода
    ├── job.json
    └── errors.json      # создаётся при ошибках отдельных файлов
```

Оригинальные файлы не изменяются. Для обработки изображений используется FFmpeg, для OCR используется PaddleOCR, для замены текста на сложном фоне используется LaMa; если их нет в поставке, пакетный перевод изображений не поддерживается. Подробности есть ниже.

## Установка и настройка

### Требования

- Windows x64.
- Microsoft Edge WebView2 Runtime. Обычно уже установлен в Windows; при необходимости доступен на [странице WebView2](https://developer.microsoft.com/microsoft-edge/webview2/).
- Для Lite: [Ollama для Windows](https://ollama.com/download/windows) и загруженная модель.
- Для Portable: драйвер, соответствующий выбранной сборке `llama.cpp`, если используется GPU runtime.

### Lite

После установки Ollama скачайте TranslateGemma:

```powershell
ollama pull translategemma:latest
```

Модель `translategemma:latest` занимает около 3,3 ГБ. Другие варианты модели и их размер указаны на [странице TranslateGemma в Ollama](https://ollama.com/library/translategemma).

Запустите `translator.exe`. При первом запуске рядом с ним создаётся `config.json`.

### Portable

Portable нельзя переносить частями: рядом с `translator.exe` должны быть папки `models` и `runtime`. Минимальная структура поставки:

```text
Sympllate/
├── translator.exe
├── config.json
├── models/
│   └── <model>.gguf
└── runtime/
    └── llama/
        ├── llama-server.exe
        └── DLL и остальные файлы из того же runtime
```

Запустите `translator.exe`. Если в `config.json` выбран `provider.active = "auto"`, приложение использует Portable при наличии полного комплекта файлов, иначе пытается подключиться к Ollama.

### Пакетный перевод изображений: OCR и очистка фона

Для Portable-перевода изображений рядом с программой должны находиться: ffmpeg, OCR-модели, ONNX Runtime и, для очистки фона, LaMa:

```text
Sympllate/
├── bin/
│   ├── ffmpeg/
│   │   └── ffmpeg.exe
│   ├── OCR/
│   │   ├── det.onnx
│   │   ├── det.yml
│   │   └── *_rec.onnx + *_rec.yml
│   └── inpaint/
│       └── inpainting_lama.onnx
└── runtime/
    └── onnx/
        └── onnxruntime.dll
```

Модель LaMa и `onnxruntime.dll` добавляются скриптом сборки, параметры приведены в разделе **Сборка**. FFmpeg и OCR-файлы (PP-OCRv5) должны быть подготовлены заранее в составе Portable-поставки.

Для обработки изображений подойдёт почти любая публичная сборка FFmpeg.

<details>
<summary><strong>Минимально требуемая конфигурация FFmpeg</strong></summary>

```bash
ffmpeg version 8.1.2 Copyright (c) 2000-2026 the FFmpeg developers
built with gcc 16.1.0 (Rev5, Built by MSYS2 project)
configuration: --target-os=mingw32 --arch=x86_64 --enable-static --disable-shared --pkg-config-flags=--static --extra-ldflags='-static -static-libgcc -static-libstdc++' --extra-ldexeflags='-static -static-libgcc -static-libstdc++ /c/Users/sympl/_project/sympllate/_scripts/_ffmpeg_build/manifest/ffmpeg-manifest.o' --disable-autodetect --disable-debug --disable-doc --disable-network --enable-small --disable-runtime-cpudetect --disable-everything --disable-ffplay --disable-ffprobe --disable-avdevice --disable-swresample --enable-ffmpeg --enable-gpl --enable-zlib --enable-libwebp --enable-swscale --enable-protocol=file --enable-demuxer=image_png_pipe --enable-demuxer=image_jpeg_pipe --enable-demuxer=image_webp_pipe --enable-demuxer=image_tiff_pipe --enable-demuxer=image_bmp_pipe --enable-muxer=image2 --enable-muxer=webp --enable-decoder=png --enable-decoder=mjpeg --enable-decoder=webp --enable-decoder=tiff --enable-decoder=bmp --enable-encoder=png --enable-encoder=bmp --enable-encoder=tiff --enable-encoder=libwebp --enable-filter=crop --enable-filter=scale --enable-filter=format --enable-filter=eq --enable-filter=unsharp
libavutil      60. 26.102 / 60. 26.102
libavcodec     62. 28.102 / 62. 28.102
libavformat    62. 12.102 / 62. 12.102
libavfilter    11. 14.102 / 11. 14.102
libswscale      9.  5.102 /  9.  5.102

```
</details>

<details>


<summary><strong>Где взять PaddleOCR</strong></summary>

### Модели из OCR-бандла

| Файлы | Модель |
|---|---|
| `det.onnx` / `det.yml` | `PP-OCRv5_mobile_det` |
| `cjk_rec.onnx` / `cjk_rec.yml` | `PP-OCRv5_mobile_rec` |
| `latin_rec.onnx` / `latin_rec.yml` | `latin_PP-OCRv5_mobile_rec` |
| `eslav_rec.onnx` / `eslav_rec.yml` | `eslav_PP-OCRv5_mobile_rec` |
| `arabic_rec.onnx` / `arabic_rec.yml` | `arabic_PP-OCRv5_mobile_rec` |
| `korean_rec.onnx` / `korean_rec.yml` | `korean_PP-OCRv5_mobile_rec` |

### Источники

- **PaddleOCR:** [PaddlePaddle/PaddleOCR](https://github.com/PaddlePaddle/PaddleOCR)
- **Официальные модели:** [PaddlePaddle на Hugging Face](https://huggingface.co/PaddlePaddle)

### Страницы моделей

- [`PP-OCRv5_mobile_det`](https://huggingface.co/PaddlePaddle/PP-OCRv5_mobile_det_onnx)
- [`PP-OCRv5_mobile_rec`](https://huggingface.co/PaddlePaddle/PP-OCRv5_mobile_rec_onnx)
- [`latin_PP-OCRv5_mobile_rec`](https://huggingface.co/PaddlePaddle/latin_PP-OCRv5_mobile_rec)
- [`eslav_PP-OCRv5_mobile_rec`](https://huggingface.co/PaddlePaddle/eslav_PP-OCRv5_mobile_rec)
- [`arabic_PP-OCRv5_mobile_rec`](https://huggingface.co/PaddlePaddle/arabic_PP-OCRv5_mobile_rec_onnx)
- [`korean_PP-OCRv5_mobile_rec`](https://huggingface.co/PaddlePaddle/korean_PP-OCRv5_mobile_rec_onnx)

</details>

## Конфигурация

### config.json

Файл `config.json` находится рядом с `translator.exe`. Настройки можно изменить в интерфейсе приложения либо вручную при закрытой программе.

<details>
<summary>Пример <b>config.json</b></summary>

```json
{
  "provider": {
    "active": "auto",
    "list": [
      "auto",
      "ollama",
      "local"
    ]
  },
  "localModel": {
    "modelFile": "",
    "profile": "translategemma-raw",
    "startupTimeoutSeconds": 180,
    "fitTargetMiB": 1024
  },
  "ollama": {
    "baseUrl": "http://127.0.0.1:11434",
    "model": "translategemma:latest",
    "timeoutSeconds": 120,
    "keepAlive": "10m",
    "numCtx": 2048,
    "numPredict": 1024,
    "temperature": 0
  },
  "hotkeys": {
    "showTranslation": "Ctrl+Alt+T",
    "replaceSelection": "Ctrl+Alt+R"
  },
  "defaultLanguagePair": {
    "first": {
      "active": "ru",
      "list": ["ru", "en", "de", "fr", "es", "uk", "pl", "it", "pt", "tr", "zh", "ja", "ko", "ar"]
    },
    "second": {
      "active": "en",
      "list": ["ru", "en", "de", "fr", "es", "uk", "pl", "it", "pt", "tr", "zh", "ja", "ko", "ar"]
    }
  },
  "fallbackTargetLanguage": {
    "active": "ru",
    "list": ["ru", "en", "de", "fr", "es", "uk", "pl", "it", "pt", "tr", "zh", "ja", "ko", "ar"]
  },
  "ui": {
    "mainWindowWidth": 900,
    "mainWindowHeight": 620,
    "popupWidth": 520,
    "popupHeight": 360,
    "alwaysOnTopPopup": true,
    "hideIntoTrayOnStartup": false
  },
  "limits": {
    "maxInputCharacters": 131072,
    "clipboardWaitMilliseconds": 800
  },
  "updater": {
    "enabled": true
  },
  "logs": {
    "log_level": {
      "active": "warning",
      "list": ["debug", "info", "warning", "error"]
    },
    "store_days": 2
  },
  "imageBatch": {
    "minimumFontSize": 7,
    "maximumFontSize": 48,
    "lineSpacing": 1.15,
    "jpegQuality": 92
  }
}
```

Параметры provider:

- `active`: выбранный способ работы с моделью: `auto`, `ollama` или `local`.
- `list`: варианты, доступные в окне настроек.

Параметры localModel:

- `modelFile`: путь к GGUF-модели. Относительный путь считается от каталога `translator.exe`. Если значение пустое, в папке `models` должна быть ровно одна `.gguf`-модель.
- `profile`: профиль модели. По умолчанию используется `translategemma-raw`.
- `startupTimeoutSeconds`: максимальное время запуска локальной модели.
- `fitTargetMiB`: объём памяти, на который ориентируется запуск модели.

Параметры Ollama:

- `baseUrl`: адрес API Ollama. Для Ollama на этом же компьютере — `http://127.0.0.1:11434`.
- `model`: имя модели из `ollama list`.
- `timeoutSeconds`: максимальное время ожидания ответа.
- `keepAlive`: время, в течение которого Ollama сохраняет модель в памяти.
- `numCtx`, `numPredict`, `temperature`: параметры запроса к модели.

Параметры горячих клавиш:

- `showTranslation`: показать перевод выделения.
- `replaceSelection`: заменить выделение переводом.

Параметры языков:

- `defaultLanguagePair.first.active`: исходный язык в главном окне.
- `defaultLanguagePair.second.active`: целевой язык в главном окне.
- `fallbackTargetLanguage.active`: язык быстрого перевода по умолчанию.
- `list`: список языков в выпадающем списке.

Прочие параметры:

- `ui`: размеры главного окна и окна быстрого перевода; `hideIntoTrayOnStartup` оставляет приложение в tray при запуске.
- `limits.maxInputCharacters`: максимальный размер текста в символах.
- `limits.clipboardWaitMilliseconds`: ожидание появления выделенного текста в буфере обмена.
- `updater.enabled`: включение проверки обновлений.
- `logs.log_level.active`: уровень логирования; `logs.store_days`: срок хранения логов в днях.
- `imageBatch`: минимальный и максимальный размер шрифта, межстрочный интервал и качество JPEG при пакетном переводе.
</details>

## Сборка

### Требования для сборки

- [Node.js LTS](https://nodejs.org/en/download).
- [Go 1.24 или новее](https://go.dev/dl/).
- MinGW-w64 GCC с UCRT. Если установлен MSYS2, скрипт использует `C:\msys64\ucrt64\bin\gcc.exe`, когда `gcc` отсутствует в `PATH`.

Скрипт собирает интерфейс и приложение, результаты помещает в `dist`. Внешние модели и runtime он не скачивает.

### Lite

```powershell
.\build.ps1
```

Создаётся `dist\lite`. Для запуска этой редакции на целевом компьютере нужны Ollama и модель, указанные в разделе **Lite**.

### Portable

Для Portable нужен существующий GGUF-файл и распакованный Windows x64 runtime `llama.cpp` с `llama-server.exe`. Runtime можно скачать в [релизах llama.cpp](https://github.com/ggml-org/llama.cpp/releases).

```powershell
.\build.ps1 `
  -Edition Portable `
  -ModelPath C:\models\translator.gguf `
  -LlamaRuntimePath C:\runtime\llama
```

Создаётся `dist\portable`. Модель копируется в `models`, runtime — в `runtime\llama`; `config.json` настраивается на локальную модель.

### Обе редакции

```powershell
.\build.ps1 `
  -Edition All `
  -ModelPath C:\models\translator.gguf `
  -LlamaRuntimePath C:\runtime\llama
```

Создаются `dist\lite` и `dist\portable`.

### Локальная очистка изображений

Нужно скачать [`inpainting_lama_2025jan.onnx` из OpenCV Zoo](https://github.com/opencv/opencv_zoo/raw/refs/heads/main/models/inpainting_lama/inpainting_lama_2025jan.onnx), переименовать его в `inpainting_lama.onnx` и скачать [ONNX Runtime 1.26.0 для Windows x64](https://github.com/microsoft/onnxruntime/releases/download/v1.26.0/onnxruntime-win-x64-1.26.0.zip). Из архива ONNX Runtime используется файл `lib\onnxruntime.dll`.

```powershell
.\build.ps1 `
  -Edition Portable `
  -ModelPath C:\models\translator.gguf `
  -LlamaRuntimePath C:\runtime\llama `
  -InpaintModelPath C:\models\inpainting_lama.onnx `
  -OnnxRuntimePath C:\runtime\onnxruntime.dll
```

`-InpaintModelPath` и `-OnnxRuntimePath` передаются только вместе. Если их не указывать, каталог `bin\inpaint` не создаётся.

## Примечания

- В Lite название в `ollama.model` должно совпадать с моделью, показанной командой `ollama list`.
- В Portable `localModel.modelFile` должен указывать на существующую `.gguf`-модель. При пустом значении в `models` допускается только один GGUF-файл.
- Ошибка подключения к Ollama обычно означает, что Ollama не запущена, модель не загружена или изменён `ollama.baseUrl`.
- Ошибка запуска Portable обычно означает, что перенесён только `translator.exe` либо в `runtime\llama` отсутствуют DLL из того же архива, что и `llama-server.exe`.
