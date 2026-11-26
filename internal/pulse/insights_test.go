package pulse

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
)

func enrichedAnalysis(t *testing.T, s *Store) (Analysis, AgentRequest) {
	t.Helper()
	rs, err := s.Responses(context.Background(), "СТ-004")
	if err != nil {
		t.Fatal(err)
	}
	var row Response
	for _, v := range rs {
		if v.Source == "intern" && v.Month == 3 {
			row = v
		}
	}
	req := AgentRequest{SchemaVersion: "1.0", RequestID: "enriched-test", Revision: Revision(rs), ProgramID: Program, InternID: "СТ-004", History: rs}
	tpl := conversationTemplates["reduce_load"]
	a := Analysis{SchemaVersion: "1.0", RequestID: req.RequestID, Revision: req.Revision, Summary: "Существующее резюме Qwen", Recommendations: []string{}, Signals: []Signal{
		{ID: "hidden-test", Kind: "text_score_mismatch", Severity: "attention", Title: "Скрытая нагрузка, месяц 3", Evidence: []Evidence{{EventID: row.EventID, Field: "workload", Value: "4"}, {EventID: row.EventID, Field: "difficulties", Value: row.Comments["difficulties"]}}},
		{ID: "regular-test", Kind: "observation", Severity: "positive", Title: "Обычный анализ Qwen", Evidence: []Evidence{{EventID: row.EventID, Field: "interest", Value: "5"}}},
	}, ConversationPlans: []ConversationPlan{{Month: 3, Situation: tpl.Situation, EvidenceIDs: []string{row.EventID + "#workload"}, Questions: append([]string{}, tpl.Questions...), ActionType: "reduce_load", MentorMessage: tpl.Message}}}
	return a, req
}
func TestHiddenSignalsAndConversationValidation(t *testing.T) {
	s := testStore(t)
	seed(t, s)
	a, req := enrichedAnalysis(t, s)
	if err := ValidateAnalysis(a, req); err != nil {
		t.Fatal(err)
	}
	cases := []string{"wrong_month", "unknown_ref", "duplicate_question", "unsafe_message", "critical_hidden", "cross_response", "low_score"}
	for _, name := range cases {
		t.Run(name, func(t *testing.T) {
			a, req := enrichedAnalysis(t, s)
			switch name {
			case "wrong_month":
				a.ConversationPlans[0].Month = 2
			case "unknown_ref":
				a.ConversationPlans[0].EvidenceIDs = []string{"invented#workload"}
			case "duplicate_question":
				a.ConversationPlans[0].Questions[1] = a.ConversationPlans[0].Questions[0]
			case "unsafe_message":
				a.ConversationPlans[0].MentorMessage = "Стажер сказал: секретная цитата"
			case "critical_hidden":
				a.Signals[0].Severity = "critical"
			case "cross_response":
				a.Signals[0].Evidence[1].EventID = "history:intern:СТ-004:4"
			case "low_score":
				for i := range req.History {
					if req.History[i].Month == 3 && req.History[i].Source == "intern" {
						n := 3
						req.History[i].Scores["workload"] = &n
					}
				}
				a.Signals[0].Evidence[0].Value = "3"
			}
			if ValidateAnalysis(a, req) == nil {
				t.Fatal("invalid enrichment accepted")
			}
		})
	}
}
func TestHRFeaturesArePeriodScopedAndPrivate(t *testing.T) {
	s := testStore(t)
	seed(t, s)
	a, req := enrichedAnalysis(t, s)
	if err := ValidateAnalysis(a, req); err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(a)
	if _, err := s.DB.Exec("INSERT INTO analyses VALUES(?,?,?,?)", req.InternID, req.Revision, string(body), stamp()); err != nil {
		t.Fatal(err)
	}
	h := (&Server{Store: s, AgentURL: "http://test-agent"}).Handler(testAssets)
	hr := loginAs(t, s, h, "hr-feature", "hr", "")
	mentor := loginAs(t, s, h, "mentor-feature", "mentor", "Н-004")
	intern := loginAs(t, s, h, "intern-feature", "intern", "СТ-004")
	for _, test := range []struct {
		role   string
		cookie *http.Cookie
	}{{"hr", hr}, {"mentor", mentor}, {"intern", intern}} {
		w := request(t, h, "GET", "/api/v1/"+test.role+"/interns/СТ-004?month=3", nil, test.cookie)
		if w.Code != 200 {
			t.Fatal(w.Code, w.Body.String())
		}
		var d struct {
			AI        *Analysis          `json:"ai"`
			Status    string             `json:"status"`
			Hidden    []Signal           `json:"hiddenSignals"`
			Plans     []ConversationPlan `json:"conversationPlans"`
			Responses []Response         `json:"responses"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &d); err != nil {
			t.Fatal(err)
		}
		if len(d.Responses) != 12 || (test.role == "hr" && (d.AI == nil || d.AI.Summary != a.Summary)) {
			t.Fatal("existing profile behavior lost")
		}
		if test.role == "hr" {
			if len(d.Hidden) != 1 || len(d.Plans) != 1 || d.Status != "attention" {
				t.Fatal("HR enrichment missing", d)
			}
		} else {
			if len(d.Hidden) != 0 || len(d.Plans) != 0 || d.AI != nil {
				t.Fatal("staff-only analysis leaked")
			}
		}
	}
	for _, month := range []string{"3", "5"} {
		w := request(t, h, "GET", "/api/v1/hr/overview?month="+month, nil, hr)
		var d struct {
			Items []struct {
				Person Person `json:"person"`
				Status string `json:"status"`
			} `json:"items"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &d); err != nil {
			t.Fatal(err)
		}
		for _, item := range d.Items {
			if item.Person.ID == "СТ-004" {
				want := "positive"
				if month == "3" {
					want = "attention"
				}
				if item.Status != want {
					t.Fatal("wrong-month hidden signal", month, item.Status)
				}
			}
		}
	}
}
func TestFallbackPlanWithoutAgent(t *testing.T) {
	s := testStore(t)
	seed(t, s)
	rs, _ := s.Responses(context.Background(), "СТ-005")
	plans := conversationPlansForMonth(nil, rs, 1)
	if len(plans) != 1 || plans[0].ActionType != "weekly_1on1" || len(plans[0].Questions) != 3 {
		t.Fatal(plans)
	}
	index := map[string]Response{}
	for _, r := range rs {
		index[r.EventID] = r
	}
	if err := validateConversationPlans(plans, index); err != nil {
		t.Fatal(err)
	}
	if len(conversationPlansForMonth(nil, rs, 6)) != 0 {
		t.Fatal("unnecessary plan on positive scores")
	}
}
func TestExplicitAnalysisRefreshPreservesHistory(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	seed(t, s)
	job, err := s.Queue(ctx, "СТ-004")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.DB.Exec("UPDATE jobs SET state='done' WHERE id=?", job); err != nil {
		t.Fatal(err)
	}
	fresh, err := s.RequestAnalysis(ctx, "СТ-004")
	if err != nil || fresh == job {
		t.Fatal("completed analysis not refreshed", err)
	}
	same, err := s.RequestAnalysis(ctx, "СТ-004")
	if err != nil || same != fresh {
		t.Fatal("active refresh duplicated", err)
	}
	duplicate, err := s.Queue(ctx, "СТ-004")
	if err != nil || duplicate != fresh {
		t.Fatal("ingestion did not reuse real job ID", err)
	}
	rs, _ := s.Responses(ctx, "")
	if len(rs) != 60 {
		t.Fatal("refresh changed history")
	}
}
