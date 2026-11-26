"""Explicit live test: sends the supplied СТ-004 history through FastAPI to LLM.

Requires a running agent and AGENT_TOKEN. A paid model request may be made.
No LLM key is read here; it belongs only to the running Python agent process.
"""
import argparse
import hashlib
import json
import os
from pathlib import Path

import httpx


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--url", default="http://127.0.0.1:8000")
    parser.add_argument("--intern", default="СТ-004")
    args = parser.parse_args()
    rows = json.loads((Path(__file__).resolve().parents[1] / "data/history.json").read_text(encoding="utf-8"))
    history = sorted([{k: v for k, v in r.items() if k != "raw"} for r in rows if r["internId"] == args.intern],
                     key=lambda row: (row["month"], row["source"]))
    assert history, "No historical responses for this ID"
    revision = hashlib.sha256(json.dumps(history, sort_keys=True, ensure_ascii=False).encode()).hexdigest()
    request_id = "live-test:" + revision
    body = {"schemaVersion": "1.0", "requestId": request_id, "revision": revision,
            "programId": "mgts-internship", "internId": args.intern, "history": history}
    headers = {"Authorization": "Bearer " + os.environ["AGENT_TOKEN"], "Idempotency-Key": request_id}
    with httpx.Client(timeout=30) as client:
        response = client.post(args.url.rstrip("/") + "/v1/analyze", headers=headers, json=body)
        if response.status_code != 200:
            raise SystemExit(f"FAIL: HTTP {response.status_code}: {response.text[:500]}")
        result = response.json()
        assert result["requestId"] == request_id and result["revision"] == revision
        assert "ответ адаптера без AI-модели" not in result["summary"], "Reference mode is enabled; no real LLM call"
        evidence = {}
        for row in history:
            evidence.update({(row["eventId"], k): str(v) for k, v in row["scores"].items() if v is not None})
            evidence.update({(row["eventId"], k): v for k, v in row["comments"].items() if v})
        for signal in result["signals"]:
            assert signal["evidence"]
            for item in signal["evidence"]:
                assert evidence.get((item["eventId"], item["field"])) == item["value"], "Invalid evidence"
        repeated = client.post(args.url.rstrip("/") + "/v1/analyze", headers=headers, json=body)
        assert repeated.status_code == 200 and repeated.json() == result, "Idempotency failed"
    print("PASS: real model response, exact evidence, idempotent replay")
    print(result["summary"])
    print("Signals:", len(result["signals"]))


if __name__ == "__main__":
    main()
