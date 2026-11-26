"""Black-box acceptance smoke for the MGTS demo program."""
import argparse
from collections import Counter
from datetime import datetime, timezone
import json
import os
from pathlib import Path
from urllib.error import HTTPError, URLError
from urllib.parse import quote, urlencode
from urllib.request import Request, urlopen
from uuid import uuid4
from zipfile import BadZipFile

from openpyxl import load_workbook


SCORES = {
    "intern": ("task_clarity", "mentor_support", "workload_comfort", "task_interest",
               "professional_growth", "team_interaction", "overall_satisfaction"),
    "mentor": ("task_understanding", "work_quality", "independence",
               "deadline_organization", "initiative", "mentor_team_interaction"),
}
COMMENTS = {
    "intern": ("period_difficulties", "work_barriers"),
    "mentor": ("period_comment", "attention_situations"),
}
WORKLOAD = "СТ-004"
RECOVERY = ("СТ-003", "СТ-005")
INTERNS = {f"СТ-{n:03}" for n in range(1, 6)}
PERIODS = range(1, 7)
STATUSES = {"normal", "attention", "critical"}


class NotTested(Exception):
    pass


def require(condition, message):
    if not condition:
        raise ValueError(message)


def run(name, check):
    try:
        result = check()
    except NotTested as exc:
        print(f"NOT TESTED {name}: {exc}")
        return 2, None
    except (ValueError, KeyError, TypeError, OSError, BadZipFile) as exc:
        detail = str(exc) if type(exc) in (ValueError, OSError) else type(exc).__name__
        print(f"FAIL {name}: {detail}")
        return 1, None
    print(f"PASS {name}")
    return 0, result


def source_rows(path, source):
    # The two supplied demo sheets use fixed columns, with mentor ID first in the mentor sheet.
    offset = int(source == "mentor")
    periods = {f"Месяц {n}": n for n in PERIODS}
    book = load_workbook(path, read_only=True, data_only=True)
    try:
        rows = book.active.values
        header = next(rows, ())
        require(header[offset:offset + 3] == (
            "ID стажера", "Подразделение / направление стажировки", "Контрольный период"
        ), f"{source}: unexpected Excel layout")
        result = {}
        for line, row in enumerate(rows, 2):
            if all(value is None for value in row):
                continue
            intern, department, label = row[offset:offset + 3]
            require(intern in INTERNS and department and label in periods,
                    f"{source}: invalid ID, department or period at row {line}")
            key = intern, periods[label], source
            require(key not in result, f"{source}: duplicate at row {line}")
            scores = dict(zip(SCORES[source], row[offset + 3:10]))
            require(len(scores) == len(SCORES[source]) and all(
                type(value) is int and 1 <= value <= 5 for value in scores.values()
            ), f"{source}: invalid or missing score at row {line}")
            comments = dict(zip(COMMENTS[source], row[10:12]))
            result[key] = {"scores": scores, "comments": comments}
        require(len(result) == 30, f"{source}: expected 30 unique rows, got {len(result)}")
        return result
    finally:
        book.close()


def check_examples(history):
    def series(intern, source, field, periods):
        return [history[intern, period, source]["scores"][field] for period in periods]

    require(series(WORKLOAD, "intern", "workload_comfort", (4, 5)) == [2, 4],
            "СТ-004 workload: expected 2/5 → 4/5 in months 4–5")
    for intern in RECOVERY:
        early = (2, 3) if intern == "СТ-003" else (1, 2, 3)
        require(series(intern, "intern", "mentor_support", early) == [2] * len(early)
                and min(series(intern, "intern", "mentor_support", (4, 5, 6))) >= 4,
                f"{intern}: expected repeated low mentor support and recovery in months 4–6")
    require(series("СТ-005", "mentor", "independence", (1, 2, 4, 5, 6)) == [2, 2, 4, 4, 5],
            "СТ-005: expected mentor-side recovery in independence")


class Api:
    def __init__(self, base_url):
        self.root = base_url.rstrip("/") + "/api/v1"
        self.token = os.environ.get("QA_INGEST_TOKEN")

    def request(self, path, query=None, body=None):
        url = self.root + path
        if query:
            url += "?" + urlencode(query)
        headers = {"Accept": "application/json"}
        data = None
        if body is not None:
            data = json.dumps(body).encode()
            headers["Content-Type"] = "application/json"
            if self.token:
                headers["Authorization"] = f"Bearer {self.token}"
        try:
            with urlopen(Request(url, data=data, headers=headers), timeout=5) as response:
                try:
                    return response.status, json.load(response)
                except (ValueError, UnicodeError):
                    raise ValueError(f"{path}: expected a JSON response") from None
        except HTTPError as exc:
            exc.close()
            return exc.code, None
        except (URLError, TimeoutError) as exc:
            raise NotTested(f"{path}: API unreachable ({type(exc).__name__})") from None

    def get(self, path, **query):
        status, data = self.request(path, query)
        require(status == 200, f"GET {path}: HTTP {status}, expected 200")
        return data

    def profile(self, program, intern, period):
        return self.get("/interns/" + quote(intern, safe=""), program_id=program, period=period)


# Keep unconfirmed GET response envelopes here. Semantic checks below use contract field names.
def overview(api, program, period):
    payload = api.get("/overview", program_id=program, period=period)
    require(payload["period"] == period and payload["participants"] == len(INTERNS),
            f"overview month {period}: expected the requested slice and 5 participants")
    for source in SCORES:
        count = payload["responses"][source]
        require((count["expected"], count["received"]) == (5, 5),
                f"overview month {period}/{source}: expected 5/5 responses")
    return payload["queue"]


def profile_history(profile):
    rows = profile["history"]
    months = Counter(row["period"] for row in rows)
    require(all(type(month) is int and month in PERIODS for month in months)
            and all(count == 1 for count in months.values()),
            "profile history: invalid or duplicate period")
    return {(row["period"], source): row[source] for row in rows for source in SCORES}


def check_history(api, program, expected):
    profiles = {intern: api.profile(program, intern, 6) for intern in sorted(INTERNS)}
    actual = {}
    for intern, profile in profiles.items():
        for (month, source), response in profile_history(profile).items():
            if response is not None:
                actual[intern, month, source] = response
    require(actual.keys() == expected.keys(),
            f"history: expected all 60 ID/month/source slots, got {len(actual)}")
    for key, record in expected.items():
        scores = actual[key]["scores"]
        require(all(type(value) is int and 1 <= value <= 5 for value in scores.values())
                and scores == record["scores"], f"{key}: scores differ from Excel")
    check_examples(actual)
    for month in (4, 5):
        slot = profile_history(api.profile(program, WORKLOAD, month))[month, "intern"]
        original = expected[WORKLOAD, month, "intern"]
        require(slot["scores"]["workload_comfort"] == original["scores"]["workload_comfort"]
                and slot["comments"]["period_difficulties"] == original["comments"]["period_difficulties"],
                f"СТ-004 month {month}: workload or exact comment differs from Excel")
    return profiles


def check_evidence(profiles, expected):
    for intern, profile in profiles.items():
        signal_ids = set()
        for signal in profile["signals"]:
            tag = f"{intern}, signal month {signal['period']}"
            require(signal["intern_id"] == intern and type(signal["period"]) is int
                    and signal["period"] in PERIODS
                    and signal["status"] in STATUSES, f"{tag}: invalid scope or status")
            require(all(isinstance(signal.get(field), str) and signal[field].strip()
                        for field in ("id", "kind", "title", "explanation", "next_step")),
                    f"{tag}: incomplete signal metadata")
            require(signal["id"] not in signal_ids, f"{tag}: duplicate signal ID")
            signal_ids.add(signal["id"])
            require(isinstance(signal["evidence"], list) and signal["evidence"],
                    f"{tag}: empty evidence")
            for item in signal["evidence"]:
                source, month = item["source"], item["period"]
                record = expected.get((intern, month, source))
                require(type(month) is int and record is not None and month <= signal["period"],
                        f"{tag}: evidence references an invalid source or period")
                field, citation = item.get("field"), item.get("quote")
                values = record["scores"] | record["comments"]
                has_value = field in values and values[field] is not None and item.get("value") == values[field]
                comments = [values[field]] if field in record["comments"] else record["comments"].values()
                has_quote = isinstance(citation, str) and bool(citation.strip()) and any(
                    isinstance(comment, str) and citation in comment for comment in comments
                )
                require((has_value or has_quote) and (not citation or has_quote),
                        f"{tag}: evidence value or quote cannot be traced to Excel")

    for intern, kind, field, month, evidence_months in (
        (WORKLOAD, "low_score", "workload_comfort", 4, {4}),
        ("СТ-003", "repeated_issue", "mentor_support", 3, {2, 3}),
        ("СТ-005", "repeated_issue", "mentor_support", 3, {2, 3}),
    ):
        candidates = [
            {
                item["period"] for item in signal["evidence"]
                if item["source"] == "intern" and item.get("field") == field and item.get("value") == 2
            }
            for signal in profiles[intern]["signals"]
            if signal["kind"] == kind and signal["period"] == month
        ]
        require(any(evidence_months <= observed for observed in candidates),
                f"{intern} month {month}: expected {kind}/{field} with evidence from {sorted(evidence_months)}")


def check_current(profiles, queue):
    for intern in (*RECOVERY, WORKLOAD):
        status = profiles[intern]["status"]
        require(status in {"normal", "attention"}, f"{intern}: unexpected current status {status!r}")
        require(not any(signal["period"] == 6 and signal["status"] == "critical"
                        for signal in profiles[intern]["signals"]),
                f"{intern}: unexpected critical signal in month 6")
    for item in queue:
        if item["intern_id"] in (*RECOVERY, WORKLOAD):
            require(item["status"] == profiles[item["intern_id"]]["status"],
                    f"{item['intern_id']}: month 6 queue and profile disagree")


def check_ingest(api, program):
    intern = "СТ-999"
    event = {
        "source_event_id": f"qa-smoke:{uuid4()}",
        "program_id": program,
        "source": "intern",
        "intern_id": intern,
        "mentor_id": None,
        "department": "QA synthetic",
        "period": 1,
        "scores": dict.fromkeys(SCORES["intern"], 4) | {"workload_comfort": 2},
        "comments": {"period_difficulties": "Synthetic QA answer"},
        "submitted_at": datetime.now(timezone.utc).isoformat(timespec="seconds"),
    }
    path = "/responses"
    status, initial = api.request(path, body=event)
    if status in (404, 405, 501) or (status in (401, 403) and not api.token):
        raise NotTested(f"POST {path}: HTTP {status}, endpoint or credentials unavailable")
    require(status in (200, 201) and initial["id"] and initial["duplicate"] is False,
            f"initial POST {path}: expected new response ID (HTTP {status})")
    before = profile_history(api.profile(program, intern, 1))
    require(before[1, "intern"]["scores"] == event["scores"],
            "new synthetic response did not reach HR history")
    require(before[1, "mentor"] is None, "missing mentor response must be null")

    status, repeated = api.request(path, body=event)
    require(status in (200, 201) and repeated["id"] == initial["id"] and repeated["duplicate"] is True,
            f"repeated POST {path}: expected original ID and duplicate=true (HTTP {status})")
    changed = event | {"scores": event["scores"] | {"workload_comfort": 3}}
    status, _ = api.request(path, body=changed)
    require(status == 409, f"changed POST {path}: expected HTTP 409, got {status}")
    after = profile_history(api.profile(program, intern, 1))
    require(before == after, "HR history changed after duplicate or conflict")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--base-url", default=os.environ.get("BASE_URL", "http://127.0.0.1:8000"))
    parser.add_argument("--intern-xlsx", type=Path, required=True)
    parser.add_argument("--mentor-xlsx", type=Path, required=True)
    parser.add_argument("--program-id", required=True, help="Program containing the 60 demo responses")
    parser.add_argument("--ingest-program-id", help="Separate writable QA program with no mentor for СТ-999")
    parser.add_argument("--excel-only", action="store_true")
    args = parser.parse_args()
    if args.ingest_program_id == args.program_id or (args.excel_only and args.ingest_program_id):
        parser.error("--ingest-program-id requires API mode and a separate program")
    codes = []

    def stage(name, check):
        code, result = run(name, check)
        codes.append(code)
        return result

    expected = stage("excel_60", lambda: source_rows(args.intern_xlsx, "intern")
                     | source_rows(args.mentor_xlsx, "mentor"))
    if expected is None:
        return 1
    stage("excel_examples", lambda: check_examples(expected))
    if args.excel_only:
        return max(codes)

    api = Api(args.base_url)
    queue = stage("overview_5x6", lambda: [overview(api, args.program_id, n) for n in PERIODS])
    profiles = stage("history_60_and_examples", lambda: check_history(api, args.program_id, expected))

    def with_profiles(check):
        if profiles is None:
            raise NotTested("HR profiles unavailable or failed history check")
        return check()

    stage("evidence", lambda: with_profiles(lambda: check_evidence(profiles, expected)))
    stage("current_status", lambda: with_profiles(
        lambda: check_current(profiles, queue[-1] if queue else [])
    ))
    if args.ingest_program_id:
        stage("ingest_replay_conflict_and_missing", lambda: check_ingest(api, args.ingest_program_id))
    else:
        print("NOT TESTED ingest_replay_conflict_and_missing: no --ingest-program-id")
    return 1 if 1 in codes else max(codes)


if __name__ == "__main__":
    raise SystemExit(main())
