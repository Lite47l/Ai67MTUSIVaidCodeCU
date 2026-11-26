package pulse

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

const qaIntern = "СТ-001"
const privateComment = "Личное сообщение о перегрузке только для HR"

func recommendationHistory(t *testing.T, s *Store) {
	t.Helper()
	var history []Response
	for month := 1; month <= 2; month++ {
		for _, source := range []string{"intern", "mentor"} {
			r := Response{
				EventID:   source + "-recommendations-" + string(rune('0'+month)),
				ProgramID: Program, InternID: qaIntern, Department: "Технический блок",
				Month: month, Source: source, Scores: map[string]*int{}, Comments: map[string]string{},
			}
			for _, field := range Fields(source) {
				value := 5
				r.Scores[field] = &value
			}
			for _, field := range CommentFields(source) {
				r.Comments[field] = ""
			}
			if source == "mentor" {
				r.MentorID = "Н-001"
			} else if month == 1 {
				r.Comments["barriers"] = "Хочу более сложные задачи."
			} else {
				r.Comments["difficulties"] = privateComment
				*r.Scores["workload"] = 2
			}
			history = append(history, r)
		}
	}
	if _, err := s.Import(context.Background(), history); err != nil {
		t.Fatal(err)
	}
}

func TestRecommendationsAreRoutedByRoleAndMonth(t *testing.T) {
	s := testStore(t)
	recommendationHistory(t, s)
	h := (&Server{Store: s}).Handler(testAssets)
	hr := loginAs(t, s, h, "hr", "hr", "")
	mentor := loginAs(t, s, h, "mentor", "mentor", "Н-001")
	stranger := loginAs(t, s, h, "other-mentor", "mentor", "Н-002")
	intern := loginAs(t, s, h, "intern", "intern", qaIntern)

	first := request(t, h, "GET", "/api/v1/hr/interns/"+qaIntern+"?month=1", nil, hr)
	if first.Code != 200 || !strings.Contains(first.Body.String(), `"actionType":"increase_task_complexity"`) ||
		!strings.Contains(first.Body.String(), `"audience":"hr"`) || !strings.Contains(first.Body.String(), `"status":"positive"`) {
		t.Fatalf("HR growth request: %d %s", first.Code, first.Body)
	}
	first = request(t, h, "GET", "/api/v1/intern/interns/"+qaIntern+"?month=1", nil, intern)
	if first.Code != 200 || strings.Contains(first.Body.String(), "increase_task_complexity") ||
		strings.Contains(first.Body.String(), `"recommendations"`) {
		t.Fatalf("intern received a staff recommendation: %d %s", first.Code, first.Body)
	}
	first = request(t, h, "GET", "/api/v1/mentor/interns/"+qaIntern+"?month=1", nil, mentor)
	if first.Code != 200 || !strings.Contains(first.Body.String(), `"recommendations":[]`) {
		t.Fatalf("mentor received a growth action owned by HR: %d %s", first.Code, first.Body)
	}
	path := "/api/v1/mentor/interns/" + qaIntern + "?month=2"
	second := request(t, h, "GET", path, nil, mentor)
	if second.Code != 200 || !strings.Contains(second.Body.String(), `"actionType":"reduce_load"`) ||
		!strings.Contains(second.Body.String(), `"audience":"mentor"`) || strings.Contains(second.Body.String(), privateComment) ||
		!strings.Contains(second.Body.String(), `"ai":null`) {
		t.Fatalf("mentor workload recommendation: %d %s", second.Code, second.Body)
	}
	if other := request(t, h, "GET", path, nil, stranger); other.Code != 403 {
		t.Fatalf("unassigned mentor: %d", other.Code)
	}
	if next := request(t, h, "GET", "/api/v1/mentor/interns/"+qaIntern+"?month=3", nil, mentor); !strings.Contains(next.Body.String(), `"recommendations":[]`) {
		t.Fatal("previous recommendation carried over to another month")
	}
}

func TestConfirmedHRTaskRetriesWithSameExternalKey(t *testing.T) {
	s := testStore(t)
	recommendationHistory(t, s)
	var mu sync.Mutex
	var attempts int
	jobs := map[string]string{}
	external := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
			return
		}
		var task hrTask
		key := r.Header.Get("Idempotency-Key")
		if json.Unmarshal(data, &task) != nil || key == "" || task.ID != key ||
			task.AssigneeRole != "mentor" || task.Assignee != "Н-001" || task.Month != 2 ||
			task.Action != "reduce_load" || strings.Contains(string(data), privateComment) ||
			r.Header.Get("Authorization") != "Bearer synthetic-test-token" {
			t.Errorf("unexpected external request")
			http.Error(w, "invalid", 422)
			return
		}
		mu.Lock()
		defer mu.Unlock()
		attempts++
		if original, exists := jobs[key]; exists && original != string(data) {
			t.Error("retry changed the original payload")
		}
		jobs[key] = string(data)
		if attempts == 1 {
			// The task exists even though the first response indicates a failure.
			http.Error(w, "retry later", 503)
			return
		}
		writeJSON(w, 200, map[string]string{"id": "TASK-1", "url": "https://tasks.example.test/TASK-1"})
	}))
	defer external.Close()
	client, err := NewHRTaskClient(external.URL, "synthetic-test-token")
	if err != nil {
		t.Fatal(err)
	}
	h := (&Server{Store: s, HRTasks: client}).Handler(testAssets)
	hr := loginAs(t, s, h, "hr", "hr", "")
	otherHR := loginAs(t, s, h, "other-hr", "hr", "")
	mentor := loginAs(t, s, h, "mentor", "mentor", "Н-001")
	profile := "/api/v1/hr/interns/" + qaIntern
	month := "?month=2"
	var body struct{ Recommendations []Recommendation }
	before := request(t, h, "GET", profile+month, nil, hr)
	if err := json.Unmarshal(before.Body.Bytes(), &body); err != nil || len(body.Recommendations) != 1 {
		t.Fatalf("expected the workload recommendation: %d %s", before.Code, before.Body)
	}
	confirm := profile + "/recommendations/" + body.Recommendations[0].ID + "/confirm"
	if got := request(t, h, "POST", confirm+month, nil, mentor); got.Code != 403 {
		t.Fatalf("mentor was able to create a task: %d", got.Code)
	}
	if got := request(t, h, "POST", confirm+"?month=1", nil, hr); got.Code != 404 {
		t.Fatalf("confirmation accepted for a different month: %d", got.Code)
	}
	if got := request(t, h, "POST", confirm+month, nil, hr); got.Code != 502 || !strings.Contains(got.Body.String(), `"state":"failed"`) {
		t.Fatalf("delivery failure: %d %s", got.Code, got.Body)
	}
	if got := request(t, h, "GET", profile+month, nil, hr); !strings.Contains(got.Body.String(), `"state":"failed"`) {
		t.Fatal("delivery error was not persisted")
	}
	if got := request(t, h, "POST", confirm+month, nil, otherHR); got.Code != 200 ||
		!strings.Contains(got.Body.String(), `"state":"sent"`) || !strings.Contains(got.Body.String(), "TASK-1") {
		t.Fatalf("retry: %d %s", got.Code, got.Body)
	}
	if got := request(t, h, "POST", confirm+month, nil, hr); got.Code != 200 {
		t.Fatalf("repeat after success: %d %s", got.Code, got.Body)
	}
	mu.Lock()
	defer mu.Unlock()
	if attempts != 2 || len(jobs) != 1 {
		t.Fatalf("expected two attempts and one task, got %d attempts and %d tasks", attempts, len(jobs))
	}
}
