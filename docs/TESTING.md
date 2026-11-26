# Как проверить проект

Команды выполняются из корня распакованного проекта. Нужны Go 1.25+ и Python 3.12+. Готовый Windows executable позволяет запускать Go-сервис без установки Go. API-ключ модели в архив не включен.

## Go-модули без внешнего AI

```powershell
go test ./... -count=1
go vet ./...
```

Проверяются импорт, валидация, повторы, роли, CSRF, обратная связь, автоматическая регистрация и очередь. Доступ к LLM и реальные секреты не нужны: HTTP-агент в тестах подменен локальным тестовым сервером.

Отдельные модули:

```powershell
go test ./internal/pulse -run 'TestHistorical|TestExcel|TestValidation' -v
go test ./internal/pulse -run 'TestAutomatic|TestNewEnrollment|TestTally' -v
go test ./internal/pulse -run 'TestRBAC|TestFeedback' -v
go test ./internal/pulse -run 'TestAgent' -v
```

## Python-модули без расходов на модель

```powershell
python -m venv .venv
.\.venv\Scripts\python.exe -m pip install -r agent/requirements.txt pytest
.\.venv\Scripts\python.exe -m pytest agent -q
```

Проверяются запрос к OpenAI-совместимому endpoint, JSON, evidence, идемпотентность, ошибки upstream и таймаут. Сетевые ответы подменены; настоящий API-ключ не используется. Ожидается 30 passed.

## Настоящий Qwen через FastAPI

Терминал 1, Python:

```powershell
$env:AGENT_TOKEN = 'local-agent-shared-secret-2026'
$env:AGENT_REFERENCE_MODE = '0'
$env:LLM_BASE_URL = 'https://llm-proxy.spirit.culab.ru'
$env:LLM_MODEL = 'qwen/qwen3.6-flash'
$env:LLM_API_KEY = [System.Net.NetworkCredential]::new('', (Read-Host 'API key' -AsSecureString)).Password
.\.venv\Scripts\python.exe -m uvicorn app:app --app-dir agent --host 127.0.0.1 --port 8000
```

Терминал 2, проверка модели:

```powershell
$env:AGENT_TOKEN = 'local-agent-shared-secret-2026'
.\.venv\Scripts\python.exe scripts/test_live_agent.py
```

Скрипт отправляет 12 исторических ответов СТ-004 в FastAPI, проверяет ссылки на оценки/комментарии и повторяет запрос с тем же ID. Ожидается `PASS: real model response, exact evidence, idempotent replay`, резюме и число сигналов. Выполняется реальный запрос к провайдеру, возможен расход квоты. Повтор в пределах кеша Python не вызывает модель повторно.

API-ключ провайдера и AGENT_TOKEN — разные секреты. Первый нужен только Python, второй должен совпадать в Go, Python и тестовом скрипте.

## Полная цепочка Go → FastAPI → Qwen → HR

Оставьте Python из предыдущего раздела запущенным. В терминале 2:

```powershell
$env:AGENT_TOKEN = 'local-agent-shared-secret-2026'
$env:AGENT_URL = 'http://127.0.0.1:8000'
.\bin\mgts-pulse-windows-amd64.exe -command demo -db data/ai-test.db
.\bin\mgts-pulse-windows-amd64.exe -db data/ai-test.db
```

На новой базе при старте Go автоматически поставит пять историй в очередь. Откройте http://127.0.0.1:8080, войдите `hr` / `Mgts-demo-2026!`, перейдите в «Интеграции». Дождитесь `done`, затем откройте СТ-004 → «AI-анализ истории». Должны появиться резюме, сигналы с точными источниками и рекомендации. Числовой светофор остается отдельным методом.

Пять историй означают до пяти реальных запросов к модели, дополнительные запросы возможны при повторе после ошибки. Не запускайте тест на рабочей базе. Новая `ai-test.db` также исключает сохраненные выводы прежнего reference-режима. Если задание в `failed`, нажмите «Повторить» в интеграциях. Для `done` той же ревизии повторный вызов не пересчитывает результат.

## Автоматическое добавление стажера из форм

Остановите только Go (Ctrl+C), Python можно оставить запущенным. В терминале Go задайте локальные параметры форм. Чтобы этот тест не тратил квоту модели, отключите AGENT_URL:

```powershell
$env:AGENT_URL = ''
$env:TALLY_SIGNING_SECRET = 'local-tally-signing-secret-2026'
$env:TALLY_INTERN_FORM_ID = 'local-intern-form'
$env:TALLY_MENTOR_FORM_ID = 'local-mentor-form'
.\bin\mgts-pulse-windows-amd64.exe -command demo -db data/forms-test.db
.\bin\mgts-pulse-windows-amd64.exe -db data/forms-test.db
```

В другом терминале:

```powershell
$env:TALLY_SIGNING_SECRET = 'local-tally-signing-secret-2026'
$env:TALLY_INTERN_FORM_ID = 'local-intern-form'
$env:TALLY_MENTOR_FORM_ID = 'local-mentor-form'
.\.venv\Scripts\python.exe scripts/demo_webhook.py
```

Ожидается PASS, СТ-DEMO автоматически появляется в HR, наставник Н-DEMO привязан, сохранено две анкеты. Их повторная доставка дает duplicate. Ручная карточка не создается ни в скрипте, ни в HR. Обновление сводки — по кнопке или автоматически через 15 секунд. Реальные Tally-формы используют тот же защищенный endpoint; их отдельная настройка описана в INTEGRATIONS.md.

## Ошибки

| Результат | Что проверить |
|---|---|
| FastAPI 401 | Совпадает ли AGENT_TOKEN |
| FastAPI 503 | Заданы ли AGENT_TOKEN длиной ≥24 и LLM_API_KEY |
| FastAPI 502, upstream HTTP 401/403 | Ключ/доступ к модели у провайдера |
| FastAPI 502, upstream HTTP 429 | Квоту и ограничения провайдера |
| FastAPI 502, invalid analysis | Модель нарушила JSON/ссылки; результат не сохранен, очередь повторит запрос |
| FastAPI 504 | Сеть или длительный ответ модели |
| Webhook 401 | Подпись Tally или Bearer-секрет |
| Webhook 409 | Измененный повтор либо уже занятый месяц/источник |
| Webhook 422 | Оценки/период, несовпадение существующего подразделения или наставника |

Тесты проверяют технический контракт и происхождение доказательств. Правильность смысловых выводов модели требует отдельной оценки HR на размеченной выборке.


## Скрытый сигнал и план 1:1

```powershell
go test ./internal/pulse -run 'TestHidden|TestHRFeatures|TestFallback|TestExplicitAnalysis' -v
.\.venv\Scripts\python.exe -m pytest agent/test_insights.py -v
```

Проверяются шесть контрольных случаев из killer-features, дословность цитат, рост/отрицания, пропуски, смена хеша текста, сохранение разметки, безопасное сообщение наставнику и отсутствие плана в API наставника/стажера. Общие AI-резюме и исходная обратная связь продолжают работать.

В работающем сервисе войдите как HR, откройте СТ-004, месяц 3, нажмите «Запросить анализ». Дождитесь done в интеграциях и обновите карточку. Должны появиться скрытые сигналы нагрузки с оценкой и комментарием, а также план 1:1. При переходе к месяцу 5 сигналы месяца 3 не должны влиять на статус. Для СТ-005 в месяце 1 ожидается weekly_1on1. Войдите mentor5/intern5: приватного плана HR нет, исходные оценки и опубликованная обратная связь доступны.

Чтобы проверить шаблон без модели, остановите Python, откройте историю СТ-005 за месяц 1 на базе с исходными ответами. План вопросов доступен из числовых правил Go, а новые скрытые сигналы без проверенной разметки не генерируются. Ранее проверенный анализ той же ревизии может оставаться сохраненным.


## Запуск Docker с ngrok

Заполните NGROK_AUTHTOKEN в корневом .env, затем выполните docker compose up --build -d. Отдельный demo и профиль ai не нужны. Дождитесь готовности app и откройте локальную панель http://127.0.0.1:4040: там должен появиться публичный HTTPS-адрес. Проверьте вход по публичной ссылке и сохранение ролей. Панель 4040 доступна только локально; Python не публикуется.

Проверка статуса: docker compose ps. Если туннель не появился — docker compose logs ngrok. Остановка без удаления данных: docker compose down. Проверка повторного старта: docker compose up -d; число исходных анкет и пароли должны сохраниться. Конфигурационная проверка без запуска: docker compose config --quiet.
