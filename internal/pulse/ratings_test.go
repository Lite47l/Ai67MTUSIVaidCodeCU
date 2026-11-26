package pulse

import (
	"encoding/json"
	"testing"
)

func TestScoreSummaryHistoricalAverages(t *testing.T) {
	rs := fixture(t)
	for _, tc := range []struct {
		source                  string
		average                 float64
		answered, low, critical int
	}{{"intern", 4.43, 7, 1, 1}, {"mentor", 4.83, 6, 0, 0}} {
		for _, r := range rs {
			if r.InternID != "СТ-004" || r.Month != 4 || r.Source != tc.source {
				continue
			}
			got := SummarizeScores(r)
			if got.Average == nil || *got.Average != tc.average || got.Answered != tc.answered || got.LowCount != tc.low || got.CriticalCount != tc.critical {
				t.Fatalf("%s: %+v", tc.source, got)
			}
			if tc.source == "intern" && (len(got.LowScores) != 1 || got.LowScores[0].Field != "workload" || got.LowScores[0].Value != 2) {
				t.Fatal("low workload hidden by high average")
			}
		}
	}
}
func TestAverageExcludesMissingInvalidAndUnknownScores(t *testing.T) {
	four, five, zero := 4, 5, 0
	r := Response{Source: "intern", Scores: map[string]*int{"taskClarity": &four, "mentorSupport": &five, "workload": nil, "interest": &zero, "unknown": &zero}}
	got := SummarizeScores(r)
	if got.Average == nil || *got.Average != 4.5 || got.Answered != 2 || got.Expected != 7 || got.Minimum == nil || *got.Minimum != 4 {
		t.Fatal(got)
	}
	r.Scores = map[string]*int{"workload": nil}
	got = SummarizeScores(r)
	if got.Average != nil || got.Minimum != nil || got.Answered != 0 || got.LowCount != 0 {
		t.Fatal("missing scores became zeros", got)
	}
}
func TestSelectedMonthAndSourceAveragesStaySeparate(t *testing.T) {
	s := testStore(t)
	seed(t, s)
	h := (&Server{Store: s}).Handler(testAssets)
	hr := loginAs(t, s, h, "ratings-hr", "hr", "")
	w := request(t, h, "GET", "/api/v1/hr/overview?month=4", nil, hr)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var d struct {
		Items []struct {
			Person Person       `json:"person"`
			IA     *float64     `json:"internAverage"`
			MA     *float64     `json:"mentorAverage"`
			Intern ScoreSummary `json:"internStats"`
			Mentor ScoreSummary `json:"mentorStats"`
		} `json:"items"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &d); err != nil {
		t.Fatal(err)
	}
	for _, item := range d.Items {
		if item.Person.ID == "СТ-004" {
			if item.IA == nil || item.MA == nil || *item.IA != 4.43 || *item.MA != 4.83 || item.Intern.CriticalCount != 1 {
				t.Fatal(item)
			}
		}
	}
	w = request(t, h, "GET", "/api/v1/hr/interns/СТ-004?month=5", nil, hr)
	var profile struct {
		Intern ScoreSummary `json:"internStats"`
		Mentor ScoreSummary `json:"mentorStats"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &profile); err != nil {
		t.Fatal(err)
	}
	if profile.Intern.Average == nil || *profile.Intern.Average != 4.86 || profile.Mentor.Average == nil || *profile.Mentor.Average != 5 || profile.Intern.LowCount != 0 {
		t.Fatal("previous month mixed into current average", profile)
	}
}
func TestDeliveryHistoryIncludesRespondentAndPeriod(t *testing.T) {
	s := testStore(t)
	seed(t, s)
	h := (&Server{Store: s}).Handler(testAssets)
	hr := loginAs(t, s, h, "delivery-hr", "hr", "")
	row := fixture(t)[0]
	if err := s.RecordDelivery(t.Context(), row.EventID, "accepted", "tally"); err != nil {
		t.Fatal(err)
	}
	w := request(t, h, "GET", "/api/v1/hr/integrations", nil, hr)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var d struct {
		Deliveries []struct {
			InternID   string `json:"internId"`
			Month      int    `json:"month"`
			Respondent string `json:"respondent"`
		} `json:"deliveries"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &d); err != nil {
		t.Fatal(err)
	}
	if len(d.Deliveries) != 1 || d.Deliveries[0].InternID != row.InternID || d.Deliveries[0].Month != 1 || d.Deliveries[0].Respondent != "intern" {
		t.Fatal(d)
	}
}
