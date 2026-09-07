# Sympllate

## Описание

Sympllate — локальный переводчик для Windows x64. Переводит текст, выделение в сторонних приложениях и изображения. Для модели используется установленный Ollama либо локальный комплект из GGUF-модели и `llama.cpp`.

Собираются две редакции:

- **Lite** — работает с Ollama, установленной в системе. В поставку не входят модель и runtime.
- **Portable** — запускает поставленные GGUF-модель и `llama-server.exe`; Ollama на целевом компьютере не требуется.

### Быстрый перевод

В главном окне можно перевести текст вручную. Для работы с выделением в другой программе используются две комбинации из `config.json`:

- `hotkeys.showTranslation` — показывает перевод выделенного текста в отдельном окне;
- `hotkeys.replaceSelection` — заменяет выделение переводом.

Текст для быстрого перевода берётся из буфера обмена. Исходный язык определяется автоматически, целевой выбирается в окне перевода.

### Перевод изображений

Одиночное изображение PNG или JPEG можно вставить в окно через `Ctrl+V` или перетащить мышью. Для Lite изображение передаётся модели Ollama. Для Portable требуется локальный OCR-комплект.

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

Оригинальные файлы не изменяются. Для замены текста на сложном фоне используется LaMa; если её нет в поставке, пакетный перевод изображений не поддерживается.

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

### OCR и очистка изображений

Для Portable-перевода изображений рядом с программой должны находиться OCR-модели, ONNX Runtime и, для очистки фона, LaMa:

```text
Sympllate/
├── bin/
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

Модель LaMa и `onnxruntime.dll` добавляются скриптом сборки, параметры приведены в разделе **Сборка**. OCR-файлы должны быть подготовлены в составе Portable-поставки.

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
    "profile": "generic",
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
    "alwaysOnTopPopup": true
  },
  "limits": {
    "maxInputCharacters": 12000,
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
- `profile`: профиль модели. Для обычной работы используется `generic`.
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

- `ui`: размеры главного окна и окна быстрого перевода.
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
