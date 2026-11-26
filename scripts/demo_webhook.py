"""Local end-to-end signed Tally simulation, using Python standard library only.

Run against a disposable demo database. It creates СТ-DEMO and two responses.
It never calls the Tally service and does not configure actual external forms.
"""
import argparse
import base64
import hashlib
import hmac
import http.cookiejar
import json
import os
import urllib.error
import urllib.request


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--url", default="http://127.0.0.1:8080")
    args = parser.parse_args()
    secret = os.environ["TALLY_SIGNING_SECRET"]
    form_ids = {"intern": os.environ["TALLY_INTERN_FORM_ID"],
                "mentor": os.environ["TALLY_MENTOR_FORM_ID"]}
    client = urllib.request.build_opener(urllib.request.HTTPCookieProcessor(http.cookiejar.CookieJar()))

    def call(path, data=None, extra_headers=None):
        body = None if data is None else json.dumps(data, ensure_ascii=False, separators=(",", ":")).encode()
        headers = {"Content-Type": "application/json", **(extra_headers or {})}
        if path.endswith("/tally"):
            headers["Tally-Signature"] = base64.b64encode(hmac.new(secret.encode(), body, hashlib.sha256).digest()).decode()
        req = urllib.request.Request(args.url + "/api/v1" + path, data=body, headers=headers)
        try:
            with client.open(req, timeout=10) as res:
                return res.status, json.load(res)
        except urllib.error.HTTPError as err:
            return err.code, json.load(err)

    status, _ = call("/auth/login", {"username": os.getenv("DEMO_HR_USERNAME", "hr"),
                                    "password": os.getenv("DEMO_HR_PASSWORD", "Mgts-demo-2026!")})
    assert status == 200, "HR login failed"
    status, _ = call("/hr/enrollments", {"id": "СТ-DEMO", "department": "IT", "mentorId": "Н-DEMO"})
    assert status in (201, 409), "Enrollment failed"
    with open(os.path.join(os.path.dirname(__file__), "..", "web", "questions.json"), encoding="utf-8") as f:
        questions = json.load(f)
    _, schema = call("/schema")
    for source in ("intern", "mentor"):
        fields = [{"label": "ID стажера", "value": "СТ-DEMO"},
                  {"label": "Подразделение / направление стажировки", "value": "IT"},
                  {"label": "Контрольный период", "value": 1}]
        if source == "mentor":
            fields.append({"label": "ID наставника", "value": "Н-DEMO"})
        for key in schema[source + "Fields"]:
            fields.append({"label": schema["labels"][key], "value": 2 if key == "workload" else 4})
        for key in (["difficulties", "barriers"] if source == "intern" else ["feedback", "attention"]):
            assert key in questions
            fields.append({"label": schema["labels"][key], "value": "Локальный тест доставки"})
        event = {"eventId": "local-demo-" + source, "eventType": "FORM_RESPONSE",
                 "data": {"formId": form_ids[source], "fields": fields}}
        first, result = call("/integrations/tally", event)
        assert first in (200, 201), result
        second, repeated = call("/integrations/tally", event)
        assert second == 200 and repeated["status"] == "duplicate", repeated
        print(source, result["status"], "; repeated: duplicate")
    _, profile = call("/hr/interns/" + urllib.parse.quote("СТ-DEMO") + "?month=1")
    assert len(profile["responses"]) == 2
    assert profile["status"] == "critical"
    _, overview = call("/hr/overview?month=1")
    assert any(x["person"]["id"] == "СТ-DEMO" and x["internSubmitted"] and x["mentorSubmitted"]
               for x in overview["items"])
    print("PASS: two answers in history; workload signal and updated overview present.")


if __name__ == "__main__":
    main()
