package pulse

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const hrTaskSchema = `
CREATE TABLE IF NOT EXISTS hr_task_deliveries (
 id TEXT PRIMARY KEY, confirmed_by TEXT NOT NULL, confirmed_at TEXT NOT NULL,
 payload TEXT NOT NULL,
 state TEXT NOT NULL DEFAULT 'pending', attempts INTEGER NOT NULL DEFAULT 0,
 lease_until INTEGER NOT NULL DEFAULT 0, external_id TEXT NOT NULL DEFAULT '',
 external_url TEXT NOT NULL DEFAULT '', last_error TEXT NOT NULL DEFAULT ''
);
INSERT OR IGNORE INTO schema_version VALUES(2);
`

type TaskDelivery struct {
	State       string `json:"state"`
	ExternalID  string `json:"externalId,omitempty"`
	ExternalURL string `json:"externalUrl,omitempty"`
	Error       string `json:"error,omitempty"`
	Attempts    int    `json:"attempts"`
}

type HRTaskClient struct {
	endpoint string
	token    string
	http     *http.Client
}

type hrTask struct {
	ID           string `json:"id"`
	Program      string `json:"programId"`
	Intern       string `json:"internId"`
	Month        int    `json:"month"`
	Action       string `json:"actionType"`
	Assignee     string `json:"assignee"`
	AssigneeRole string `json:"assigneeRole"`
	Description  string `json:"description"`
}

func safeTaskURL(value string) bool {
	u, err := url.Parse(value)
	if err != nil || u.Hostname() == "" || u.User != nil {
		return false
	}
	ip := net.ParseIP(u.Hostname())
	return u.Scheme == "https" || (u.Scheme == "http" &&
		(strings.EqualFold(u.Hostname(), "localhost") || (ip != nil && ip.IsLoopback())))
}

func NewHRTaskClient(endpoint, token string) (*HRTaskClient, error) {
	if endpoint == "" {
		return nil, nil
	}
	if !safeTaskURL(endpoint) {
		return nil, errors.New("HR_TASK_URL requires HTTPS or loopback HTTP")
	}
	return &HRTaskClient{endpoint: endpoint, token: token, http: &http.Client{
		Timeout:       8 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}}, nil
}

func (c *HRTaskClient) create(ctx context.Context, job hrTask) (TaskDelivery, error) {
	body, err := json.Marshal(job)
	if err != nil {
		return TaskDelivery{}, errors.New("Не удалось подготовить задачу")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(body))
	if err != nil {
		return TaskDelivery{}, errors.New("Не удалось подготовить запрос к HR-сервису")
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", job.ID)
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	res, err := c.http.Do(req)
	if err != nil {
		return TaskDelivery{}, errors.New("HR-сервис недоступен")
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusCreated && res.StatusCode != http.StatusOK {
		return TaskDelivery{}, fmt.Errorf("HR-сервис вернул HTTP %d", res.StatusCode)
	}
	var result struct {
		ID  string `json:"id"`
		URL string `json:"url"`
	}
	data, err := io.ReadAll(io.LimitReader(res.Body, 32<<10+1))
	if err != nil || len(data) > 32<<10 || json.Unmarshal(data, &result) != nil ||
		strings.TrimSpace(result.ID) == "" || len(result.ID) > 200 || !safeTaskURL(result.URL) {
		return TaskDelivery{}, errors.New("Проверьте ID и ссылку в ответе HR-сервиса")
	}
	return TaskDelivery{State: "sent", ExternalID: result.ID, ExternalURL: result.URL}, nil
}

func (s *Store) taskDelivery(ctx context.Context, id string) (*TaskDelivery, error) {
	var d TaskDelivery
	err := s.DB.QueryRowContext(ctx, `
 SELECT CASE WHEN state='sending' AND lease_until<=unixepoch() THEN 'failed' ELSE state END,
        attempts,external_id,external_url,
        CASE WHEN state='sending' AND lease_until<=unixepoch() THEN 'Отправка прервалась, можно повторить' ELSE last_error END
 FROM hr_task_deliveries WHERE id=?`, id).
		Scan(&d.State, &d.Attempts, &d.ExternalID, &d.ExternalURL, &d.Error)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return &d, err
}

func (s *Server) confirmRecommendation(w http.ResponseWriter, r *http.Request) {
	if s.HRTasks == nil {
		problem(w, 503, "HR-сервис задач не настроен")
		return
	}
	id, ok := s.scope(w, r)
	if !ok {
		return
	}
	month, err := selectedMonth(r)
	if err != nil {
		problem(w, 400, err.Error())
		return
	}
	ctx := r.Context()
	rows, err := s.Store.Responses(ctx, id)
	if err != nil {
		s.internal(w, err)
		return
	}
	analysis, _, err := s.Store.Analysis(ctx, id, rows)
	if err != nil {
		s.internal(w, err)
		return
	}
	var recommendation *Recommendation
	for _, item := range monthRecommendations(id, month, rows, analysis, Rules(rows, month)) {
		if item.ID == r.PathValue("recommendation") {
			recommendation = &item
			break
		}
	}
	if recommendation == nil {
		problem(w, 404, "Рекомендация за выбранный месяц не найдена")
		return
	}
	who := identity(r)
	job := hrTask{
		ID: recommendation.ID, Program: Program, Intern: id, Month: month, Action: recommendation.ActionType,
		Assignee: who.Username, AssigneeRole: recommendation.Audience,
		Description: recommendation.Title + ". " + recommendation.Reason,
	}
	if job.AssigneeRole == "mentor" {
		person, err := s.Store.Person(ctx, id)
		if err != nil {
			s.internal(w, err)
			return
		}
		job.Assignee = person.MentorID
	}
	out, err := s.deliverHRTask(ctx, job, who.Username)
	if err != nil {
		s.internal(w, err)
		return
	}
	code := http.StatusOK
	if out.State == "failed" {
		code = http.StatusBadGateway
	} else if out.State == "sending" {
		code = http.StatusAccepted
	}
	writeJSON(w, code, out)
}

func (s *Server) deliverHRTask(ctx context.Context, job hrTask, confirmedBy string) (*TaskDelivery, error) {
	// Continue persisting the outcome if the browser closes after HR confirmed.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 15*time.Second)
	defer cancel()
	tx, err := s.Store.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	payload, err := json.Marshal(job)
	if err != nil {
		return nil, err
	}
	if _, err = tx.ExecContext(ctx, "INSERT OR IGNORE INTO hr_task_deliveries(id,confirmed_by,confirmed_at,payload) VALUES(?,?,?,?)",
		job.ID, confirmedBy, stamp(), string(payload)); err != nil {
		return nil, err
	}
	// Keep the full payload stable even when another HR user retries delivery.
	if err = tx.QueryRowContext(ctx, "SELECT payload FROM hr_task_deliveries WHERE id=?", job.ID).Scan(&payload); err != nil {
		return nil, err
	}
	if err = json.Unmarshal(payload, &job); err != nil {
		return nil, err
	}
	now := time.Now().Unix()
	var attempt int
	err = tx.QueryRowContext(ctx, `
 UPDATE hr_task_deliveries SET state='sending',lease_until=?,attempts=attempts+1,last_error=''
 WHERE id=? AND (state IN ('pending','failed') OR (state='sending' AND lease_until<=?))
 RETURNING attempts`, now+30, job.ID, now).Scan(&attempt)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	if attempt == 0 {
		return s.Store.taskDelivery(ctx, job.ID)
	}
	delivery, sendErr := s.HRTasks.create(ctx, job)
	if sendErr != nil {
		delivery = TaskDelivery{State: "failed", Error: sendErr.Error()}
	}
	_, err = s.Store.DB.ExecContext(ctx, `
 UPDATE hr_task_deliveries SET state=?,external_id=?,external_url=?,last_error=?,lease_until=0
 WHERE id=? AND state='sending' AND attempts=?`,
		delivery.State, delivery.ExternalID, delivery.ExternalURL, delivery.Error, job.ID, attempt)
	if err != nil {
		return nil, err
	}
	return s.Store.taskDelivery(ctx, job.ID)
}
