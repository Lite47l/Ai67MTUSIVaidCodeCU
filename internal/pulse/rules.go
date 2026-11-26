package pulse

import (
	"fmt"
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
	sum, n := 0, 0
	for _, v := range r.Scores {
		if v != nil {
			sum += *v
			n++
		}
	}
	if n == 0 {
		return nil
	}
	x := float64(sum) / float64(n)
	return &x
}
