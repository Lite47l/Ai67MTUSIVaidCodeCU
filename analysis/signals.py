"""Deterministic analysis of normalized internship survey responses."""

import json
from collections import defaultdict
from collections.abc import Iterable, Mapping
from hashlib import sha256
from typing import Literal, TypedDict

from ._text import issue_quotes

Source = Literal["intern", "mentor"]
Status = Literal["attention", "critical"]


class Response(TypedDict):
    program_id: str
    intern_id: str
    period: int
    source: Source
    scores: Mapping[str, int | None]
    comments: Mapping[str, str | None]


class Evidence(TypedDict):
    source: Source
    period: int
    field: str
    value: int | None
    quote: str | None


class Signal(TypedDict):
    id: str
    intern_id: str
    period: int
    kind: str
    status: Status
    title: str
    explanation: str
    evidence: list[Evidence]
    next_step: str


_SCORES: dict[Source, dict[str, str]] = {
    "intern": {
        "task_clarity": "Понятность задач",
        "mentor_support": "Поддержка и обратная связь наставника",
        "workload_comfort": "Комфортность нагрузки",
        "task_interest": "Интерес к задачам",
        "professional_growth": "Профессиональное развитие",
        "team_interaction": "Взаимодействие с командой",
        "overall_satisfaction": "Общая удовлетворенность стажировкой",
    },
    "mentor": {
        "task_understanding": "Понимание задач и ожиданий",
        "work_quality": "Качество выполнения задач",
        "independence": "Самостоятельность",
        "deadline_organization": "Соблюдение сроков и организация работы",
        "initiative": "Вовлеченность и инициативность",
        "mentor_team_interaction": "Взаимодействие с наставником и командой",
    },
}
_COMMENTS: dict[Source, tuple[str, str]] = {
    "intern": ("period_difficulties", "work_barriers"),
    "mentor": ("period_comment", "attention_situations"),
}
_TOPICS = {
    "task_clarity": ("Понятность задач", "Сверить формулировки задач, приоритеты и критерии готовности."),
    "mentor_feedback": ("Обратная связь наставника", "Обсудить формат и частоту обратной связи."),
    "workload": ("Нагрузка", "Уточнить распределение задач и сроки с участниками."),
    "team_interaction": ("Взаимодействие с командой", "Уточнить у обеих сторон, где возникают сложности в общении."),
}
_SCORE_TOPICS = {
    "task_clarity": "task_clarity",
    "task_understanding": "task_clarity",
    "mentor_support": "mentor_feedback",
    "workload_comfort": "workload",
    "team_interaction": "team_interaction",
}
_DEFAULT_NEXT_STEP = "Обсудить с участниками причины этой оценки."
_PAIRS = (
    ("task_clarity", "task_understanding", None),
    ("team_interaction", "mentor_team_interaction", "team_interaction"),
)


def _signal(
    scope: tuple[str, str, int],
    kind: str,
    subject: str,
    status: Status,
    title: str,
    explanation: str,
    evidence: list[Evidence],
    next_step: str,
) -> Signal:
    program_id, intern_id, period = scope
    identity = json.dumps([program_id, intern_id, period, kind, subject], ensure_ascii=False)
    return {
        "id": sha256(identity.encode("utf-8")).hexdigest()[:24],
        "intern_id": intern_id,
        "period": period,
        "kind": kind,
        "status": status,
        "title": title,
        "explanation": explanation,
        "evidence": evidence,
        "next_step": next_step,
    }


def _score_evidence(source: Source, period: int, field: str, value: int) -> Evidence:
    return {"source": source, "period": period, "field": field, "value": value, "quote": None}


def _comments_by_topic(responses: Mapping[Source, Response]) -> dict[str, list[Evidence]]:
    topics: dict[str, list[Evidence]] = defaultdict(list)
    for source, fields in _COMMENTS.items():
        response = responses.get(source)
        if response is None:
            continue
        for field in fields:
            comment = response["comments"].get(field)
            if not comment:
                continue
            for topic, quote in issue_quotes(comment):
                topics[topic].append(
                    {"source": source, "period": response["period"], "field": field, "value": None, "quote": quote}
                )
    return dict(topics)


def _score_signals(
    scope: tuple[str, str, int],
    current: Mapping[Source, Response],
    previous: Mapping[Source, Response],
    text_topics: Mapping[str, list[Evidence]],
) -> tuple[list[Signal], dict[str, list[Evidence]]]:
    period = scope[2]
    signals: list[Signal] = []
    repeated_scores: dict[str, list[Evidence]] = defaultdict(list)

    for source, fields in _SCORES.items():
        if source not in current:
            continue
        current_scores = current[source]["scores"]
        previous_scores = previous[source]["scores"] if source in previous else {}
        for field, label in fields.items():
            value = current_scores.get(field)
            if value is None:
                continue

            subject = f"{source}:{field}"
            topic = _SCORE_TOPICS.get(field, subject)
            _, next_step = _TOPICS.get(topic, (label, _DEFAULT_NEXT_STEP))
            evidence = _score_evidence(source, period, field, value)
            prior_value = previous_scores.get(field)
            prior = (
                _score_evidence(source, period - 1, field, prior_value)
                if prior_value is not None
                else None
            )
            supporting_text = text_topics.get(topic, [])
            repeats = prior_value is not None and prior_value <= 3 and value <= 3
            decline = prior_value is not None and prior_value > value

            if value <= 2:
                signals.append(_signal(
                    scope, "low_score", subject, "critical", f"Низкая оценка: {label.lower()}",
                    f"Оценка {value}/5 попадает в критичный диапазон 1–2.", [evidence], next_step,
                ))
            elif value == 3 and (decline or repeats or supporting_text):
                reasons = []
                supporting_evidence = [evidence]
                if prior is not None and (decline or repeats):
                    reasons.append("оценка снизилась" if decline else "низкие оценки два месяца подряд")
                    supporting_evidence.append(prior)
                if supporting_text:
                    reasons.append("в комментариях есть проблема по этой теме")
                    supporting_evidence.extend(supporting_text)
                signals.append(_signal(
                    scope, "borderline_score", subject, "attention",
                    f"Пограничная оценка: {label.lower()}",
                    f"Оценка 3/5 требует внимания: {', '.join(reasons)}.", supporting_evidence, next_step,
                ))

            if prior_value is not None and prior is not None and prior_value - value >= 2:
                signals.append(_signal(
                    scope, "score_drop", subject, "attention", f"Снижение: {label.lower()}",
                    f"Оценка снизилась с {prior_value}/5 до {value}/5 за месяц (на 2 балла или больше).",
                    [prior, evidence], next_step,
                ))
            if repeats and prior is not None:
                repeated_scores[topic].extend((prior, evidence))

    return signals, dict(repeated_scores)


def _issue_signals(
    scope: tuple[str, str, int],
    current: Mapping[str, list[Evidence]],
    previous: Mapping[str, list[Evidence]],
    repeated_scores: Mapping[str, list[Evidence]],
) -> Iterable[Signal]:
    for topic in dict.fromkeys((*current, *repeated_scores)):
        text = current.get(topic, [])
        previous_text = previous.get(topic, []) if text else []
        scores = repeated_scores.get(topic, [])
        if topic in _TOPICS:
            label, next_step = _TOPICS[topic]
        else:
            first_score = scores[0]
            label = _SCORES[first_score["source"]][first_score["field"]]
            next_step = _DEFAULT_NEXT_STEP

        if previous_text or scores:
            reasons = []
            if scores:
                reasons.append("оценка 3/5 или ниже")
            if previous_text:
                reasons.append("комментарии об одной проблеме")
            yield _signal(
                scope, "repeated_issue", topic, "attention", f"Повторяется: {label.lower()}",
                f"Два месяца подряд: {' и '.join(reasons)}.",
                [*scores, *previous_text, *text], next_step,
            )
        elif text:
            yield _signal(
                scope, "text_issue", topic, "attention", f"В комментариях: {label.lower()}",
                "В комментарии за выбранный месяц описана проблема по этой теме.", text, next_step,
            )


def _disagreements(
    scope: tuple[str, str, int],
    current: Mapping[Source, Response],
    topics: Mapping[str, list[Evidence]],
) -> Iterable[Signal]:
    if "intern" not in current or "mentor" not in current:
        return
    for intern_field, mentor_field, contextual_topic in _PAIRS:
        intern_score = current["intern"]["scores"].get(intern_field)
        mentor_score = current["mentor"]["scores"].get(mentor_field)
        if intern_score is None or mentor_score is None or abs(intern_score - mentor_score) < 2:
            continue
        text = topics.get(contextual_topic, []) if contextual_topic else []
        if contextual_topic and not text:
            continue
        period = scope[2]
        label = _SCORES["intern"][intern_field]
        evidence = [
            _score_evidence("intern", period, intern_field, intern_score),
            _score_evidence("mentor", period, mentor_field, mentor_score),
            *text,
        ]
        explanation = (
            f"Стажер поставил {intern_score}/5, наставник — {mentor_score}/5. "
            f"Разница между оценками — {abs(intern_score - mentor_score)} (порог сигнала — 2 балла). "
            "Вопросы описывают разные стороны ситуации, поэтому стоит уточнить контекст."
        )
        if text:
            explanation += " В этом месяце есть комментарий о сложностях взаимодействия."
        yield _signal(
            scope, "score_disagreement", intern_field, "attention",
            f"Разница в обратной связи: {label.lower()}", explanation, evidence,
            "Уточнить у обеих сторон, как они понимают ситуацию.",
        )


def analyze_signals(
    responses: Iterable[Response],
    *,
    program_id: str,
    intern_id: str,
    period: int | None = None,
) -> list[Signal]:
    """Analyze canonical responses for one intern and program, optionally for one month."""
    if period is not None and (type(period) is not int or not 1 <= period <= 6):
        raise ValueError("period must be an integer from 1 to 6")

    months: dict[int, dict[Source, Response]] = defaultdict(dict)
    for response in responses:
        if (response["program_id"], response["intern_id"]) != (program_id, intern_id):
            continue
        month, source = response["period"], response["source"]
        if type(month) is not int or not 1 <= month <= 6:
            raise ValueError("response period must be an integer from 1 to 6")
        if period is not None and month > period:
            continue
        if source not in _SCORES:
            raise ValueError(f"unknown feedback source: {source!r}")
        if source in months[month]:
            raise ValueError(f"multiple canonical responses for {source}, month {month}")
        for field in _SCORES[source]:
            value = response["scores"].get(field)
            if value is not None and (type(value) is not int or not 1 <= value <= 5):
                raise ValueError(f"{source}.{field}, month {month}: expected an integer from 1 to 5")
        months[month][source] = response

    topics = {month: _comments_by_topic(records) for month, records in months.items()}
    signals: list[Signal] = []
    selected_months = sorted(months) if period is None else [period]
    for month in selected_months:
        scope = (program_id, intern_id, month)
        current = months.get(month, {})
        text = topics.get(month, {})
        scores, repeats = _score_signals(scope, current, months.get(month - 1, {}), text)
        signals.extend(scores)
        signals.extend(_issue_signals(scope, text, topics.get(month - 1, {}), repeats))
        signals.extend(_disagreements(scope, current, text))

    return sorted(signals, key=lambda signal: (signal["period"], signal["status"] != "critical"))
