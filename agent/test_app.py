from fastapi.testclient import TestClient
from app import app, _cache

client = TestClient(app)
token = "test-python-agent-token-2026"


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
    assert client.post("/v1/analyze", headers=h, json=payload()).status_code == 501


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
