import json

import httpx
import pytest
from fastapi import HTTPException
from fastapi.testclient import TestClient

import app as api
import llm
from test_app import payload, token


@pytest.fixture(autouse=True)
def isolate(monkeypatch):
    api._cache.clear()
    monkeypatch.setenv("AGENT_TOKEN", token)
    monkeypatch.setenv("AGENT_REFERENCE_MODE", "0")
    monkeypatch.setenv("LLM_API_KEY", "fake-upstream-key")
    monkeypatch.setenv("LLM_BASE_URL", "https://llm.example/v1")
    monkeypatch.setenv("LLM_MODEL", "qwen/qwen3.6-flash")


def content():
    return {"summary": "Нагрузка требует обсуждения в месяце 1.",
            "signals": [{"id": "ai-1", "kind": "workload", "severity": "critical",
                         "title": "Низкая комфортность нагрузки, месяц 1",
                         "evidenceIds": ["ev-1"]}],
            "recommendations": ["Согласовать приоритеты на встрече."]}


def upstream(data, finish="stop"):
    return {"choices": [{"finish_reason": finish, "message": {"content": json.dumps(data)}}]}


def call():
    return TestClient(api.app).post("/v1/analyze", json=payload(),
        headers={"Authorization": "Bearer " + token, "Idempotency-Key": "request-1"})


def test_model_request_and_exact_evidence(monkeypatch):
    calls = []
    def request(endpoint, key, data):
        calls.append(data)
        assert endpoint == "https://llm.example/v1/chat/completions"
        assert key == "fake-upstream-key"
        assert data["model"] == "qwen/qwen3.6-flash"
        assert data["reasoning"]["enabled"] is False
        assert data["response_format"]["type"] == "json_object"
        assert "fake-upstream-key" not in json.dumps(data)
        return upstream(content())
    monkeypatch.setattr(llm, "_request", request)
    response = call()
    assert response.status_code == 200, response.text
    assert response.json()["requestId"] == "request-1"
    assert response.json()["signals"][0]["evidence"][0]["value"] == "2"
    assert call().json() == response.json()
    assert len(calls) == 1


@pytest.mark.parametrize("bad", ["evidence", "unknown_event", "duplicate", "invalid_schema", "truncated"])
def test_model_invalid_output_rejected(monkeypatch, bad):
    data = content()
    if bad == "evidence": data["signals"][0]["evidenceIds"] = ["unknown"]
    if bad == "unknown_event": data["signals"][0]["evidenceIds"] = ["ev-9999"]
    if bad == "duplicate": data["signals"] *= 2
    if bad == "invalid_schema": data["signals"][0]["severity"] = "urgent"
    monkeypatch.setattr(llm, "_request", lambda *args: upstream(data, "length" if bad == "truncated" else "stop"))
    response = call()
    assert response.status_code == 502, response.text
    assert not api._cache


@pytest.mark.parametrize("status", [401, 429, 500])
def test_transport_sanitizes_upstream_errors(monkeypatch, status):
    original = httpx.Client
    transport = httpx.MockTransport(lambda request: httpx.Response(status, text="fake-upstream-key internal error"))
    monkeypatch.setattr(llm.httpx, "Client", lambda **kwargs: original(transport=transport, **kwargs))
    with pytest.raises(HTTPException) as error:
        llm._request("https://llm.example/v1/chat/completions", "fake-upstream-key", {})
    assert error.value.status_code == 502
    assert "fake-upstream-key" not in error.value.detail


def test_transport_timeout(monkeypatch):
    original = httpx.Client
    def fail(request): raise httpx.ReadTimeout("timeout", request=request)
    monkeypatch.setattr(llm.httpx, "Client", lambda **kwargs: original(transport=httpx.MockTransport(fail), **kwargs))
    with pytest.raises(HTTPException) as error:
        llm._request("https://llm.example/v1/chat/completions", "fake-upstream-key", {})
    assert error.value.status_code == 504


def test_null_is_preserved_in_model_history(monkeypatch):
    p = payload()
    p["history"][0]["scores"]["interest"] = None
    def request(endpoint, key, data):
        rows = json.loads(data["messages"][1]["content"])["history"]
        assert "interest" in rows[0]["scores"] and rows[0]["scores"]["interest"] is None
        return upstream(content())
    monkeypatch.setattr(llm, "_request", request)
    response = TestClient(api.app).post("/v1/analyze", json=p,
        headers={"Authorization": "Bearer " + token, "Idempotency-Key": "request-1"})
    assert response.status_code == 200, response.text


def test_evidence_validated_even_if_generator_is_replaced(monkeypatch):
    data = content()
    data["signals"][0].pop("evidenceIds")
    data["signals"][0]["evidence"] = [{"eventId": "event-1", "field": "workload", "value": "5"}]
    monkeypatch.setattr(api, "generate", lambda history: data)
    assert call().status_code == 502
