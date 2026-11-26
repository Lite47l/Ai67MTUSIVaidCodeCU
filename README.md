# МГТС Пульс стажировки

Go-монолит для сбора и просмотра обратной связи за шесть месяцев стажировки. Встроенный фронтенд, SQLite, роли HR / наставник / стажер, импорт Excel, webhook Tally, журнал действий HR и Python-агент с моделью Qwen через HTTP-прокси.

## Запуск всего сервиса с Qwen через Docker

В архив уже добавлен `.env` с вашим токеном Qwen, URL модели и общим секретом Go ↔ Python. Код менять и токен вводить повторно не нужно. Архив содержит действующий ключ: не публикуйте его и не загружайте `.env` в Git.

Распакуйте архив, запустите Docker Desktop и дождитесь запуска движка. Откройте PowerShell **в папке проекта, где находится compose.yaml**, и выполните:

```powershell
docker compose build
docker compose run --rm app -command demo
docker compose --profile ai up -d
```

Откройте http://127.0.0.1:8080. Подняты Go, фронтенд, SQLite и Python-агент с настоящим Qwen. Истории пяти стажеров анализируются автоматически; статус — HR → «Интеграции», результат — карточка стажера → «AI-анализ истории».

Проверка и остановка:

```powershell
docker compose --profile ai ps
docker compose logs --tail 50 app agent
docker compose --profile ai down
```

Повторный запуск: `docker compose --profile ai up -d`. Данные сохраняются в Docker volume. Если Docker сообщает, что не может подключиться к Engine, сначала запустите Docker Desktop.

## Запуск на Windows без Docker

Требуется установленный Python 3.12+. Go устанавливать не нужно: используется `bin/mgts-pulse-windows-amd64.exe`. Оба терминала откройте в корне распакованного проекта. Активация venv не требуется; отдельная команда `pip` не используется.

**Терминал 1 — установка зависимостей и Python-агент.** Скопируйте весь блок:

```powershell
$ErrorActionPreference = 'Stop'
$PythonCommand = if (Get-Command py -ErrorAction SilentlyContinue) { 'py' } else { 'python' }
& $PythonCommand -m venv .venv
if ($LASTEXITCODE -ne 0) { throw 'Не удалось создать окружение. Установите Python 3.12+ и откройте PowerShell заново.' }
& .\.venv\Scripts\python.exe -m ensurepip --upgrade
& .\.venv\Scripts\python.exe -m pip install -r agent/requirements.txt
if ($LASTEXITCODE -ne 0) { throw 'Не удалось установить зависимости Python.' }
Get-Content .env | ForEach-Object {
    $line = $_.Trim()
    if ($line -and -not $line.StartsWith('#')) {
        $name, $value = $line -split '=', 2
        Set-Item -Path ("Env:" + $name) -Value $value
    }
}
& .\.venv\Scripts\python.exe -m uvicorn app:app --app-dir agent --host 127.0.0.1 --port 8000
```

Оставьте терминал работающим. Если не найдены ни `py`, ни `python`, установите Python 3.12+ с python.org с опцией **Add python.exe to PATH** и повторите блок в новом PowerShell.

**Терминал 2 — Go и фронтенд.** Скопируйте весь блок:

```powershell
$ErrorActionPreference = 'Stop'
Get-Content .env | ForEach-Object {
    $line = $_.Trim()
    if ($line -and -not $line.StartsWith('#')) {
        $name, $value = $line -split '=', 2
        Set-Item -Path ("Env:" + $name) -Value $value
    }
}
$env:AGENT_URL = 'http://127.0.0.1:8000'
& .\bin\mgts-pulse-windows-amd64.exe -command demo
if ($LASTEXITCODE -ne 0) { throw 'Ошибка подготовки базы.' }
& .\bin\mgts-pulse-windows-amd64.exe
```

Откройте http://127.0.0.1:8080. Для завершения нажмите Ctrl+C в обоих терминалах. Повторный запуск: в первом терминале достаточно импорта `.env` и команды uvicorn; во втором повторите его блок. `demo` не дублирует историю.

`start-demo.cmd` по-прежнему запускает только Go и интерфейс. Для работы AI используйте один из двух способов выше.

## Учетные записи

| Доступ | Логин | Пароль |
|---|---|---|
| HR | `hr` | `Mgts-demo-2026!` |
| Наставники Н-001…Н-005 | `mentor1`…`mentor5` | `Mgts-demo-2026!` |
| Стажеры СТ-001…СТ-005 | `intern1`…`intern5` | `Mgts-demo-2026!` |

Демонстрационные пароли предназначены для локального запуска. Исходные XLSX/DOCX в архив не включены. `data/history.json` содержит 60 нормализованных ответов из предоставленных таблиц; файл исключен из Git.

## Запуск из исходников

Требуется Go 1.25+; Node.js и npm не нужны.

```sh
go mod download
go run ./cmd/server -command demo
go run ./cmd/server
```

`demo` создает схему, демонстрационные учетные записи на пустой базе и импортирует историю. Повторный запуск не добавляет ответы и не сбрасывает существующие пароли. База — `data/pulse.db`; завершение — Ctrl+C.

Кабинеты: `/hr`, `/mentor`, `/intern`. Роль определяется серверной сессией, а не адресом страницы. Наставник видит назначенных ему стажеров, стажер — только свою историю, оценки, комментарии и опубликованные сообщения наставника.

## Импорт исходных Excel

На новой базе сначала выполните `migrate`. Имена столбцов соответствуют анкетам, порядок столбцов произвольный.

```sh
go run ./cmd/server -command migrate
go run ./cmd/server -command import -intern "C:/path/ответы стажеров.xlsx" -mentor "C:/path/оценки наставников.xlsx"
```

Импорт атомарный. Первый импорт — 60 ответов, повторный — 0 новых. Пропуск сохраняется как `null`. Оценки вне 1–5, неверный период, подразделение или наставник отклоняются. Сочетание программы, стажера, месяца и источника уникально. Одинаковый `eventId` с теми же ответами идемпотентен; измененный повтор и второй ответ в тот же слот — `409`.

Новый стажер появляется автоматически после первого валидного ответа из защищенного webhook/Tally. Если первым ответил стажер, наставник пока не назначается; его ID заполнится после анкеты наставника. Уже известные подразделение и наставник не перезаписываются. Ручное добавление в HR удалено. Исторический импорт и обычный кабинет не создают неизвестных участников.

## Создание учетных записей

После `migrate` создайте HR и учетные записи участников. Пароль передается через окружение, минимум 12 символов.

```powershell
$env:USER_PASSWORD = 'replace-with-a-long-password'
go run ./cmd/server -command user -username admin -role hr
go run ./cmd/server -command user -username trainee6 -role intern -subject 'СТ-006'
go run ./cmd/server -command user -username mentor6 -role mentor -subject 'Н-006'
Remove-Item Env:USER_PASSWORD
```

Для этих двух учетных записей сначала должны поступить внешние анкеты СТ-006 и Н-006. Автоматизация создает реестр участников, а не пароли: создание учетных записей остается задачей администратора/SSO. `ADDR`, `DB_PATH`, секреты и URL задаются переменными окружения; обычный `go run` сам `.env` не читает. Параметр `-db` переопределяет путь базы для любой команды.

## Python-агент

Агент настроен на `https://llm-proxy.spirit.culab.ru`, модель `qwen/qwen3.6-flash`. Его запуск вместе с Go описан выше. `LLM_API_KEY` — ключ Qwen, `AGENT_TOKEN` — отдельный общий секрет Go ↔ FastAPI. Они уже заполнены в `.env`. Для прямого запуска переменные загружаются PowerShell-блоками выше; Compose читает `.env` автоматически.

Go вызывает `POST {AGENT_URL}/v1/analyze`; контракт — [docs/AGENT.md](docs/AGENT.md), спецификация — [docs/openapi.json](docs/openapi.json). JSON модели и ссылки на доказательства проверяются до сохранения. Ответ анкеты сохраняется до обращения к AI. Очередь переживает перезапуск, делает до четырех попыток; ошибочные задания можно повторить в HR → «Интеграции».

`AGENT_REFERENCE_MODE=0` включает настоящий AI. Режим `1` проверяет только транспорт без модели.

## Внешние формы

Поддержаны Tally с HMAC-подписью и нормализованный Bearer-webhook. Настройка двух форм, поля и локальный тест: [docs/INTEGRATIONS.md](docs/INTEGRATIONS.md).

Адаптер реализован и проверен на подписанных запросах. Живые формы не созданы: для этого нужны аккаунт Tally и публичный HTTPS endpoint. Статус «настроено» в UI означает наличие конфигурации, а не подтвержденную доставку из Tally.

## Проверка

```sh
go test ./... -count=1
go vet ./...
go build ./cmd/server
.\.venv\Scripts\python.exe -m pip install pytest
.\.venv\Scripts\python.exe -m pytest agent
```

Проверяются импорт и пропуски, 2/5 и 4/5 по нагрузке СТ-004 в месяцах 4 и 5, конфликты и параллельные повторы, доступ по ролям, CSRF, сессии, публикация обратной связи, новый участник, подпись Tally, очередь и evidence агента. Пошаговая проверка модулей, реального AI и автоматической регистрации: [docs/TESTING.md](docs/TESTING.md). Фактические результаты: [docs/VALIDATION.md](docs/VALIDATION.md).

## Структура и ограничения

`cmd/server` — запуск и CLI; `internal/pulse` — API, SQLite, импорт, правила и очередь; `web` — встроенный HTML/CSS/JS; `agent` — FastAPI-контракт; `docs` — API, интеграции, методика и сценарий защиты.

Светофор — прозрачные правила: оценка ≤2 критична, 3 требует внимания, падение ≥2, повтор низких оценок и расхождение ≥2 по двум близким парам показателей требуют проверки. Это эвристики, не валидированная AI-модель. Средние разных анкет не вычитаются. Анализ текста и рекомендации Qwen показываются отдельно от числового светофора и требуют проверки HR.

Прототип рассчитан на одну программу и один процесс Go. Для внедрения нужны корпоративный SSO, HTTPS с `COOKIE_SECURE=true`, согласованный сервис форм/модели, правила доступа и хранения данных, резервное копирование SQLite и проверка качества сигналов на размеченных примерах. План пилота и измеримые показатели: [docs/METHOD.md](docs/METHOD.md).
