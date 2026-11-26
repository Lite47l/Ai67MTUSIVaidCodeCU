import hashlib
import json
from pathlib import Path

import pytest
from fastapi.testclient import TestClient

import app as api
import insights
from test_app import payload, token


def history_rows():
    rows = json.loads((Path(__file__).parents[1] / "data/history.json").read_text(encoding="utf-8"))
    return [api.Response(**{k: v for k, v in row.items() if k != "raw"}) for row in rows]


CASES = [
    ("СТ-004", 3, "intern", "difficulties", "workload", "Стало больше параллельных задач."),
    ("СТ-004", 3, "mentor", "attention", "workload", "Стоит продолжать следить за нагрузкой."),
    ("СТ-001", 1, "intern", "barriers", "feedback", "Нужно быстрее получать обратную связь по промежуточным результатам."),
    ("СТ-001", 2, "intern", "barriers", "feedback", "Иногда не хватает короткой сверки с наставником по новым задачам."),
    ("СТ-002", 4, "intern", "barriers", "workload", "Иногда нагрузка выше комфортной."),
    ("СТ-004", 4, "mentor", "feedback", "workload", "часть сроков пришлось перенести"),
]


def test_six_historical_hidden_signals_and_exact_evidence():
    history = history_rows()
    labels = []
    for ident, month, source, field, theme, quote in CASES:
        row = next(r for r in history if (r.internId, r.month, r.source) == (ident, month, source))
        items = insights.valid_items([{"theme": theme, "polarity": "negative", "quote": quote}], row.comments[field])
        assert items
        labels.append((row, field, items))
    result = insights.hidden_signals(labels)
    assert len(result) == 6
    for month, action, signal in result:
        assert signal["kind"] == "text_score_mismatch" and signal["severity"] == "attention"
        score, comment = signal["evidence"]
        assert score["eventId"] == comment["eventId"] and int(score["value"]) >= 4
        row = next(r for r in history if r.eventId == score["eventId"])
        assert row.month == month and row.comments[comment["field"]] == comment["value"]


@pytest.mark.parametrize("text,theme,quote", [
    ("Нагрузка выросла.", "workload", "Вымышленная цитата"),
    ("Хотелось бы больше ответственных задач.", "growth", "больше ответственных задач"),
    ("Хотелось бы больше задач повышенной сложности.", "workload", "больше задач повышенной сложности"),
    ("Сложностей за период не было.", "workload", "Сложностей"),
    ("Перегрузки в этом месяце нет.", "workload", "Перегрузки"),
    ("Нагрузка вернулась к комфортному уровню.", "workload", "Нагрузка"),
    ("По задачам и взаимодействию проблем нет.", "team", "взаимодействию"),
])
def test_negation_growth_and_invented_quotes_are_not_hidden(text, theme, quote):
    assert insights.valid_items([{"theme": theme, "polarity": "negative", "quote": quote}], text) == []


def test_null_and_low_score_do_not_form_hidden_signal():
    row = api.Response(**payload()["history"][0])
    labels = [(row, "difficulties", [("workload", "negative", "Нагрузка")])]
    for value in (None, 1, 2, 3):
        row.scores["workload"] = value
        assert insights.hidden_signals(labels) == []


def test_mixed_clause_keeps_load_but_not_absence_of_team_problem():
    text = "Основная сложность — высокая нагрузка; по задачам и взаимодействию проблем нет."
    items = [{"theme": "workload", "polarity": "negative", "quote": "высокая нагрузка"},
             {"theme": "team", "polarity": "negative", "quote": "взаимодействию"}]
    assert insights.valid_items(items, text) == [("workload", "negative", "высокая нагрузка")]


def test_label_cache_survives_restart_and_hash_invalidates(monkeypatch):
    row = api.Response(**payload()["history"][0])
    row.scores["workload"] = 4
    row.comments = {"difficulties": "Добавилось много проектов.", "barriers": ""}
    calls = []
    def ask(prompt, data):
        calls.append(data)
        if "items" in prompt:
            return {"rows": [{"index": 0, "items": [{"theme": "workload", "polarity": "negative", "quote": row.comments["difficulties"]}]}]}
        return {"plans": []}
    monkeypatch.setattr(insights, "configured_model", lambda: ask)
    first, _ = insights.analyze_insights([row])
    assert len(first) == 1 and insights.cache_path().exists()
    insights._labels_cache.clear()
    monkeypatch.setattr(insights, "configured_model", lambda: None)
    again, _ = insights.analyze_insights([row])
    assert again == first
    row.comments["difficulties"] = "Теперь задачи распределены комфортно."
    assert insights.analyze_insights([row])[0] == []


def test_one_on_one_for_st005_month1_without_llm():
    row = next(r for r in history_rows() if (r.internId, r.month, r.source) == ("СТ-005", 1, "intern"))
    signals, plans = insights.analyze_insights([row])
    assert signals == [] and plans[0]["actionType"] == "weekly_1on1"
    assert len(plans[0]["questions"]) == 3
    assert all(ref.startswith(row.eventId + "#") for ref in plans[0]["evidenceIds"])
    assert plans[0]["mentorMessage"] == insights.TEMPLATES["weekly_1on1"][2]


def test_plan_rejects_wrong_month_refs_and_ignores_model_mentor_text():
    evidence = {"weekly_1on1": {"early#mentorSupport": "2"}}
    model = {"situation": "Нужно обсудить формат встреч.", "actionType": "weekly_1on1",
             "evidenceIds": ["early#mentorSupport"],
             "questions": ["Что поможет подготовиться?", "Как выбирать время?", "Какие вопросы важны?"],
             "mentorMessage": "Секретная цитата стажера"}
    plan = insights.plan_for_month(1, evidence, lambda *_: model)
    assert plan["situation"] == model["situation"]
    assert plan["mentorMessage"] == insights.TEMPLATES["weekly_1on1"][2]
    model["evidenceIds"] = ["late#mentorSupport"]
    assert insights.plan_for_month(1, evidence, lambda *_: model)["evidenceIds"] == ["early#mentorSupport"]


def test_new_features_do_not_replace_qwen_summary(monkeypatch):
    api._cache.clear()
    monkeypatch.setenv("AGENT_TOKEN", token)
    monkeypatch.setenv("AGENT_REFERENCE_MODE", "0")
    monkeypatch.setattr(api, "generate", lambda _: {"summary": "Существующее резюме Qwen", "signals": [], "recommendations": []})
    response = TestClient(api.app).post("/v1/analyze", json=payload(), headers={
        "Authorization": "Bearer " + token, "Idempotency-Key": "request-1"})
    assert response.status_code == 200, response.text
    assert response.json()["summary"] == "Существующее резюме Qwen"
    assert response.json()["conversationPlans"][0]["actionType"] == "reduce_load"


def test_duplicate_event_or_slot_rejected(monkeypatch):
    monkeypatch.setenv("AGENT_TOKEN", token)
    p = payload(); p["history"] *= 2
    response = TestClient(api.app).post("/v1/analyze", json=p, headers={
        "Authorization": "Bearer " + token, "Idempotency-Key": "request-1"})
    assert response.status_code == 422
