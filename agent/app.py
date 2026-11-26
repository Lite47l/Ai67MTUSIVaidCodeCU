"""FastAPI agent: Qwen analysis with exact evidence validation."""
from __future__ import annotations

import hashlib
import hmac
import json
import os
from collections import OrderedDict
from threading import Lock
from typing import Literal

from fastapi import FastAPI, Header, HTTPException
from pydantic import BaseModel, ConfigDict, Field, StrictInt
from pydantic import ValidationError

from llm import generate


class StrictModel(BaseModel):
    model_config = ConfigDict(extra="forbid")


class Response(StrictModel):
    eventId: str = Field(min_length=3, max_length=160)
    programId: Literal["mgts-internship"]
    internId: str
    mentorId: str | None = None
    department: str
    month: int = Field(ge=1, le=6)
    source: Literal["intern", "mentor"]
    scores: dict[str, StrictInt | None]
    comments: dict[str, str]
    receivedAt: str | None = None


class AnalyzeRequest(StrictModel):
    schemaVersion: Literal["1.0"]
    requestId: str = Field(min_length=1, max_length=256)
    revision: str = Field(pattern=r"^[a-f0-9]{64}$")
    programId: Literal["mgts-internship"]
    internId: str
    history: list[Response] = Field(min_length=1, max_length=12)


class Evidence(StrictModel):
    eventId: str
    field: str
    value: str


class Signal(StrictModel):
    id: str = Field(min_length=1, max_length=160)
    kind: str = Field(min_length=1, max_length=100)
    severity: Literal["positive", "attention", "critical"]
    title: str = Field(min_length=1, max_length=2000)
    evidence: list[Evidence] = Field(min_length=1, max_length=20)


class AnalyzeResult(StrictModel):
    schemaVersion: Literal["1.0"]
    requestId: str
    revision: str
    summary: str = Field(min_length=1, max_length=10000)
    signals: list[Signal] = Field(max_length=100)
    recommendations: list[str] = Field(max_length=20)


app = FastAPI(title="МГТС Python Agent Contract", version="1.0.0")
_cache: OrderedDict[str, tuple[str, AnalyzeResult]] = OrderedDict()
_lock = Lock()


def authorize(authorization: str | None) -> None:
    secret = os.getenv("AGENT_TOKEN", "")
    if len(secret) < 24:
        raise HTTPException(503, "AGENT_TOKEN must contain at least 24 characters")
    expected = ("Bearer " + secret).encode()
    if not hmac.compare_digest((authorization or "").encode(), expected):
        raise HTTPException(401, "Invalid agent credentials")


def analyze_history(req: AnalyzeRequest) -> AnalyzeResult:
    if os.getenv("AGENT_REFERENCE_MODE") == "1":
        return AnalyzeResult(
            schemaVersion="1.0", requestId=req.requestId, revision=req.revision,
            summary="Контракт Go ↔ FastAPI проверен. Это ответ адаптера без AI-модели; аналитические выводы не сформированы.",
            signals=[], recommendations=[],
        )
    # IDs are owned by Go, never generated or changed by the model.
    history = [row.model_dump(mode="json", exclude_none=True) for row in
               sorted(req.history, key=lambda row: (row.month, row.source))]
    try:
        result = AnalyzeResult(schemaVersion="1.0", requestId=req.requestId,
                               revision=req.revision, **generate(history))
    except (ValidationError, TypeError):
        raise HTTPException(502, "LLM analysis failed schema validation") from None
    evidence = {}
    for row in req.history:
        for field, value in row.scores.items():
            if value is not None:
                evidence[(row.eventId, field)] = str(value)
        for field, value in row.comments.items():
            if value:
                evidence[(row.eventId, field)] = value
    ids = set()
    for signal in result.signals:
        if signal.id in ids:
            raise HTTPException(502, "LLM returned duplicate signal IDs")
        ids.add(signal.id)
        for item in signal.evidence:
            if evidence.get((item.eventId, item.field)) != item.value:
                raise HTTPException(502, "LLM evidence does not match original responses")
    return result


@app.get("/healthz")
def health() -> dict[str, str]:
    return {"status": "alive"}


@app.post("/v1/analyze", response_model=AnalyzeResult)
def analyze(req: AnalyzeRequest, authorization: str | None = Header(default=None),
            idempotency_key: str | None = Header(default=None)) -> AnalyzeResult:
    authorize(authorization)
    if idempotency_key != req.requestId:
        raise HTTPException(422, "Idempotency-Key must equal requestId")
    for row in req.history:
        if row.internId != req.internId or row.programId != req.programId:
            raise HTTPException(422, "History contains another participant")
        if any(value is not None and not 1 <= value <= 5 for value in row.scores.values()):
            raise HTTPException(422, "Score must be 1–5 or null")
    payload = json.dumps(req.model_dump(mode="json"), sort_keys=True, ensure_ascii=False)
    fingerprint = hashlib.sha256(payload.encode()).hexdigest()
    # Serialized adapter for a single prototype worker. Replace with a durable
    # result store keyed by requestId when scaling the real model service.
    with _lock:
        if req.requestId in _cache:
            old_hash, result = _cache[req.requestId]
            if old_hash != fingerprint:
                raise HTTPException(409, "Changed replay")
            return result
        result = analyze_history(req)
        _cache[req.requestId] = (fingerprint, result)
        if len(_cache) > 128:
            _cache.popitem(last=False)
        return result
