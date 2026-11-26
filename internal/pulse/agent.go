package pulse

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"
)

type AgentRequest struct {
	SchemaVersion string     `json:"schemaVersion"`
	RequestID     string     `json:"requestId"`
	Revision      string     `json:"revision"`
	ProgramID     string     `json:"programId"`
	InternID      string     `json:"internId"`
	History       []Response `json:"history"`
}
type Worker struct {
	Store      *Store
	URL, Token string
	Client     *http.Client
}

func (w *Worker) Run(ctx context.Context) {
	// The supported deployment is one Go process. Interrupted jobs are resumed.
	if _, err := w.Store.DB.ExecContext(ctx, "UPDATE jobs SET state='pending' WHERE state='running'"); err != nil {
		slog.Error("recover jobs", "error", err)
		return
	}
	if w.URL == "" {
		return
	}
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			if err := w.Once(ctx); err != nil {
				slog.Error("analysis worker", "error", err)
			}
		}
	}
}
func (w *Worker) Once(ctx context.Context) error {
	var id, intern, rev string
	var attempts int
	err := w.Store.DB.QueryRowContext(ctx, "SELECT id,intern_id,revision,attempts FROM jobs WHERE state='pending' AND next_at<=? ORDER BY created_at LIMIT 1", time.Now().Unix()).Scan(&id, &intern, &rev, &attempts)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if _, err = w.Store.DB.ExecContext(ctx, "UPDATE jobs SET state='running' WHERE id=?", id); err != nil {
		return err
	}
	rs, err := w.Store.Responses(ctx, intern)
	if err != nil {
		return w.fail(ctx, id, attempts, "storage_error")
	}
	if Revision(rs) != rev {
		_, err = w.Store.DB.ExecContext(ctx, "UPDATE jobs SET state='superseded' WHERE id=?", id)
		return err
	}
	for i := range rs {
		rs[i].Raw = nil
	} // no vendor payloads or credentials sent to the agent
	reqBody := AgentRequest{"1.0", id, rev, Program, intern, rs}
	body, _ := json.Marshal(reqBody)
	callCtx, cancel := context.WithTimeout(ctx, 25*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(callCtx, http.MethodPost, strings.TrimRight(w.URL, "/")+"/v1/analyze", bytes.NewReader(body))
	if err != nil {
		return w.fail(ctx, id, attempts, "invalid_agent_url")
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+w.Token)
	req.Header.Set("Idempotency-Key", id)
	client := w.Client
	if client == nil {
		client = &http.Client{Timeout: 26 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	}
	res, err := client.Do(req)
	if err != nil {
		return w.fail(ctx, id, attempts, "agent_unavailable")
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return w.fail(ctx, id, attempts, fmt.Sprintf("agent_http_%d", res.StatusCode))
	}
	b, err := io.ReadAll(io.LimitReader(res.Body, 1<<20+1))
	if err != nil || len(b) > 1<<20 {
		return w.fail(ctx, id, attempts, "invalid_agent_body")
	}
	var a Analysis
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	if err = dec.Decode(&a); err != nil {
		return w.fail(ctx, id, attempts, "invalid_agent_json")
	}
	var extra any
	if dec.Decode(&extra) != io.EOF {
		return w.fail(ctx, id, attempts, "invalid_agent_json")
	}
	if err = ValidateAnalysis(a, reqBody); err != nil {
		return w.fail(ctx, id, attempts, "invalid_agent_evidence")
	}
	tx, err := w.Store.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	payload, _ := json.Marshal(a)
	if _, err = tx.ExecContext(ctx, "INSERT OR REPLACE INTO analyses VALUES(?,?,?,?)", intern, rev, string(payload), stamp()); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, "UPDATE jobs SET state='done',last_error='' WHERE id=?", id); err != nil {
		return err
	}
	return tx.Commit()
}
func (w *Worker) fail(ctx context.Context, id string, attempts int, code string) error {
	attempts++
	state := "pending"
	if attempts >= 4 {
		state = "failed"
	}
	_, err := w.Store.DB.ExecContext(ctx, "UPDATE jobs SET state=?,attempts=?,next_at=?,last_error=? WHERE id=?", state, attempts, time.Now().Add(time.Duration(1<<attempts)*time.Second).Unix(), code, id)
	slog.Warn("agent request failed", "job", id, "attempt", attempts, "code", code)
	return err
}
func ValidateAnalysis(a Analysis, r AgentRequest) error {
	if a.SchemaVersion != "1.0" || a.RequestID != r.RequestID || a.Revision != r.Revision || strings.TrimSpace(a.Summary) == "" || len(a.Summary) > 10000 {
		return errors.New("invalid identity or summary")
	}
	if a.Signals == nil || a.Recommendations == nil || len(a.Signals) > 100 || len(a.Recommendations) > 20 {
		return errors.New("invalid arrays")
	}
	index := map[string]Response{}
	for _, v := range r.History {
		index[v.EventID] = v
	}
	ids := map[string]bool{}
	for _, s := range a.Signals {
		if s.ID == "" || len(s.ID) > 160 || ids[s.ID] || s.Kind == "" || len(s.Kind) > 100 || s.Title == "" || len(s.Title) > 2000 || len(s.Evidence) == 0 || len(s.Evidence) > 20 {
			return errors.New("invalid signal")
		}
		ids[s.ID] = true
		if s.Severity != "positive" && s.Severity != "attention" && s.Severity != "critical" {
			return errors.New("invalid severity")
		}
		for _, e := range s.Evidence {
			row, ok := index[e.EventID]
			if !ok {
				return errors.New("unknown event")
			}
			if v, ok := row.Scores[e.Field]; ok {
				if v == nil || strconv.Itoa(*v) != e.Value {
					return errors.New("score mismatch")
				}
			} else if v, ok := row.Comments[e.Field]; !ok || v != e.Value {
				return errors.New("comment mismatch")
			}
		}
	}
	for _, v := range a.Recommendations {
		if strings.TrimSpace(v) == "" || len(v) > 2000 {
			return errors.New("invalid recommendation")
		}
	}
	return nil
}
func (s *Store) Analysis(ctx context.Context, id string, rs []Response) (*Analysis, string, error) {
	var body string
	rev := Revision(rs)
	err := s.DB.QueryRowContext(ctx, "SELECT payload FROM analyses WHERE intern_id=? AND revision=?", id, rev).Scan(&body)
	if err == nil {
		var a Analysis
		err = json.Unmarshal([]byte(body), &a)
		return &a, "done", err
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, "", err
	}
	var state string
	err = s.DB.QueryRowContext(ctx, "SELECT state FROM jobs WHERE intern_id=? AND revision=?", id, rev).Scan(&state)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, "not_requested", nil
	}
	return nil, state, err
}
