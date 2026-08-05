# Sympllate

Sympllate собирается в двух вариантах для Windows x64.

- **Lite** использует Ollama, уже установленную и настроенную пользователем. Локальные бинарники и модель в поставку не входят.
- **Portable** запускает поставленный `llama-server.exe` и GGUF-модель. Приложение не устанавливает и не скачивает Ollama, llama.cpp, модель или драйверы во время работы.

Оба варианта используют Microsoft Edge WebView2 Runtime, установленный в Windows. Portable также требует рабочий драйвер GPU; выбор GPU backend определяется содержимым поставленного runtime llama.cpp и доступными драйверами.

## Конфигурация provider

Активный provider задаётся в `provider.active`; поддерживаются `"auto"`, `"ollama"` и `"local"`. Массив `provider.list` содержит варианты для выпадающего списка окна настроек. В режиме `auto` полный локальный layout выбирает local provider, иначе используется Ollama. В режиме `local` отсутствие runtime или модели является ошибкой.

Если `localModel.modelFile` пуст, в каталоге `models` должна находиться ровно одна модель с расширением `.gguf`. Относительный `modelFile` всегда разрешается относительно каталога `translator.exe`.

Portable layout:

```text
Sympllate/
├── translator.exe
├── config.json
├── models/
│   └── <model>.gguf
└── runtime/
    └── llama/
        ├── llama-server.exe
        └── DLL и остальные файлы одной версии llama.cpp
```

## Сборка

Lite (также является безопасным вариантом по умолчанию):

```powershell
.\build.ps1
# или
.\build.ps1 -Edition Lite
```

Portable из заранее подготовленных локальных ресурсов:

```powershell
.\build.ps1 `
  -Edition Portable `
  -ModelPath C:\models\translator.gguf `
  -LlamaRuntimePath C:\runtime\llama
```

Обе директории за один вызов создаются через `-Edition All` с теми же Portable-параметрами. Результаты находятся в `dist\lite` и `dist\portable`. Скрипт не создаёт ZIP или установщик, не включает WebView2 Runtime и не загружает внешние ресурсы.
