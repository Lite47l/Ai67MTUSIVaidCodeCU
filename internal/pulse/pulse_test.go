package pulse

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"testing/fstest"

	"github.com/xuri/excelize/v2"
)

func fixture(t *testing.T) []Response {
	t.Helper()
	b, err := os.ReadFile("../../data/history.json")
	if err != nil {
		t.Fatal(err)
	}
	var rs []Response
	if err = json.Unmarshal(b, &rs); err != nil {
		t.Fatal(err)
	}
	return rs
}
func testStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.DB.Close() })
	if err = s.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	return s
}
func seed(t *testing.T, s *Store) {
	t.Helper()
	if _, err := s.Import(context.Background(), fixture(t)); err != nil {
		t.Fatal(err)
	}
}
func TestHistoricalImportAndEvidence(t *testing.T) {
	ctx := context.Background()
	s := testStore(t)
	rs := fixture(t)
	for i, want := range []int{60, 0} {
		n, err := s.Import(ctx, rs)
		if err != nil || n != want {
			t.Fatalf("pass %d: %d %v", i, n, err)
		}
	}
	stored, err := s.Responses(ctx, "")
	if err != nil || len(stored) != 60 {
		t.Fatalf("history %d %v", len(stored), err)
	}
	months := map[string]map[int]int{}
	for _, r := range stored {
		if months[r.InternID] == nil {
			months[r.InternID] = map[int]int{}
		}
		months[r.InternID][r.Month]++
		if len(r.Raw) == 0 {
			t.Fatal("raw lost")
		}
	}
	if len(months) != 5 {
		t.Fatal(months)
	}
	for id, m := range months {
		if len(m) != 6 {
			t.Fatal(id, m)
		}
		for _, n := range m {
			if n != 2 {
				t.Fatal("unpaired period")
			}
		}
	}
	p, _ := s.Responses(ctx, "СТ-004")
	for _, r := range p {
		if r.Source == "intern" && (r.Month == 4 || r.Month == 5) {
			want := 2
			if r.Month == 5 {
				want = 4
			}
			if *r.Scores["workload"] != want {
				t.Fatal("historical workload corrupted")
			}
		}
	}
	for _, m := range []int{4, 5} {
		signals := Rules(p, m)
		if m == 4 && status(signals) != "critical" {
			t.Fatal("missing workload alert")
		}
		if m == 5 && status(signals) == "critical" {
			t.Fatal("resolved workload remained critical")
		}
	}
}
func TestValidationAndAtomicConflict(t *testing.T) {
	ctx := context.Background()
	s := testStore(t)
	a := fixture(t)[0]
	for _, value := range []int{0, 6, -1} {
		b := fixture(t)[0]
		b.Scores["workload"] = &value
		if b.Validate() == nil {
			t.Fatal("invalid score accepted")
		}
	}
	a.Scores["workload"] = nil
	if _, err := s.Import(ctx, []Response{a}); err != nil {
		t.Fatal(err)
	}
	stored, _ := s.Responses(ctx, a.InternID)
	if stored[0].Scores["workload"] != nil {
		t.Fatal("null became zero")
	}
	changed := fixture(t)[0]
	if _, err := s.Import(ctx, []Response{changed}); err == nil {
		t.Fatal("changed replay accepted")
	}
	fresh := fixture(t)[1]
	natural := a
	natural.EventID = "new-id-for-same-slot"
	if _, err := s.Import(ctx, []Response{fresh, natural}); err == nil {
		t.Fatal("natural key conflict accepted")
	}
	stored, _ = s.Responses(ctx, "")
	if len(stored) != 1 {
		t.Fatal("partial import committed")
	}
	bad := fixture(t)[2]
	bad.Department = "Other"
	if _, err := s.Import(ctx, []Response{bad}); err == nil {
		t.Fatal("department mismatch accepted")
	}
	bad = fixture(t)[30]
	bad.MentorID = "Н-005"
	if _, err := s.Import(ctx, []Response{bad}); err == nil {
		t.Fatal("wrong mentor accepted")
	}
	bad = fixture(t)[2]
	bad.Month = 7
	if bad.Validate() == nil {
		t.Fatal("month 7 accepted")
	}
}
func TestConcurrentReplay(t *testing.T) {
	s := testStore(t)
	var wg sync.WaitGroup
	errs := make(chan error, 8)
	counts := make(chan int, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			n, err := s.Import(context.Background(), fixture(t)[:1])
			counts <- n
			errs <- err
		}()
	}
	wg.Wait()
	close(counts)
	close(errs)
	sum := 0
	for n := range counts {
		sum += n
	}
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if sum != 1 {
		t.Fatal("duplicate insert", sum)
	}
}
func TestExcelRoundTripAndBlank(t *testing.T) {
	for _, src := range []string{"intern", "mentor"} {
		f := excelize.NewFile()
		sheet := f.GetSheetName(0)
		headers := []string{"ID стажера", "Подразделение / направление стажировки", "Контрольный период"}
		if src == "mentor" {
			headers = append(headers, "ID наставника")
		}
		for _, k := range append(Fields(src), CommentFields(src)...) {
			headers = append(headers, Labels[k])
		}
		head := make([]any, len(headers))
		for i, v := range headers {
			head[i] = v
		}
		if err := f.SetSheetRow(sheet, "A1", &head); err != nil {
			t.Fatal(err)
		}
		row := []any{"СТ-004", "HR-блок", "Месяц 4"}
		if src == "mentor" {
			row = append(row, "Н-004")
		}
		for i := range Fields(src) {
			if i == 0 {
				row = append(row, "")
			} else {
				row = append(row, 2)
			}
		}
		row = append(row, "Текст 1", "Текст 2")
		if err := f.SetSheetRow(sheet, "A2", &row); err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(t.TempDir(), src+".xlsx")
		if err := f.SaveAs(path); err != nil {
			t.Fatal(err)
		}
		f.Close()
		rs, err := ReadExcel(path, src)
		if err != nil || len(rs) != 1 {
			t.Fatalf("excel: %v", err)
		}
		if rs[0].Scores[Fields(src)[0]] != nil {
			t.Fatal("blank lost")
		}
		if rs[0].Month != 4 {
			t.Fatal("wrong period")
		}
	}
}

var testAssets = fstest.MapFS{"index.html": {Data: []byte("<html>pulse</html>")}, "app.js": {Data: []byte("'use strict'")}}

func request(t *testing.T, h http.Handler, method, path string, body any, cookie *http.Cookie) *httptest.ResponseRecorder {
	t.Helper()
	var b []byte
	if body != nil {
		b, _ = json.Marshal(body)
	}
	r := httptest.NewRequest(method, path, bytes.NewReader(b))
	r.Header.Set("Content-Type", "application/json")
	if cookie != nil {
		r.AddCookie(cookie)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}
func loginAs(t *testing.T, s *Store, h http.Handler, name, role, subject string) *http.Cookie {
	t.Helper()
	if err := s.AddUser(context.Background(), name, "a-long-test-password", role, subject); err != nil {
		t.Fatal(err)
	}
	w := request(t, h, "POST", "/api/v1/auth/login", map[string]string{"username": name, "password": "a-long-test-password"}, nil)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	return w.Result().Cookies()[0]
}
func TestRBACCSRFAndSession(t *testing.T) {
	s := testStore(t)
	seed(t, s)
	h := (&Server{Store: s}).Handler(testAssets)
	student := loginAs(t, s, h, "student", "intern", "СТ-001")
	mentor := loginAs(t, s, h, "mentor", "mentor", "Н-001")
	hr := loginAs(t, s, h, "hr", "hr", "")
	tests := []struct {
		path string
		c    *http.Cookie
		code int
	}{{"/api/v1/hr/overview", nil, 401}, {"/api/v1/hr/overview", student, 403}, {"/api/v1/intern/interns/СТ-002", student, 403}, {"/api/v1/intern/interns/СТ-001", student, 200}, {"/api/v1/mentor/interns/СТ-002", mentor, 403}, {"/api/v1/hr/overview", hr, 200}, {"/api/v1/hr/overview?month=7", hr, 400}, {"/api/v1/hr/integrations", mentor, 403}}
	for _, x := range tests {
		w := request(t, h, "GET", x.path, nil, x.c)
		if w.Code != x.code {
			t.Fatalf("%s: %d, %s", x.path, w.Code, w.Body.String())
		}
	}
	r := httptest.NewRequest("POST", "http://mgts.local/api/v1/auth/logout", nil)
	r.Header.Set("Origin", "https://evil.example")
	r.AddCookie(student)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatal("CSRF accepted", w.Code)
	}
	if w := request(t, h, "POST", "/api/v1/auth/logout", nil, student); w.Code != 200 {
		t.Fatal(w.Code)
	}
	if w := request(t, h, "GET", "/api/v1/me", nil, student); w.Code != 401 {
		t.Fatal("revoked session accepted")
	}
}
func TestFeedbackVisibleToOwnIntern(t *testing.T) {
	s := testStore(t)
	h := (&Server{Store: s}).Handler(testAssets)
	mentor := loginAs(t, s, h, "mentor", "mentor", "Н-001")
	student := loginAs(t, s, h, "intern", "intern", "СТ-001")
	text := "Хорошая работа. Следующий шаг — планирование."
	w := request(t, h, "POST", "/api/v1/mentor/interns/СТ-001/feedback", map[string]any{"month": 4, "text": text}, mentor)
	if w.Code != 201 {
		t.Fatal(w.Code, w.Body.String())
	}
	w = request(t, h, "GET", "/api/v1/intern/interns/СТ-001", nil, student)
	if !strings.Contains(w.Body.String(), text) {
		t.Fatal("feedback not visible")
	}
	if w = request(t, h, "POST", "/api/v1/mentor/interns/СТ-002/feedback", map[string]any{"month": 4, "text": text}, mentor); w.Code != 403 {
		t.Fatal("unauthorized feedback")
	}
}
func TestNewEnrollmentWebhookAndReplay(t *testing.T) {
	s := testStore(t)
	seed(t, s)
	app := &Server{Store: s, WebhookSecret: "integration-secret-that-is-long"}
	h := app.Handler(testAssets)
	hr := loginAs(t, s, h, "hr", "hr", "")
	w := request(t, h, "POST", "/api/v1/hr/enrollments", Person{"СТ-006", "IT", "Н-006"}, hr)
	if w.Code != 404 {
		t.Fatal("manual HR registration must be disabled", w.Code)
	}
	row := fixture(t)[0]
	row.EventID = "external:new-1"
	row.InternID = "СТ-006"
	row.Department = "IT"
	row.Raw = nil
	send := func(row Response, secret string) *httptest.ResponseRecorder {
		b, _ := json.Marshal(row)
		r := httptest.NewRequest("POST", "/api/v1/integrations/responses", bytes.NewReader(b))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Authorization", "Bearer "+secret)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	if send(row, "bad").Code != 401 {
		t.Fatal("secret bypass")
	}
	for _, code := range []int{201, 200} {
		if w = send(row, app.WebhookSecret); w.Code != code {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	row.Comments["barriers"] = "Измененный повтор"
	if send(row, app.WebhookSecret).Code != 409 {
		t.Fatal("modified replay accepted")
	}
	rs, _ := s.Responses(context.Background(), "")
	if len(rs) != 61 {
		t.Fatal(len(rs))
	}
	var jobs int
	if err := s.DB.QueryRow("SELECT count(*) FROM jobs WHERE intern_id='СТ-006'").Scan(&jobs); err != nil || jobs != 1 {
		t.Fatal("queue", jobs, err)
	}
	w = request(t, h, "POST", "/api/v1/hr/actions", map[string]string{"internId": "СТ-006", "note": "Провести встречу"}, hr)
	if w.Code != 201 {
		t.Fatal(w.Code)
	}
	var action Action
	json.Unmarshal(w.Body.Bytes(), &action)
	w = request(t, h, "PATCH", "/api/v1/hr/actions/"+action.ID, map[string]string{"status": "closed"}, hr)
	if w.Code != 200 {
		t.Fatal(w.Code)
	}
}
func tallyBody(t *testing.T, row Response) []byte {
	t.Helper()
	e := map[string]any{"eventId": "tally-test-1", "eventType": "FORM_RESPONSE", "data": map[string]any{"formId": "form-intern", "fields": []any{}}}
	fields := []map[string]any{{"label": "ID стажера", "value": row.InternID}, {"label": "Подразделение / направление стажировки", "value": row.Department}, {"label": "Контрольный период", "value": row.Month}}
	for k, v := range row.Scores {
		fields = append(fields, map[string]any{"label": Labels[k], "value": v})
	}
	for k, v := range row.Comments {
		fields = append(fields, map[string]any{"label": Labels[k], "value": v})
	}
	e["data"].(map[string]any)["fields"] = fields
	b, _ := json.Marshal(e)
	return b
}
func TestTallySignature(t *testing.T) {
	s := testStore(t)
	app := &Server{Store: s, TallySecret: "test-tally-secret", InternFormID: "form-intern", MentorFormID: "form-mentor"}
	h := app.Handler(testAssets)
	body := tallyBody(t, fixture(t)[0])
	mac := hmac.New(sha256.New, []byte(app.TallySecret))
	mac.Write(body)
	sig := base64.StdEncoding.EncodeToString(mac.Sum(nil))
	for i, code := range []int{401, 201, 200} {
		r := httptest.NewRequest("POST", "/api/v1/integrations/tally", bytes.NewReader(body))
		if i > 0 {
			r.Header.Set("Tally-Signature", sig)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != code {
			t.Fatal(w.Code, w.Body.String())
		}
	}
}
func goodAnalysis(req AgentRequest) Analysis {
	r := req.History[0]
	k := Fields(r.Source)[0]
	return Analysis{SchemaVersion: "1.0", RequestID: req.RequestID, Revision: req.Revision, Summary: "Сводка по исходной истории", Signals: []Signal{{ID: "s1", Kind: "observation", Severity: "attention", Title: "Нужно обсудить", Evidence: []Evidence{{r.EventID, k, fmt.Sprint(*r.Scores[k])}}}}, Recommendations: []string{"Провести встречу"}}
}
func TestAgentEvidenceValidationAndQueue(t *testing.T) {
	s := testStore(t)
	seed(t, s)
	ctx := context.Background()
	job, err := s.Queue(ctx, "СТ-001")
	for _, r := range job {
		if r > 127 {
			t.Fatal("non-ASCII HTTP idempotency key")
		}
	}
	if err != nil {
		t.Fatal(err)
	}
	agent := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer shared-test-token" {
			t.Error("missing token")
		}
		var req AgentRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Error(err)
		}
		for _, row := range req.History {
			if len(row.Raw) != 0 {
				t.Error("raw sent to agent")
			}
		}
		writeJSON(w, 200, goodAnalysis(req))
	}))
	defer agent.Close()
	worker := &Worker{Store: s, URL: agent.URL, Token: "shared-test-token"}
	if err = worker.Once(ctx); err != nil {
		t.Fatal(err)
	}
	var state string
	s.DB.QueryRow("SELECT state FROM jobs WHERE id=?", job).Scan(&state)
	if state != "done" {
		t.Fatal(state)
	}
	rs, _ := s.Responses(ctx, "СТ-001")
	req := AgentRequest{"1.0", "job", Revision(rs), Program, "СТ-001", rs}
	a := goodAnalysis(req)
	if err = ValidateAnalysis(a, req); err != nil {
		t.Fatal(err)
	}
	a.Signals[0].Evidence[0].Value = "999"
	if ValidateAnalysis(a, req) == nil {
		t.Fatal("invented evidence accepted")
	}
	a = goodAnalysis(req)
	a.Revision = "old"
	if ValidateAnalysis(a, req) == nil {
		t.Fatal("stale analysis accepted")
	}
	found, st, err := s.Analysis(ctx, "СТ-001", rs)
	if err != nil || st != "done" || found == nil {
		t.Fatal(st, err)
	}
}
func TestAgentFailuresAndStaleJobs(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	rs := fixture(t)
	s.Import(ctx, rs[:1])
	old, _ := s.Queue(ctx, "СТ-001")
	s.Import(ctx, rs[1:2])
	fresh, _ := s.Queue(ctx, "СТ-001")
	agent := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Error(w, "down", 503) }))
	defer agent.Close()
	worker := &Worker{Store: s, URL: agent.URL}
	if err := worker.Once(ctx); err != nil {
		t.Fatal(err)
	}
	var state string
	s.DB.QueryRow("SELECT state FROM jobs WHERE id=?", old).Scan(&state)
	if state != "superseded" {
		t.Fatal(state)
	}
	for i := 0; i < 4; i++ {
		s.DB.Exec("UPDATE jobs SET next_at=0 WHERE id=?", fresh)
		if err := worker.Once(ctx); err != nil {
			t.Fatal(err)
		}
	}
	s.DB.QueryRow("SELECT state FROM jobs WHERE id=?", fresh).Scan(&state)
	if state != "failed" {
		t.Fatal(state)
	}
}

func TestMissingPairIsNotPositive(t *testing.T) {
	if profileStatus(nil, 1, nil) != "missing" {
		t.Fatal("missing pair became positive")
	}
	r := fixture(t)[0]
	if profileStatus([]Response{r}, 1, Rules([]Response{r}, 1)) != "attention" {
		t.Fatal("workload 3 should still require attention")
	}
	r = fixture(t)[5]
	if profileStatus([]Response{r}, 6, nil) != "missing" {
		t.Fatal("single source became positive")
	}
}
