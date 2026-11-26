"""FastAPI agent: Qwen analysis with exact evidence validation."""
from __future__ import annotations

import hashlib
import hmac
import json
import os
from collections import OrderedDict
from concurrent.futures import ThreadPoolExecutor
from threading import Lock
from typing import Literal

from fastapi import FastAPI, Header, HTTPException
from pydantic import BaseModel, ConfigDict, Field, StrictInt
from pydantic import ValidationError

from llm import generate
from insights import analyze_insights, TEMPLATES


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
            summary="Контракт Go ↔ FastAPI проверен. Это ответ адаптера без AI-модели; аналитические выводы не сформированы.",
            signals=[], recommendations=[],
        )
    # IDs are owned by Go, never generated or changed by the model.
    history = [row.model_dump(mode="json", exclude_none=True) for row in
               sorted(req.history, key=lambda row: (row.month, row.source))]
    try:
        # Summary and a single batched annotation/plan request run in parallel
        # with 20s transport timeouts, within the Go worker's 25s deadline.
        with ThreadPoolExecutor(max_workers=2) as pool:
            future = pool.submit(analyze_insights, req.history)
            base = generate(history)
            hidden, plans = future.result()
        if any(signal.get("kind") == "text_score_mismatch" for signal in base.get("signals", [])):
            raise HTTPException(502, "Reserved signal kind must come from verified annotation")
        base["signals"] = [*base["signals"], *hidden]
        result = AnalyzeResult(schemaVersion="1.0", requestId=req.requestId,
                               revision=req.revision, **base, conversationPlans=plans)
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
    validate_plans(result.conversationPlans or [], req)
    return result


def validate_plans(plans: list[ConversationPlan], req: AnalyzeRequest) -> None:
    index = {row.eventId: row for row in req.history}
    months = set()
    for plan in plans:
        if plan.month in months or len(set(plan.evidenceIds)) != len(plan.evidenceIds):
            raise HTTPException(502, "Duplicate conversation plan or evidence")
        months.add(plan.month)
        if plan.mentorMessage != TEMPLATES[plan.actionType][2]:
            raise HTTPException(502, "Unsafe mentor message")
        for ref in plan.evidenceIds:
            event_id, sep, field = ref.rpartition("#")
            row = index.get(event_id)
            if (not sep or row is None or row.month != plan.month or
                (row.scores.get(field) is None and not row.comments.get(field, "").strip())):
                raise HTTPException(502, "Conversation plan references unknown evidence")


@app.get("/healthz")
def health() -> dict[str, str]:
    return {"status": "alive"}


@app.post("/v1/analyze", response_model=AnalyzeResult, response_model_exclude_none=True)
def analyze(req: AnalyzeRequest, authorization: str | None = Header(default=None),
            idempotency_key: str | None = Header(default=None)) -> AnalyzeResult:
    authorize(authorization)
    if idempotency_key != req.requestId:
        raise HTTPException(422, "Idempotency-Key must equal requestId")
    seen_events, seen_slots = set(), set()
    for row in req.history:
        if row.internId != req.internId or row.programId != req.programId:
            raise HTTPException(422, "History contains another participant")
        if any(value is not None and not 1 <= value <= 5 for value in row.scores.values()):
            raise HTTPException(422, "Score must be 1–5 or null")
        if row.eventId in seen_events or (row.month, row.source) in seen_slots:
            raise HTTPException(422, "History contains duplicate events or slots")
        seen_events.add(row.eventId)
        seen_slots.add((row.month, row.source))
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
