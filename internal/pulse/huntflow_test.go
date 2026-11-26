package pulse

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
)

func TestHuntflowBindingAndRoleAccess(t *testing.T) {
	s := testStore(t)
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer synthetic-huntflow-token" {
			t.Error("missing token")
		}
		if r.URL.Path == "/v2/accounts" {
			writeJSON(w, 200, map[string]any{"items": []any{map[string]any{"id": 7, "name": "Test organization"}}})
			return
		}
		if r.URL.Path == "/v2/accounts/7/applicants/42" {
			writeJSON(w, 200, map[string]any{"id": 42})
			return
		}
		http.NotFound(w, r)
	}))
	defer fake.Close()
	c, err := NewHuntflowClient(fake.URL+"/v2", "synthetic-huntflow-token", "7", s)
	if err != nil {
		t.Fatal(err)
	}
	h := (&Server{Store: s, Huntflow: c}).Handler(testAssets)
	hr := loginAs(t, s, h, "hf-hr", "hr", "")
	mentor := loginAs(t, s, h, "hf-mentor", "mentor", "Н-001")
	if w := request(t, h, "GET", "/api/v1/hr/integrations/huntflow", nil, mentor); w.Code != 403 {
		t.Fatal("role access", w.Code)
	}
	if w := request(t, h, "GET", "/api/v1/hr/integrations/huntflow", nil, hr); w.Code != 200 || !strings.Contains(w.Body.String(), "Test organization") || strings.Contains(w.Body.String(), c.token) {
		t.Fatal(w.Code, w.Body)
	}
	path := "/api/v1/hr/integrations/huntflow/participants"
	input := map[string]any{"applicantId": 42, "department": "IT", "mentorId": "Н-001"}
	if w := request(t, h, "POST", path, input, mentor); w.Code != 403 {
		t.Fatal("mentor imported candidate", w.Code)
	}
	for range 2 {
		if w := request(t, h, "POST", path, input, hr); w.Code != 200 {
			t.Fatal("bind/replay", w.Code, w.Body)
		}
	}
	p, err := s.Person(context.Background(), "HF-42")
	if err != nil || p.Department != "IT" || p.MentorID != "Н-001" {
		t.Fatal(p, err)
	}
	input["internId"] = "HF-DUPLICATE"
	if w := request(t, h, "POST", path, input, hr); w.Code != 409 {
		t.Fatal("duplicate candidate mapping", w.Code, w.Body)
	}
	if _, err := s.Person(context.Background(), "HF-DUPLICATE"); err == nil {
		t.Fatal("partial enrollment committed")
	}
	input["internId"] = "HF-42"
	input["mentorId"] = "Н-002"
	if w := request(t, h, "POST", path, input, hr); w.Code != 409 {
		t.Fatal("mentor changed", w.Code)
	}
	input["internId"] = "HF-MISSING"
	input["applicantId"] = 404
	if w := request(t, h, "POST", path, input, hr); w.Code != 502 {
		t.Fatal("unknown applicant accepted", w.Code)
	}
	if _, err := s.Person(context.Background(), "HF-MISSING"); err == nil {
		t.Fatal("missing applicant created person")
	}
}

func TestHuntflowLostResponseReconcilesAcrossPages(t *testing.T) {
	s := testStore(t)
	recommendationHistory(t, s)
	_, err := s.DB.Exec("INSERT INTO huntflow_bindings VALUES(?,?,?,?,?)", qaIntern, 7, 42, "hr", stamp())
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var saved string
	posts := 0
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if r.Header.Get("Authorization") != "Bearer test-token" {
			t.Error("authorization missing")
		}
		if r.URL.Path != "/v2/accounts/7/applicants/42/logs" {
			t.Error("wrong API path", r.URL.Path)
			http.NotFound(w, r)
			return
		}
		if r.Method == "GET" {
			if r.URL.Query().Get("personal") != "true" || r.URL.Query().Get("count") != "100" {
				t.Error("wrong filter")
			}
			page, _ := strconv.Atoi(r.URL.Query().Get("page"))
			items := []any{}
			if page == 2 && saved != "" {
				items = append(items, map[string]any{"id": 91, "comment": saved})
			}
			writeJSON(w, 200, map[string]any{"page": page, "total_pages": 2, "items": items})
			return
		}
		var body map[string]any
		if json.NewDecoder(r.Body).Decode(&body) != nil {
			t.Error("invalid body")
		}
		if len(body) != 1 {
			t.Error("unexpected external fields", body)
		}
		comment, _ := body["comment"].(string)
		if strings.Contains(comment, privateComment) || !strings.Contains(comment, "mentor (Н-001)") || !strings.Contains(comment, "[mgts-pulse:huntflow:") {
			t.Error("wrong comment payload")
		}
		posts++
		saved = comment
		// The upstream persisted a comment but the response failed.
		http.Error(w, "synthetic upstream failure", 503)
	}))
	defer fake.Close()
	c, _ := NewHuntflowClient(fake.URL+"/v2", "test-token", "7", s)
	h := (&Server{Store: s, Huntflow: c}).Handler(testAssets)
	hr := loginAs(t, s, h, "hf-confirm", "hr", "")
	otherHR := loginAs(t, s, h, "hf-other", "hr", "")
	intern := loginAs(t, s, h, "hf-intern", "intern", qaIntern)
	var body struct {
		Recommendations []Recommendation
		Huntflow        *HuntflowBinding
	}
	profile := "/api/v1/hr/interns/" + qaIntern + "?month=2"
	w := request(t, h, "GET", profile, nil, hr)
	if json.Unmarshal(w.Body.Bytes(), &body) != nil || body.Huntflow == nil || len(body.Recommendations) != 1 {
		t.Fatal(w.Code, w.Body)
	}
	path := "/api/v1/hr/interns/" + qaIntern + "/recommendations/" + body.Recommendations[0].ID + "/huntflow?month=2"
	if w := request(t, h, "POST", path, nil, intern); w.Code != 403 {
		t.Fatal("intern exported", w.Code)
	}
	if w := request(t, h, "POST", strings.Replace(path, "month=2", "month=1", 1), nil, hr); w.Code != 404 {
		t.Fatal("wrong month accepted", w.Code)
	}
	if w := request(t, h, "POST", path, nil, hr); w.Code != 502 {
		t.Fatal("lost response", w.Code, w.Body)
	}
	if w := request(t, h, "POST", path, nil, otherHR); w.Code != 200 || !strings.Contains(w.Body.String(), `"externalId":"91"`) {
		t.Fatal("reconcile", w.Code, w.Body)
	}
	if w := request(t, h, "POST", path, nil, hr); w.Code != 200 {
		t.Fatal(w.Code)
	}
	mu.Lock()
	if posts != 1 {
		t.Error("duplicate upstream comment", posts)
	}
	mu.Unlock()
	w = request(t, h, "GET", profile, nil, hr)
	if !strings.Contains(w.Body.String(), `"huntflowDelivery":{"state":"sent"`) {
		t.Fatal("delivery not shown", w.Body)
	}
	w = request(t, h, "GET", "/api/v1/intern/interns/"+qaIntern+"?month=2", nil, intern)
	if strings.Contains(w.Body.String(), "huntflow") {
		t.Fatal("binding leaked to intern")
	}
}

func TestHuntflowErrorsNeverLeakExternalBody(t *testing.T) {
	for _, status := range []int{401, 403, 404, 429, 500} {
		fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Error(w, "SECRET-REMOTE-BODY", status) }))
		c, _ := NewHuntflowClient(fake.URL, "synthetic-token", "7", nil)
		var result any
		err := c.request(context.Background(), "GET", "/accounts", nil, &result)
		fake.Close()
		if err == nil || strings.Contains(err.Error(), "SECRET") {
			t.Fatal("unsafe error", err)
		}
	}
	for _, base := range []string{"http://external.example/v2", "https://user:pass@example.test/v2", "https://api.huntflow.ru/v2?x=y"} {
		if _, err := NewHuntflowClient(base, "token", "7", nil); err == nil {
			t.Fatal("unsafe configuration", base)
		}
	}
}

func TestHuntflowDoesNotPostWhenHistoryIsUnverifiable(t *testing.T) {
	for _, mode := range []string{"unavailable", "too_many_pages", "invalid_json", "redirect"} {
		t.Run(mode, func(t *testing.T) {
			s := testStore(t)
			if _, err := s.DB.Exec("INSERT INTO huntflow_bindings VALUES(?,?,?,?,?)", "СТ-001", 7, 42, "hr", stamp()); err != nil {
				t.Fatal(err)
			}
			posts := 0
			fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == "POST" {
					posts++
					t.Error("posted without verified history")
				}
				switch mode {
				case "unavailable":
					http.Error(w, "unavailable", 503)
				case "too_many_pages":
					writeJSON(w, 200, map[string]any{"page": 1, "total_pages": 101, "items": []any{}})
				case "invalid_json":
					w.Write([]byte("not-json"))
				case "redirect":
					http.Redirect(w, r, "https://example.test/steal", 302)
				}
			}))
			defer fake.Close()
			c, _ := NewHuntflowClient(fake.URL, "token", "7", s)
			if _, err := c.create(context.Background(), hrTask{ID: "test", Intern: "СТ-001"}); err == nil {
				t.Fatal("history error ignored")
			}
			if posts != 0 {
				t.Fatal("POST sent")
			}
		})
	}
}
