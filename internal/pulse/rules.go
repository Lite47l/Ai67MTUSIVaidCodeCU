package pulse

import (
	"fmt"
	"math"
	"strconv"
)

// These are explainable numeric heuristics, never presented as AI output.
// Questionnaires measure different concepts; only clarity and communication
// are compared as proxies, without subtracting unrelated questionnaire averages.
func Rules(rs []Response, month int) []Signal {
	out := []Signal{}
	by := map[int]map[string]Response{}
	for _, r := range rs {
		if by[r.Month] == nil {
			by[r.Month] = map[string]Response{}
		}
		by[r.Month][r.Source] = r
	}
	ev := func(r Response, k string) Evidence { return Evidence{r.EventID, k, strconv.Itoa(*r.Scores[k])} }
	for _, src := range []string{"intern", "mentor"} {
		cur, ok := by[month][src]
		if !ok {
			continue
		}
		prev, had := by[month-1][src]
		for _, k := range Fields(src) {
			v := cur.Scores[k]
			if v == nil {
				continue
			}
			if *v <= 3 {
				severity := "attention"
				if *v <= 2 {
					severity = "critical"
				}
				out = append(out, Signal{fmt.Sprintf("low:%s:%d:%s", src, month, k), "low_score", severity, Labels[k] + fmt.Sprintf(": %d/5", *v), []Evidence{ev(cur, k)}})
			}
			if had && prev.Scores[k] != nil && *prev.Scores[k]-*v >= 2 {
				out = append(out, Signal{fmt.Sprintf("drop:%s:%d:%s", src, month, k), "decline", "attention", Labels[k] + ": снижение на 2 или более пункта", []Evidence{ev(prev, k), ev(cur, k)}})
			}
			if had && prev.Scores[k] != nil && *prev.Scores[k] <= 3 && *v <= 3 {
				out = append(out, Signal{fmt.Sprintf("repeat:%s:%d:%s", src, month, k), "repeated_low", "attention", Labels[k] + ": низкая оценка два месяца подряд", []Evidence{ev(prev, k), ev(cur, k)}})
			}
		}
	}
	a, ao := by[month]["intern"]
	b, bo := by[month]["mentor"]
	if ao && bo {
		for _, pair := range [][2]string{{"taskClarity", "taskUnderstanding"}, {"teamwork", "communication"}} {
			x, y := a.Scores[pair[0]], b.Scores[pair[1]]
			if x != nil && y != nil && (*x-*y >= 2 || *y-*x >= 2) {
				out = append(out, Signal{fmt.Sprintf("gap:%d:%s", month, pair[0]), "source_gap", "attention", "Расхождение по близким показателям: " + Labels[pair[0]], []Evidence{ev(a, pair[0]), ev(b, pair[1])}})
			}
		}
	}
	return out
}
func status(signals []Signal) string {
	s := "positive"
	for _, v := range signals {
		if v.Severity == "critical" {
			return "critical"
		}
		if v.Severity == "attention" {
			s = "attention"
		}
	}
	return s
}
func profileStatus(rs []Response, month int, signals []Signal) string {
	st := status(signals)
	if st != "positive" {
		return st
	}
	sources := map[string]bool{}
	for _, r := range rs {
		if r.Month != month {
			continue
		}
		sources[r.Source] = true
		for _, v := range r.Scores {
			if v == nil {
				return "missing"
			}
		}
	}
	if !sources["intern"] || !sources["mentor"] {
		return "missing"
	}
	return st
}
func Average(r Response) *float64 {
	return SummarizeScores(r).Average
}

type ScoreValue struct {
	Field string `json:"field"`
	Label string `json:"label"`
	Value int    `json:"value"`
}
type ScoreSummary struct {
	Average       *float64     `json:"average"`
	Answered      int          `json:"answered"`
	Expected      int          `json:"expected"`
	Minimum       *int         `json:"minimum"`
	LowCount      int          `json:"lowCount"`
	CriticalCount int          `json:"criticalCount"`
	LowScores     []ScoreValue `json:"lowScores"`
}

// Average only the questionnaire's known, present scores. Missing/invalid
// entries do not enter either numerator or denominator. Round once for display.
func SummarizeScores(r Response) ScoreSummary {
	out := ScoreSummary{LowScores: []ScoreValue{}}
	if r.Source != "intern" && r.Source != "mentor" {
		return out
	}
	fields := Fields(r.Source)
	out.Expected = len(fields)
	sum := 0
	for _, field := range fields {
		value := r.Scores[field]
		if value == nil || *value < 1 || *value > 5 {
			continue
		}
		out.Answered++
		sum += *value
		if out.Minimum == nil || *value < *out.Minimum {
			v := *value
			out.Minimum = &v
		}
		if *value <= 3 {
			out.LowCount++
			out.LowScores = append(out.LowScores, ScoreValue{field, Labels[field], *value})
		}
		if *value <= 2 {
			out.CriticalCount++
		}
	}
	if out.Answered > 0 {
		value := math.Round(float64(sum)*100/float64(out.Answered)) / 100
		out.Average = &value
	}
	return out
}

func ScoresForMonth(rs []Response, month int) (ScoreSummary, ScoreSummary) {
	intern := SummarizeScores(Response{Source: "intern"})
	mentor := SummarizeScores(Response{Source: "mentor"})
	for _, r := range rs {
		if r.Month != month {
			continue
		}
		if r.Source == "intern" {
			intern = SummarizeScores(r)
		} else if r.Source == "mentor" {
			mentor = SummarizeScores(r)
		}
	}
	return intern, mentor
}
