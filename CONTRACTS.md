# Контракт MVP v1

Все даты и время API — ISO 8601; `period` — целое 1..6 для исторической программы. Для тестовых новых ответов к уже завершенной истории используйте отдельный `program_id` или явно согласованный новый цикл: месяц 7 не допускается без изменения модели. `source` = `intern` | `mentor`. Обязательная связка: `program_id`, `intern_id`, `period`, `source`. У наставника также `mentor_id`.

## Прием нормализованного ответа

`POST /api/v1/responses` — внутренний защищенный endpoint адаптера; внешний webhook реализуется отдельным маршрутом с проверкой секрета. Тело:

```json
{
  "source_event_id": "form-provider:response-123",
  "program_id": "demo-2026",
  "source": "intern",
  "intern_id": "СТ-004",
  "mentor_id": null,
  "department": "Административный блок",
  "period": 4,
  "scores": {"workload_comfort": 2},
  "comments": {"period_difficulties": "Нагрузка была слишком высокой"},
  "submitted_at": "2026-11-26T12:00:00+03:00"
}
```

Все 7/6 показателей обязательны для завершенной анкеты; частичный ответ можно сохранить как `incomplete`, но нельзя считать отсутствующие оценки нулем. Повтор того же `source_event_id` с тем же содержимым возвращает исходный ID и `duplicate: true`; другое содержимое с тем же ID возвращает 409 и логируется.

Поля стажера: `task_clarity`, `mentor_support`, `workload_comfort`, `task_interest`, `professional_growth`, `team_interaction`, `overall_satisfaction`. Комментарии: `period_difficulties`, `work_barriers`.

Поля наставника: `task_understanding`, `work_quality`, `independence`, `deadline_organization`, `initiative`, `mentor_team_interaction`. Комментарии: `period_comment`, `attention_situations`.

## Чтение

- `GET /api/v1/overview?program_id=...&period=6`: число участников, ожидаемые/полученные ответы по источникам, распределение статусов, очередь по объяснимому приоритету. `period` задает срез истории, по умолчанию последний доступный.
- `GET /api/v1/interns/{intern_id}?program_id=...&period=6`: метаданные, оба источника за 1..6, комментарии, сигналы и evidence; значения отсутствующих ответов — `null`.
- `GET /api/v1/integrations`: провайдер, статус реальной настройки, время и итог последней синхронизации, ошибка. Не возвращать `connected: true` без успешной доставки.

## Сигнал

```json
{
  "id": "stable-key",
  "intern_id": "СТ-004",
  "period": 4,
  "kind": "low_score",
  "status": "critical",
  "title": "Некомфортная нагрузка",
  "explanation": "Оценка комфортности нагрузки 2 из 5",
  "evidence": [{"source": "intern", "period": 4, "field": "workload_comfort", "value": 2, "quote": null}],
  "next_step": "Уточнить распределение задач и сроки с участниками"
}
```

Статус — `normal`, `attention`, `critical`. Порядок приоритета: критичный активный сигнал, затем несколько активных сигналов/повтор, затем одиночный сигнал, затем норма. Отсутствие анкеты показывать отдельно как `missing_feedback`, а не автоматически как критичный статус человека.

Пары близких, но не тождественных показателей: `task_clarity` ↔ `task_understanding`; `team_interaction` ↔ `mentor_team_interaction` (вторая пара только контекстуальная, так как наставник оценивает поведение стажера). `mentor_support`, `workload_comfort`, `overall_satisfaction` не имеют прямых пар в анкете наставника. Разница сама по себе — повод уточнить контекст, не доказательство неправоты стороны.

Координатор фиксирует любые изменения схемы до их реализации в двух модулях. Эталонные JSON-fixtures должны использовать эти имена полей.

