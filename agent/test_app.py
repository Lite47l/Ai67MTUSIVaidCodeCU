import hashlib
import io
import json
from urllib.error import URLError

from fastapi.testclient import TestClient
import pytest
from app import app, _cache
import insights

client = TestClient(app)
token = "test-python-agent-token-2026"


def analyze(history, request_id="insights-1"):
    body = payload()
    body.update(history=history, requestId=request_id)
    return client.post("/v1/analyze", json=body, headers={
        "Authorization": "Bearer " + token,
        "Idempotency-Key": request_id,
    })


def checked_label(row, theme, quote, field="difficulties"):
    return {
        "eventId": row["eventId"], "field": field,
        "textHash": hashlib.sha256(row["comments"][field].encode()).hexdigest(),
        "items": [{"theme": theme, "polarity": "negative", "quote": quote}],
    }


def use_reviewed_labels(monkeypatch, tmp_path, labels):
    path = tmp_path / "labels.json"
    path.write_text(json.dumps(labels, ensure_ascii=False), encoding="utf-8")
    monkeypatch.setenv("AGENT_REVIEWED_LABELS", str(path))
    monkeypatch.setenv("AGENT_TOKEN", token)
    monkeypatch.delenv("AGENT_REFERENCE_MODE", raising=False)
    monkeypatch.delenv("AGENT_LLM_URL", raising=False)
    _cache.clear()


def payload():
    return {"schemaVersion": "1.0", "requestId": "request-1", "revision": "a" * 64,
            "programId": "mgts-internship", "internId": "СТ-001",
            "history": [{"eventId": "event-1", "programId": "mgts-internship",
                         "internId": "СТ-001", "department": "IT", "month": 1,
                         "source": "intern", "scores": {"workload": 2},
                         "comments": {"difficulties": "Нагрузка"}}]}


def test_contract_and_idempotency(monkeypatch):
    _cache.clear()
    monkeypatch.setenv("AGENT_TOKEN", token)
    monkeypatch.setenv("AGENT_REFERENCE_MODE", "1")
    h = {"Authorization": "Bearer " + token, "Idempotency-Key": "request-1"}
    p = payload()
    assert client.post("/v1/analyze", json=p).status_code == 401
    first = client.post("/v1/analyze", headers=h, json=p)
    assert first.status_code == 200
    assert first.json()["signals"] == []
    assert client.post("/v1/analyze", headers=h, json=p).json() == first.json()
    p["history"][0]["scores"]["workload"] = 4
    assert client.post("/v1/analyze", headers=h, json=p).status_code == 409


def test_no_fake_model(monkeypatch):
    _cache.clear()
    monkeypatch.setenv("AGENT_TOKEN", token)
    monkeypatch.delenv("AGENT_REFERENCE_MODE", raising=False)
    h = {"Authorization": "Bearer " + token, "Idempotency-Key": "request-1"}
    monkeypatch.delenv("AGENT_LLM_URL", raising=False)
    monkeypatch.delenv("AGENT_REVIEWED_LABELS", raising=False)
    result = client.post("/v1/analyze", headers=h, json=payload())
    assert result.status_code == 200
    assert result.json()["signals"] == []
    assert result.json()["conversationPlans"][0]["actionType"] == "reduce_load"


def test_invalid_history(monkeypatch):
    _cache.clear()
    monkeypatch.setenv("AGENT_TOKEN", token)
    h = {"Authorization": "Bearer " + token, "Idempotency-Key": "request-1"}
    p = payload()
    p["history"][0]["internId"] = "СТ-002"
    assert client.post("/v1/analyze", headers=h, json=p).status_code == 422
    p = payload()
    p["history"][0]["scores"]["workload"] = 0
    assert client.post("/v1/analyze", headers=h, json=p).status_code == 422


def test_attention_signal_uses_the_high_score_and_the_full_original_comment(monkeypatch, tmp_path):
    row = payload()["history"][0]
    row["scores"]["workload"] = 4
    comment = "В спринте добавилось много параллельных задач; с командой проблем нет."
    row["comments"]["difficulties"] = comment
    use_reviewed_labels(monkeypatch, tmp_path, [
        checked_label(row, "workload", "добавилось много параллельных задач"),
    ])
    response = analyze([row])
    assert response.status_code == 200
    signal, = response.json()["signals"]
    assert signal["kind"] == "text_score_mismatch"
    assert signal["severity"] == "attention"
    assert signal["evidence"] == [
        {"eventId": "event-1", "field": "workload", "value": "4"},
        {"eventId": "event-1", "field": "difficulties", "value": comment},
    ]


@pytest.mark.parametrize(("text", "theme", "quote", "score"), [
    ("Нагрузка выросла.", "workload", "Вымышленная цитата", 4),
    ("Нагрузка выросла.", "unknown", "Нагрузка выросла.", 4),
    ("Перегрузки в этом месяце нет.", "workload", "Перегрузки", 4),
    ("Нагрузка вернулась к комфортному уровню.", "workload", "Нагрузка", 4),
    ("Хотелось бы больше ответственных задач.", "workload", "больше ответственных задач", 4),
    ("Добавилось много задач.", "workload", "Добавилось много задач.", None),
])
def test_hidden_signal_rejects_invalid_or_unconfirmed_labels(monkeypatch, tmp_path, text, theme, quote, score):
    row = payload()["history"][0]
    row["scores"]["workload"] = score
    row["comments"]["difficulties"] = text
    use_reviewed_labels(monkeypatch, tmp_path, [checked_label(row, theme, quote)])
    result = analyze([row])
    assert result.status_code == 200
    assert result.json()["signals"] == []


def test_model_contract_cache_and_bounded_plan_inputs(monkeypatch, tmp_path):
    early, late = payload()["history"][0], payload()["history"][0]
    early.update(eventId="qa-early", scores={"workload": 4}, comments={"difficulties": "Добавилось много проектов."})
    late.update(eventId="qa-late", month=2, scores={"mentorSupport": 2}, comments={"difficulties": "Поздний комментарий."})
    use_reviewed_labels(monkeypatch, tmp_path, [])
    monkeypatch.setenv("AGENT_LLM_URL", "https://test.invalid/chat/completions")
    monkeypatch.setenv("AGENT_LLM_MODEL", "local-model-stub")
    model_inputs = []

    class Transport:
        def open(self, request, timeout):
            messages = json.loads(request.data)["messages"]
            data = json.loads(messages[1]["content"])
            model_inputs.append(data)
            if isinstance(data, list):
                result = {"rows": [
                    {"index": item["index"], "items": [
                        {"theme": "workload", "polarity": "negative", "quote": item["text"]},
                    ] if item["text"] == early["comments"]["difficulties"] else []}
                    for item in data
                ]}
            elif data["month"] == 1:
                assert "qa-late" not in json.dumps(data)
                result = {
                    "situation": "Нужно обсудить объем параллельных задач.",
                    "evidenceIds": ["qa-early#workload"],
                    "questions": ["Какие задачи конкурируют?", "Как выбрать приоритет?", "Что можно перенести?"],
                    "actionType": "reduce_load",
                    "mentorMessage": "Это поле от модели не публикуется.",
                    "audience": "intern",
                }
            else:
                raise URLError("temporarily unavailable")
            return io.BytesIO(json.dumps({"choices": [{"message": {"content": json.dumps(result)}}]}).encode())

    monkeypatch.setattr(insights, "build_opener", lambda *_: Transport())
    first = analyze([early, late]).json()
    assert first["signals"][0]["evidence"][0]["eventId"] == "qa-early"
    assert first["conversationPlans"][0]["situation"] == "Нужно обсудить объем параллельных задач."
    assert "audience" not in first["conversationPlans"][0]
    assert "Это поле" not in first["conversationPlans"][0]["mentorMessage"]
    assert first["conversationPlans"][1]["actionType"] == "weekly_1on1"
    assert first["conversationPlans"][1]["evidenceIds"] == ["qa-late#mentorSupport"]
    assert len(first["conversationPlans"][1]["questions"]) == 3
    assert analyze([early, late]).json() == first
    assert len(model_inputs) == 3
    monkeypatch.delenv("AGENT_LLM_URL")
    assert analyze([early, late], "offline-cache").json()["signals"] == first["signals"]
    early["comments"]["difficulties"] = "Теперь задачи распределены комфортно."
    assert analyze([early, late], "changed-text").json()["signals"] == []


def test_unrecognized_model_action_is_returned_as_hr_review(monkeypatch, tmp_path):
    row = payload()["history"][0]
    row["comments"] = {}
    use_reviewed_labels(monkeypatch, tmp_path, [])
    monkeypatch.setenv("AGENT_LLM_URL", "https://test.invalid/chat/completions")
    monkeypatch.setenv("AGENT_LLM_MODEL", "local-model-stub")

    class Transport:
        def open(self, request, timeout):
            answer = {"actionType": "change_tasks_as_intern", "audience": "intern"}
            return io.BytesIO(json.dumps({"choices": [{"message": {"content": json.dumps(answer)}}]}).encode())

    monkeypatch.setattr(insights, "build_opener", lambda *_: Transport())
    plan, = analyze([row], "unknown-action").json()["conversationPlans"]
    assert plan["actionType"] == "other"
    assert plan["evidenceIds"] == [row["eventId"] + "#workload"]
    assert len(plan["questions"]) == 3
    assert "audience" not in plan
