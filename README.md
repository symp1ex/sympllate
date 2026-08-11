# Sympllate

Sympllate собирается в двух вариантах для Windows x64.

- **Lite** использует Ollama, уже установленную и настроенную пользователем. GGUF-модель и llama.cpp в поставку не входят; локальные LaMa/ONNX Runtime для image cleanup входят в обе редакции.
- **Portable** запускает поставленный `llama-server.exe` и GGUF-модель. Приложение не устанавливает и не скачивает Ollama, llama.cpp, модель или драйверы во время работы.

Оба варианта используют Microsoft Edge WebView2 Runtime, установленный в Windows, и локальный CPU-only ONNX Runtime 1.26.0 для удаления текста с изображений. Portable также требует рабочий драйвер GPU; выбор GPU backend определяется только содержимым поставленного runtime llama.cpp и доступными драйверами. LaMa не подключает CUDA или DirectML и не занимает GPU переводчика.

## Конфигурация provider

Активный provider задаётся в `provider.active`; поддерживаются `"auto"`, `"ollama"` и `"local"`. Массив `provider.list` содержит варианты для выпадающего списка окна настроек. В режиме `auto` полный локальный layout выбирает local provider, иначе используется Ollama. В режиме `local` отсутствие runtime или модели является ошибкой.

Если `localModel.modelFile` пуст, в каталоге `models` должна находиться ровно одна модель с расширением `.gguf`. Относительный `modelFile` всегда разрешается относительно каталога `translator.exe`.

Portable layout:

```text
Sympllate/
├── translator.exe
├── config.json
├── bin/
│   └── inpaint/
│       ├── onnxruntime.dll
│       └── inpainting_lama.onnx
├── models/
│   └── <model>.gguf
└── runtime/
    └── llama/
        ├── llama-server.exe
        └── DLL и остальные файлы одной версии llama.cpp
```

## Сборка

Для сборки нужен MinGW-w64 GCC с UCRT (скрипт автоматически использует `C:\msys64\ucrt64\bin\gcc.exe`, если `gcc` отсутствует в `PATH`). LaMa-модель и CPU DLL не скачиваются и передаются явно для обоих вариантов:

```powershell
.\build.ps1 `
  -Edition Lite `
  -InpaintModelPath C:\models\inpainting_lama.onnx `
  -OnnxRuntimePath C:\runtime\onnxruntime.dll
```

Portable из заранее подготовленных локальных ресурсов:

```powershell
.\build.ps1 `
  -Edition Portable `
  -ModelPath C:\models\translator.gguf `
  -LlamaRuntimePath C:\runtime\llama `
  -InpaintModelPath C:\models\inpainting_lama.onnx `
  -OnnxRuntimePath C:\runtime\onnxruntime.dll
```

Обе директории за один вызов создаются через `-Edition All` с теми же Portable-параметрами. Результаты находятся в `dist\lite` и `dist\portable`. Скрипт не создаёт ZIP или установщик, не включает WebView2 Runtime и не загружает внешние ресурсы.

## Перевод изображений

Одиночное изображение PNG или JPEG можно вставить через `Ctrl+V` либо передать Drag-and-Drop. Для local provider перед Tesseract создаётся временный OCR PNG через `bin\ffmpeg.exe`: изображение масштабируется Lanczos с сохранением aspect ratio, переводится в luma/grayscale и получает умеренное повышение контраста и резкости без агрессивного threshold. После этого распознанный текст переводится той же TranslateGemma. Ollama image provider этот pipeline не использует: исходное изображение по-прежнему передаётся непосредственно vision-модели Ollama, без Tesseract.

Local и batch OCR используют один adaptive pipeline. Основной full-image TSV pass выполняется с Tesseract PSM 3. Небольшие изображения увеличиваются до 2–4 раз в пределах hard limits. Если целую крупную страницу нельзя увеличить до полезного масштаба либо основной pass содержит мелкие или низкоуверенные слова, добавляется ограниченное число перекрывающихся tiles; каждый tile масштабируется отдельно и распознаётся с PSM 11. Tile-слова дополняют основной layout, overlap-кандидаты объединяются по геометрии, нормализованному тексту и confidence, а координаты возвращаются в систему оригинального изображения. FFmpeg и Tesseract запускаются только последовательно под одним OCR timeout; временные файлы удаляются при успехе, ошибке и отмене.

Существующий OCR layout разрешается только относительно каталога EXE:

```text
Sympllate/
├── translator.exe
└── bin/
    ├── ffmpeg.exe
    ├── tesseract.exe
    ├── необходимые DLL
    └── tessdata/
        └── *.traineddata
```

Для совместимости с layout первой фазы также распознаётся вложенный каталог `bin/tesseract/` с тем же содержимым. Если он уже работает, его переносить не требуется. `PATH` и автоматическое скачивание бинарников или языков не используются.

## Пакетный перевод изображений

Кнопка с иконкой файла справа от **Copy translation** открывает отдельное окно **Batch image translation**. В нём можно выбрать несколько PNG, JPEG, WebP, TIFF или BMP-файлов либо один каталог. Закрытие прячет окно по аналогии с quick translate popup; активное задание продолжает выполняться, а повторное открытие показывает его текущий статус. Каталог просматривается нерекурсивно; скрытые, временные, symbolic-link/junction entries и неподдерживаемые расширения пропускаются. Файлы сортируются natural sort (`page-2.png` перед `page-10.png`). Абсолютные пути выбранных файлов хранятся только на стороне Go в краткоживущем selection record и не передаются в WebView.

Задание выполняет файлы последовательно: проверяет и копирует оригинал без перекодирования, запускает описанный выше FFmpeg preprocessing и adaptive Tesseract TSV OCR, восстанавливает детерминированные строки/абзацы и переводит их через TranslateGemma. После layout renderer анализирует кольцо пикселей вокруг OCR-области. Однородный фон очищается точной дешёвой заливкой; градиенты, рамки, UI и текстуры направляются в shared CPU-session LaMa. Для LaMa строится маска пикселей текста по foreground/background contrast с dilation в один пиксель. Близкие маски объединяются, получают 48 px контекста и aspect-ratio preserving letterbox до 512×512; целая страница не уменьшается.

Размер шрифта подбирается ограниченным бинарным поиском. Перенос сохраняет явные переводы строк, выполняется по словам и безопасно разбивает слишком длинные URL/идентификаторы по rune boundary. Если текст не помещается, text box ограниченно расширяется вниз, затем по горизонтали, без пересечения соседних OCR-блоков. Блок, который нельзя безопасно восстановить, не очищается; файл получает статус `partial`. Минимальный размер шрифта остаётся предупреждением. Если для сложного фона нельзя безопасно построить text mask или LaMa inference завершается ошибкой, файл получает явную ошибку `clean_background`; прямоугольная заливка как скрытый fallback не используется.

Параллельные модельные запросы и несколько FFmpeg-процессов не выполняются. Задание можно отменить во время подготовки, layout, cleanup, render и encoding; уже записанные результаты сохраняются, временные файлы удаляются, а незавершённый итоговый файл не публикуется.

Результаты создаются рядом с EXE, а не в текущем рабочем каталоге:

```text
_output/
└── YYYY-MM-DD_HH-MM-SS[_N]/
    ├── images/          # неизменённые копии оригиналов
    ├── translated/      # итоговые изображения с заменённым текстом
    ├── ocr/             # *.ocr.json
    ├── translations/    # *.translation.json
    ├── debug/           # опциональные *.ocr.png, *.cleaned.png, *.layout.png, *.render.json
    ├── job.json
    └── errors.json
```

Каждый OCR JSON имеет `schemaVersion: 1`, размеры и media type изображения, все raw OCR words (включая confidence и флаг `accepted`) и сгруппированные строки/абзацы с bounding boxes. Stable IDs имеют вид `p1-b2-par3`, `p1-b2-par3-l4`, `p1-b2-par3-l4-w5`. Confidence строки и абзаца — обычное среднее confidence входящих принятых слов.

Translation JSON также имеет `schemaVersion: 1`. Для каждого абзаца он сохраняет тот же ID, исходный и переведённый текст, confidence и координаты. Ответ модели принимается только как один JSON object с точным множеством ID; допустимо снять один Markdown JSON fence. При нарушении формата выполняется один repair retry. Большие страницы делятся на последовательные chunks с 20% резервом character budget для ответа; слишком большой абзац сначала делится по OCR-строкам и после перевода собирается обратно. Использованные стабильные дочерние `*-part-N` units сохраняются в `parts` родительского translation block.

Если принятого OCR-текста нет, модель, cleanup и renderer не вызываются; файл в `translated` является byte-for-byte копией оригинала, а translation status равен `no_text`. Ошибка отдельного файла записывается в `errors.json` и не останавливает остальные файлы; системная недоступность OCR, модели или обязательного шрифта завершает задание. После `completed` и `completed_with_errors` каталог автоматически открывается в Explorer. Ошибка Explorer не меняет успешный статус.

PNG и JPEG для renderer декодируются и кодируются pure Go один раз; отдельный временный FFmpeg PNG используется только OCR pipeline. JPEG quality задаётся в `imageBatch.jpegQuality`. Для WebP, TIFF и BMP `bin\ffmpeg.exe` также нормализует вход в один временный PNG и один раз кодирует готовый PNG обратно в исходное расширение. Команды запускаются без shell, с timeout/cancellation, ограниченным stderr и проверкой результата. Оригинал в `images` никогда не заменяется.

Renderer использует Go Regular из `golang.org/x/image/font/gofont/goregular` (BSD-3-Clause). Сборка детерминированно создаёт `bin\fonts\regular.ttf` рядом с EXE и кладёт рядом текст лицензии; системные Windows fonts не используются. Один TTF парсится один раз, faces кэшируются по размеру. Проверены латиница, кириллица, цифры и символы, присутствующие в шрифте. Сложный shaping арабского письма, вертикальный текст, блоки 90°/270° и произвольный rotation текущим pure-Go renderer не поддерживаются.

Debug mode создаёт OCR overlay, изображение после hybrid cleanup (`*.cleaned.png`), итоговый layout overlay с source/cleanup/text boxes (`*.layout.png`) и фактический `RenderDocument` (`*.render.json`). Ошибка debug-файла не отменяет основной результат.

Параметры `imageBatch` в `config.json`: `minimumFontSize`, `maximumFontSize`, `lineSpacing` и `jpegQuality`. Порог однородности, sampling, mask dilation, crop padding и tensor preprocessing являются тестируемыми деталями реализации и не вынесены в пользовательский config.

ONNX environment, LaMa session и фиксированные tensors создаются один раз при запуске приложения, переиспользуются всеми изображениями и освобождаются после отмены/завершения batch jobs при shutdown. Одновременно выполняется не более одного inference. Alpha исходника сохраняется вне маски и предсказуемо сохраняется внутри восстановленных пикселей.
