# Ollama Переводчик для Windows

Компактное Windows-приложение для ручного перевода, быстрого перевода выделенного текста и замены выделения через локальный или удалённый Ollama API. Интерфейс написан на React, backend — на Go; собранный frontend встроен в `translator.exe`.

## Возможности

- ручной перевод с выбором языка и `Ctrl+Enter`;
- глобальный `Ctrl+Alt+T`: скопировать выделение, определить направление и показать переиспользуемый popup;
- глобальный `Ctrl+Alt+R`: перевести выделение и вставить результат вместо него;
- автоматическое направление для настраиваемой основной пары языков;
- локальное приблизительное определение письменности без отдельной ML-модели;
- понятные ошибки Ollama, конфигурации, hotkey и clipboard;
- максимум одна ручная операция и по одной операции каждого hotkey-типа одновременно.

## Системные требования

- Windows 10/11 x64;
- Microsoft Edge WebView2 Runtime (обычно уже установлен; при необходимости используйте [официальный установщик Microsoft](https://developer.microsoft.com/microsoft-edge/webview2/));
- Ollama на этом или доступном по сети компьютере;
- для сборки: Go 1.24+, Node.js LTS и npm.

Приложение не запускает Ollama самостоятельно.

## Подготовка Ollama

Установите и запустите Ollama, затем загрузите небольшую модель:

```powershell
ollama pull gemma3:1b
```

Для отдельного имени модели скопируйте `Modelfile.example` в `Modelfile` и выполните:

```powershell
ollama create translator-gemma -f Modelfile
```

Если используется исходное имя `gemma3:1b`, укажите его в `config.json` вместо `translator-gemma`.

## Конфигурация

При первом запуске приложение создаёт `config.json` рядом с `.exe`. Полный образец находится в `config.example.json`.

Основные параметры:

- `ollama.baseUrl` — базовый URL без обязательного `/api/generate`;
- `ollama.model`, `timeoutSeconds`, `keepAlive`, `numCtx`, `numPredict`, `temperature` — параметры запроса;
- `hotkeys.showTranslation` и `hotkeys.replaceSelection` — глобальные комбинации;
- `defaultLanguagePair` — два языка, направление между которыми переключается автоматически;
- `fallbackTargetLanguage` — целевой язык для неизвестного или третьего языка;
- `limits.maxInputCharacters` и `clipboardWaitMilliseconds` — ограничения ввода и ожидание `Ctrl+C`.

Поддерживаемый синтаксис hotkey: `Ctrl`, `Alt`, `Shift`, `Win` и клавиши `A-Z`, `0-9`, `F1-F12`, `Space`, `Enter`, `Tab`, `Escape`. Например: `Ctrl+Alt+T`, `Ctrl+Shift+Space`, `Alt+F8`.

После изменения `config.json` перезапустите приложение.

## Сборка

Полная сборка в каталог `dist`:

```powershell
.\build.ps1
```

Ручная сборка:

```powershell
cd frontend
npm install
npm run build
cd ..
go test ./...
New-Item -ItemType Directory -Force dist
go build -ldflags="-H windowsgui" -o dist\translator.exe .\cmd\translator
Copy-Item config.example.json dist\config.example.json
```

Запускайте `dist\translator.exe`. При ошибках смотрите `dist\translator.log`.

## Разработка

Frontend можно проверять отдельно командой `npm run typecheck`; для полноценной работы bindings нужен запущенный WebView2 backend. Production-сборка Vite помещается в `internal/webassets/dist`, после чего Go встраивает HTML, CSS и JavaScript в `.exe`.

```powershell
cd frontend
npm install
npm run typecheck
npm run build
cd ..
go test ./...
```

## Использование

1. Запустите Ollama и `translator.exe`.
2. Для обычного перевода введите текст, выберите языки и нажмите «Перевести».
3. Для popup выделите текст в другом приложении и нажмите `Ctrl+Alt+T`.
4. Для замены выделите текст в редактируемом поле и нажмите `Ctrl+Alt+R`.
5. В popup можно изменить языки, повторить перевод, скопировать результат или закрыть окно клавишей `Escape`.

## Диагностика

- **Ollama недоступна** — проверьте, что `ollama serve` работает и `baseUrl` доступен. Для удалённого сервера проверьте firewall и настройки прослушивания Ollama.
- **model not found** — выполните `ollama pull ...` или исправьте `ollama.model`.
- **Hotkey уже занят** — измените комбинацию в `config.json` и перезапустите приложение.
- **Не удалось получить выделенный текст** — убедитесь, что поле поддерживает обычный `Ctrl+C` и текст действительно выделен.
- **Перевод не вставился** — поле может блокировать синтетический `Ctrl+V`; скопируйте результат из popup вручную.
- **WebView2 не создаётся** — установите WebView2 Runtime по ссылке выше.
- **Повреждён config.json** — исправьте JSON по `config.example.json`; приложение намеренно не затирает повреждённый файл.

## Известные ограничения MVP

- Получение выделения основано на эмуляции `Ctrl+C`, замена — на `Ctrl+V`; некоторые приложения и защищённые поля блокируют эти действия.
- Восстанавливается только прежнее текстовое содержимое clipboard. Изображения, файлы и сложные форматы после временного копирования могут восстановиться не полностью.
- Автоопределение близких языков на латинице приблизительное; при отсутствии характерных признаков выбирается английский.
- Качество и скорость зависят от модели Ollama и оборудования. Большие тексты на GTX 750 могут переводиться медленно.
- Вставку через `Ctrl+V` нельзя надёжно подтвердить без Windows UI Automation, которая намеренно не используется в MVP.
- Позиционирование popup учитывает рабочую область монитора, но на нестандартном DPI scaling возможны отклонения.
- Popup рассчитан на один пользовательский сеанс Windows и постоянно живёт в процессе приложения; tray icon пока отсутствует.
