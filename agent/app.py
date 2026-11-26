"""FastAPI boundary for explainable analysis of internship feedback."""
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
from insights import analyze_insights


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


class ConversationPlan(StrictModel):
    month: int = Field(ge=1, le=6)
    situation: str = Field(min_length=1, max_length=500)
    evidenceIds: list[str] = Field(min_length=1, max_length=8)
    questions: list[str] = Field(min_length=3, max_length=3)
    actionType: Literal["weekly_1on1", "reduce_load", "clarify_tasks", "hr_talk", "other"]
    mentorMessage: str = Field(min_length=1, max_length=1000)


class AnalyzeResult(StrictModel):
    schemaVersion: Literal["1.0"]
    requestId: str
    revision: str
    summary: str = Field(min_length=1, max_length=10000)
    signals: list[Signal] = Field(max_length=100)
    recommendations: list[str] = Field(max_length=20)
    conversationPlans: list[ConversationPlan] | None = Field(default=None, max_length=6)


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
            summary="Проверка связи с Go. Анализ отключен в режиме проверки контракта.",
            signals=[], recommendations=[],
        )
    signals, plans = analyze_insights(req.history)
    return AnalyzeResult(
        schemaVersion="1.0", requestId=req.requestId, revision=req.revision,
        summary="Проверьте оценки и комментарии, на которых основан анализ выбранного месяца.",
        signals=signals, recommendations=[], conversationPlans=plans,
    )


@app.get("/healthz")
def health() -> dict[str, str]:
    return {"status": "alive"}


@app.post("/v1/analyze", response_model=AnalyzeResult, response_model_exclude_none=True)
def analyze(req: AnalyzeRequest, authorization: str | None = Header(default=None),
            idempotency_key: str | None = Header(default=None)) -> AnalyzeResult:
    authorize(authorization)
    if idempotency_key != req.requestId:
        raise HTTPException(422, "Idempotency-Key must equal requestId")
    event_ids = set()
    slots = set()
    for row in req.history:
        if row.internId != req.internId or row.programId != req.programId:
            raise HTTPException(422, "History contains another participant")
        if any(value is not None and not 1 <= value <= 5 for value in row.scores.values()):
            raise HTTPException(422, "Score must be 1–5 or null")
        slot = (row.month, row.source)
        if row.eventId in event_ids or slot in slots:
            raise HTTPException(422, "History contains a duplicate event or slot")
        event_ids.add(row.eventId)
        slots.add(slot)
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
