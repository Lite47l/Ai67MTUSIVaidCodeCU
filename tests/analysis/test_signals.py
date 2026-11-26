import json
import unittest
from pathlib import Path

from analysis import analyze_signals

PROGRAM = "demo-a01"
INTERN = "DEMO-001"


def response(period, source="intern", *, scores=None, comments=None, program_id=PROGRAM, intern_id=INTERN):
    return {
        "period": period,
        "source": source,
        "program_id": program_id,
        "intern_id": intern_id,
        "scores": scores or {},
        "comments": comments or {},
    }


def analyze(history, period=None):
    return analyze_signals(history, program_id=PROGRAM, intern_id=INTERN, period=period)


class AnalyzeSignalsTest(unittest.TestCase):
    def test_reference_json_matches_api_contract(self):
        fixture_path = Path(__file__).parent / "fixtures" / "low_workload.json"
        fixture = json.loads(fixture_path.read_text(encoding="utf-8"))
        signals = analyze_signals(
            fixture["responses"],
            program_id=fixture["program_id"],
            intern_id=fixture["intern_id"],
            period=fixture["period"],
        )
        self.assertEqual(signals, fixture["expected_signals"])
        json.dumps(signals)

    def test_administrative_scenario_st_003_recovers_after_month_three(self):
        history = [
            response(1, scores={"task_clarity": 3, "mentor_support": 3}),
            response(2, scores={"task_clarity": 3, "mentor_support": 2}),
            response(3, scores={"task_clarity": 2, "mentor_support": 2}),
            response(4, scores={"task_clarity": 4, "mentor_support": 4}),
            response(6, scores={"task_clarity": 5, "mentor_support": 4}),
        ]
        third = analyze(history, 3)
        self.assertEqual(
            {signal["evidence"][0]["field"] for signal in third if signal["status"] == "critical"},
            {"task_clarity", "mentor_support"},
        )
        repetitions = [signal for signal in third if signal["kind"] == "repeated_issue"]
        self.assertEqual(len(repetitions), 2)
        for signal in repetitions:
            self.assertEqual({item["period"] for item in signal["evidence"]}, {2, 3})
        self.assertEqual(analyze(history, 4), [])
        self.assertEqual(analyze(history, 6), [])
        self.assertTrue(any(signal["period"] == 3 for signal in analyze(history)))

    def test_hr_scenario_st_004_low_workload_survives_high_other_scores(self):
        history = [
            response(3, scores={"workload_comfort": 4}),
            response(
                4,
                scores={"workload_comfort": 2, "task_clarity": 5, "professional_growth": 5},
                comments={"period_difficulties": "Нагрузка была высокой, других проблем нет."},
            ),
            response(4, "mentor", scores={"work_quality": 5}),
            response(
                5,
                scores={"workload_comfort": 4},
                comments={"period_difficulties": "Нагрузка вернулась к комфортному уровню."},
            ),
        ]
        fourth = analyze(history, 4)
        low = next(signal for signal in fourth if signal["kind"] == "low_score")
        self.assertEqual(low["status"], "critical")
        self.assertEqual(low["evidence"][0]["field"], "workload_comfort")
        change = next(signal for signal in fourth if signal["kind"] == "score_drop")
        self.assertEqual([(e["period"], e["value"]) for e in change["evidence"]], [(3, 4), (4, 2)])
        text = next(signal for signal in fourth if signal["kind"] == "text_issue")
        self.assertEqual(text["evidence"][0]["quote"], "Нагрузка была высокой")
        self.assertFalse(any(signal["kind"] == "score_disagreement" for signal in fourth))
        self.assertEqual(analyze(history, 5), [])

    def test_it_scenario_st_005_detects_repeated_feedback_in_different_wording(self):
        history = [
            response(1, comments={"work_barriers": "Недостаточно обратной связи по моей работе."}),
            response(2, "mentor", comments={"attention_situations": "Нужна регулярная связь с наставником."}),
            response(3, comments={"work_barriers": "Нет обратной связи о результатах задач."}),
            response(4, comments={"work_barriers": "Обратная связь стала регулярнее."}),
        ]
        for month in (2, 3):
            with self.subTest(month=month):
                signals = analyze(history, month)
                self.assertEqual([signal["kind"] for signal in signals], ["repeated_issue"])
                for item in signals[0]["evidence"]:
                    original = next(
                        record["comments"][item["field"]]
                        for record in history
                        if record["period"] == item["period"] and record["source"] == item["source"]
                    )
                    self.assertIn(item["quote"], original)
                self.assertEqual({item["source"] for item in signals[0]["evidence"]}, {"intern", "mentor"})
        self.assertEqual(analyze(history, 4), [])

    def test_disagreement_uses_both_roles_and_stays_at_attention(self):
        history = [
            response(2, scores={"task_clarity": 2, "team_interaction": 2, "workload_comfort": 2}),
            response(2, "mentor", scores={"task_understanding": 4, "mentor_team_interaction": 5, "work_quality": 5}),
        ]
        disagreements = [signal for signal in analyze(history, 2) if signal["kind"] == "score_disagreement"]
        self.assertEqual(len(disagreements), 1)
        self.assertEqual(disagreements[0]["status"], "attention")
        self.assertEqual(
            [(e["source"], e["field"], e["value"]) for e in disagreements[0]["evidence"]],
            [("intern", "task_clarity", 2), ("mentor", "task_understanding", 4)],
        )

    def test_contextual_team_pair_requires_supporting_comment(self):
        history = [
            response(2, scores={"team_interaction": 3}, comments={"work_barriers": "Мне некомфортно общаться с командой."}),
            response(2, "mentor", scores={"mentor_team_interaction": 5}),
        ]
        discrepancy = next(signal for signal in analyze(history, 2) if signal["kind"] == "score_disagreement")
        self.assertEqual(discrepancy["status"], "attention")
        self.assertEqual(len(discrepancy["evidence"]), 3)
        self.assertEqual(discrepancy["evidence"][-1]["field"], "work_barriers")

    def test_unanswered_or_incomplete_forms_do_not_become_low_scores(self):
        history = [
            response(1, scores={"task_clarity": 5}),
            response(2, scores={"task_clarity": None, "workload_comfort": 2}),
            response(3, "mentor", scores={"task_understanding": 4}),
        ]
        second = analyze(history, 2)
        self.assertEqual([signal["kind"] for signal in second], ["low_score"])
        self.assertEqual(second[0]["evidence"][0]["field"], "workload_comfort")
        self.assertEqual(analyze(history, 3), [])
        self.assertEqual(analyze(history, 4), [])
        self.assertEqual(analyze([], 2), [])

    def test_score_three_needs_relevant_context(self):
        unconfirmed = response(1, scores={"initiative": 3}, source="mentor")
        self.assertEqual(analyze([unconfirmed], 1), [])
        confirmed = [
            response(1, scores={"task_clarity": 4}),
            response(2, scores={"task_clarity": 3, "task_interest": 3}),
        ]
        only = analyze(confirmed, 2)
        self.assertEqual([signal["kind"] for signal in only], ["borderline_score"])
        self.assertEqual({item["field"] for item in only[0]["evidence"]}, {"task_clarity"})
        with_text = response(
            1, scores={"task_clarity": 3, "task_interest": 3},
            comments={"work_barriers": "Мне не понятны приоритеты по задачам."},
        )
        borderline = next(signal for signal in analyze([with_text]) if signal["kind"] == "borderline_score")
        self.assertEqual(borderline["evidence"][1]["field"], "work_barriers")

    def test_negation_and_recovery_are_neutral(self):
        for comment in (
            "Сложностей за период не было.",
            "Ничего не мешает выполнять задачи.",
            "Проблем нет.",
            "Не было перегрузки.",
            "Нет высокой нагрузки.",
            "Нагрузка не была высокой.",
            "Нагрузка вернулась к комфортному уровню.",
            "Задачи стали понятнее.",
        ):
            with self.subTest(comment=comment):
                self.assertEqual(analyze([response(1, comments={"period_difficulties": comment})]), [])
        continued = response(1, comments={"work_barriers": "Нагрузка не вернулась к комфортному уровню."})
        self.assertEqual([signal["kind"] for signal in analyze([continued])], ["text_issue"])

    def test_repeat_and_drop_require_adjacent_months(self):
        history = [
            response(1, scores={"workload_comfort": 5}, comments={"work_barriers": "Нагрузка была высокой."}),
            response(3, scores={"workload_comfort": 3}, comments={"work_barriers": "Нагрузка выше комфортной."}),
        ]
        self.assertEqual([signal["kind"] for signal in analyze(history, 3)], ["borderline_score", "text_issue"])

    def test_scope_and_order_are_deterministic(self):
        relevant = [
            response(2, scores={"task_clarity": 1}),
            response(2, "mentor", scores={"task_understanding": 5}),
            response(5, scores={"task_clarity": 1}),
        ]
        unrelated = [
            response(2, scores={"workload_comfort": 1}, program_id="second-program"),
            response(2, "mentor", scores={"work_quality": 1}, intern_id="other-intern"),
        ]
        history = relevant + unrelated
        signals = analyze(history)
        self.assertEqual(signals, analyze(reversed(history)))
        self.assertEqual(signals, analyze(relevant))
        self.assertEqual(analyze(history, 2), [signal for signal in signals if signal["period"] == 2])
        self.assertEqual(len({signal["id"] for signal in signals}), len(signals))
        self.assertNotEqual(
            signals[0]["id"],
            analyze_signals(unrelated, program_id="second-program", intern_id=INTERN)[0]["id"],
        )

    def test_duplicate_canonical_response_requires_backend_to_choose_version(self):
        history = [response(1, scores={"task_clarity": 4}), response(1, scores={"task_clarity": 2})]
        with self.assertRaisesRegex(ValueError, "multiple canonical responses"):
            analyze(history)

    def test_zero_and_invalid_scores_are_rejected(self):
        for invalid in (0, 6, True, "2", 2.5):
            with self.subTest(invalid=invalid):
                with self.assertRaisesRegex(ValueError, "intern.task_clarity.*from 1 to 5"):
                    analyze([response(1, scores={"task_clarity": invalid})])

    def test_new_response_changes_only_current_month(self):
        history = [response(3, scores={"workload_comfort": 4})]
        self.assertEqual(analyze(history, 4), [])
        history.append(response(4, scores={"workload_comfort": 2}))
        self.assertEqual([signal["kind"] for signal in analyze(history, 4)], ["low_score", "score_drop"])
        self.assertEqual(analyze(history, 3), [])

    def test_month_seven_is_outside_the_contract(self):
        with self.assertRaisesRegex(ValueError, "from 1 to 6"):
            analyze([], 7)
        with self.assertRaisesRegex(ValueError, "from 1 to 6"):
            analyze([response(7)])
