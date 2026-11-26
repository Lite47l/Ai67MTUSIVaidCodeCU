package pulse

import (
	"context"
	"strings"
	"testing"
)

func TestManualEnrollmentKeepsTrustedAutomaticRegistration(t *testing.T) {
	s := testStore(t)
	h := (&Server{Store: s}).Handler(testAssets)
	hr := loginAs(t, s, h, "manual-hr", "hr", "")
	mentor := loginAs(t, s, h, "manual-mentor", "mentor", "Н-001")
	person := Person{"СТ-MANUAL", "IT", "Н-001"}
	if w := request(t, h, "POST", "/api/v1/hr/enrollments", person, mentor); w.Code != 403 {
		t.Fatal("non-HR enrolled", w.Code)
	}
	if w := request(t, h, "POST", "/api/v1/hr/enrollments", Person{}, hr); w.Code != 422 {
		t.Fatal("empty person", w.Code)
	}
	if w := request(t, h, "POST", "/api/v1/hr/enrollments", person, hr); w.Code != 201 {
		t.Fatal(w.Code, w.Body)
	}
	if w := request(t, h, "POST", "/api/v1/hr/enrollments", Person{person.ID, "Other", "Н-002"}, hr); w.Code != 409 {
		t.Fatal("duplicate overwrote binding", w.Code)
	}
	row := fixture(t)[0]
	row.InternID = person.ID
	row.Department = "IT"
	row.EventID = "manual-first-response"
	if _, err := s.ImportTrusted(context.Background(), []Response{row}); err != nil {
		t.Fatal(err)
	}
	row.InternID = "СТ-AUTOMATIC"
	row.EventID = "auto-after-manual"
	if _, err := s.ImportTrusted(context.Background(), []Response{row}); err != nil {
		t.Fatal(err)
	}
	original, err := s.Person(context.Background(), person.ID)
	if err != nil || original != person {
		t.Fatal("manual binding changed", original, err)
	}
	if _, err := s.Person(context.Background(), row.InternID); err != nil {
		t.Fatal("automatic registration disabled", err)
	}
}

func TestDevelopmentRoutingAndTaskConfiguration(t *testing.T) {
	for _, text := range []string{"Хочу более сложные задачи.", "Хотелось бы больше самостоятельности."} {
		if !asksForDevelopment(map[string]string{"barriers": text}) {
			t.Fatal("growth request missed", text)
		}
	}
	if asksForDevelopment(map[string]string{"barriers": "Не хочу более сложные задачи."}) {
		t.Fatal("negation became growth request")
	}
	for _, raw := range []string{"http://external.example/tasks", "https://user:password@example.test/tasks", "file:///tmp/tasks"} {
		if _, err := NewHRTaskClient(raw, ""); err == nil {
			t.Fatal("unsafe endpoint", raw)
		}
	}
	s := testStore(t)
	if err := s.UpgradeHRTasks(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := s.UpgradeHRTasks(context.Background()); err != nil {
		t.Fatal("non-idempotent migration", err)
	}
	var count int
	if err := s.DB.QueryRow("SELECT count(*) FROM sqlite_master WHERE name='hr_task_deliveries'").Scan(&count); err != nil || count != 1 {
		t.Fatal("missing task schema", err)
	}
	h := (&Server{Store: s}).Handler(testAssets)
	hr := loginAs(t, s, h, "no-service-hr", "hr", "")
	w := request(t, h, "POST", "/api/v1/hr/interns/СТ-001/recommendations/unknown/confirm?month=1", nil, hr)
	if w.Code != 503 || !strings.Contains(w.Body.String(), "HR") {
		t.Fatal("missing service handling", w.Code)
	}
}
