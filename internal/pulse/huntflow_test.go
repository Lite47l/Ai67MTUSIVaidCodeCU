package pulse

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

func TestHuntflowIsIndependentAndReconcilesUncertainWrite(t *testing.T) {
	store := testStore(t)
	recommendationHistory(t, store)
	var mu sync.Mutex
	var comment string
	postCount, getCount, taskCount := 0, 0, 0
	external := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if r.URL.Path == "/tasks" {
			taskCount++
			writeJSON(w, 201, map[string]string{"id": "HR-TASK-1", "url": "https://tasks.example.test/HR-TASK-1"})
			return
		}
		if r.URL.Path != "/v2/accounts/31/applicants/90/logs" || r.Header.Get("Authorization") != "Bearer huntflow-test-token" {
			t.Error("unexpected Huntflow request")
			http.Error(w, "unexpected", 400)
			return
		}
		if r.Method == http.MethodPost {
			var payload map[string]string
			if json.NewDecoder(r.Body).Decode(&payload) != nil || len(payload) != 1 ||
				!strings.Contains(payload["comment"], "Провести короткий созвон про нагрузку") ||
				strings.Contains(payload["comment"], privateComment) || strings.Contains(payload["comment"], qaIntern) {
				t.Error("Huntflow should receive only a neutral note and its marker")
				http.Error(w, "unexpected", 400)
				return
			}
			postCount++
			comment = payload["comment"]
			// Huntflow saved the note, then the response failed.
			http.Error(w, "bad gateway", 502)
			return
		}
		getCount++
		if r.Method != http.MethodGet || r.URL.Query().Get("type") != "COMMENT" || r.URL.Query().Get("personal") != "true" {
			t.Error("Huntflow lookup must be restricted to personal comments")
		}
		if comment == "" {
			writeJSON(w, 200, map[string]any{"page": 1, "total_pages": 0, "items": []huntflowLog{}})
		} else if r.URL.Query().Get("page") == "1" {
			writeJSON(w, 200, map[string]any{"page": 1, "total_pages": 2, "items": []huntflowLog{{ID: 22, Type: "COMMENT", Comment: "Unrelated"}}})
		} else {
			writeJSON(w, 200, map[string]any{"page": 2, "total_pages": 2, "items": []huntflowLog{{ID: 33, Type: "COMMENT", Comment: comment}}})
		}
	}))
	defer external.Close()
	client, err := NewHuntflowClient(external.URL, "31", "huntflow-test-token", huntflowTestMap)
	if err != nil {
		t.Fatal(err)
	}
	hrTasks, err := NewHRTaskClient(external.URL+"/tasks", "")
	if err != nil {
		t.Fatal(err)
	}
	h := (&Server{Store: store, Huntflow: client, HRTasks: hrTasks}).Handler(testAssets)
	hr := loginAs(t, store, h, "hr", "hr", "")
	mentor := loginAs(t, store, h, "mentor", "mentor", "Н-001")
	profile := "/api/v1/hr/interns/" + qaIntern
	path := profile + "/recommendations/" + huntflowTestRecommendation(t, h, hr)
	if response := request(t, h, "POST", path+"/huntflow?month=2", nil, mentor); response.Code != 403 {
		t.Fatalf("mentor access: %d", response.Code)
	}
	first := request(t, h, "POST", path+"/huntflow?month=2", nil, hr)
	if first.Code != 502 || !strings.Contains(first.Body.String(), `"state":"uncertain"`) {
		t.Fatalf("ambiguous send: %d %s", first.Code, first.Body)
	}
	retry := request(t, h, "POST", path+"/huntflow?month=2", nil, hr)
	if retry.Code != 200 || !strings.Contains(retry.Body.String(), `"externalId":"33"`) {
		t.Fatalf("retry should find the existing note: %d %s", retry.Code, retry.Body)
	}
	already := request(t, h, "POST", path+"/huntflow?month=2", nil, hr)
	if already.Code != 200 || already.Body.String() != retry.Body.String() {
		t.Fatalf("replay changed the result: %d %s", already.Code, already.Body)
	}
	task := request(t, h, "POST", path+"/confirm?month=2", nil, hr)
	if task.Code != 200 || !strings.Contains(task.Body.String(), "HR-TASK-1") {
		t.Fatalf("generic HR service should still be available: %d %s", task.Code, task.Body)
	}
	profileResponse := request(t, h, "GET", profile+"?month=2", nil, hr)
	if !strings.Contains(profileResponse.Body.String(), `"delivery":{"state":"sent","externalId":"HR-TASK-1"`) ||
		!strings.Contains(profileResponse.Body.String(), `"huntflowDelivery":{"state":"sent","externalId":"33"`) {
		t.Fatalf("expected separate delivery states: %s", profileResponse.Body)
	}
	mu.Lock()
	defer mu.Unlock()
	if postCount != 1 || taskCount != 1 || getCount != 3 {
		t.Fatalf("unexpected Huntflow POST/GET and task count: %d/%d/%d", postCount, getCount, taskCount)
	}
}

const huntflowTestMap = `{"СТ-001":{"applicantId":90,"profileUrl":"https://huntflow.example.test/card/90"}}`

func huntflowTestRecommendation(t *testing.T, h http.Handler, cookie *http.Cookie) string {
	t.Helper()
	var profile struct{ Recommendations []Recommendation }
	response := request(t, h, "GET", "/api/v1/hr/interns/"+qaIntern+"?month=2", nil, cookie)
	if json.Unmarshal(response.Body.Bytes(), &profile) != nil || len(profile.Recommendations) != 1 {
		t.Fatalf("recommendation missing: %d %s", response.Code, response.Body)
	}
	return profile.Recommendations[0].ID
}

func TestHuntflowRetryNeverWritesWhenUncertainNoteIsStillMissing(t *testing.T) {
	store := testStore(t)
	recommendationHistory(t, store)
	var mu sync.Mutex
	posts := 0
	external := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			mu.Lock()
			posts++
			mu.Unlock()
			http.Error(w, "lost", 502)
			return
		}
		writeJSON(w, 200, map[string]any{"page": 1, "total_pages": 0, "items": []huntflowLog{}})
	}))
	defer external.Close()
	unbound, err := NewHuntflowClient(external.URL, "31", "token",
		`{"СТ-002":{"applicantId":90,"profileUrl":"https://huntflow.example.test/card/90"}}`)
	if err != nil {
		t.Fatal(err)
	}
	client, err := NewHuntflowClient(external.URL, "31", "token", huntflowTestMap)
	if err != nil {
		t.Fatal(err)
	}
	server := &Server{Store: store, Huntflow: unbound}
	h := server.Handler(testAssets)
	hr := loginAs(t, store, h, "hr", "hr", "")
	id := huntflowTestRecommendation(t, h, hr)
	path := "/api/v1/hr/interns/" + qaIntern + "/recommendations/" + id + "/huntflow?month=2"
	if missing := request(t, h, "POST", path, nil, hr); missing.Code != 409 {
		t.Fatalf("missing mapping: %d %s", missing.Code, missing.Body)
	}
	server.Huntflow = client
	for range 3 {
		result := request(t, h, "POST", path, nil, hr)
		if result.Code != 502 || !strings.Contains(result.Body.String(), `"state":"uncertain"`) {
			t.Fatalf("safe retry: %d %s", result.Code, result.Body)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if posts != 1 {
		t.Fatalf("retry wrote %d times", posts)
	}
}
