package pulse

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const huntflowSchema = `CREATE TABLE IF NOT EXISTS huntflow_bindings (
 intern_id TEXT PRIMARY KEY REFERENCES people(id), account_id INTEGER NOT NULL,
 applicant_id INTEGER NOT NULL, created_by TEXT NOT NULL, created_at TEXT NOT NULL,
 UNIQUE(account_id,applicant_id));
 INSERT OR IGNORE INTO schema_version VALUES(3);`

type HuntflowClient struct {
	base, token string
	account     int64
	http        *http.Client
	store       *Store
}
type HuntflowBinding struct {
	AccountID   int64 `json:"accountId"`
	ApplicantID int64 `json:"applicantId"`
}

func NewHuntflowClient(base, token, account string, store *Store) (*HuntflowClient, error) {
	if token == "" && account == "" {
		return nil, nil
	}
	if base == "" {
		base = "https://api.huntflow.ru/v2"
	}
	u, err := url.Parse(base)
	id, idErr := strconv.ParseInt(account, 10, 64)
	if err != nil || !safeTaskURL(base) || u.RawQuery != "" || u.Fragment != "" || idErr != nil || id <= 0 || strings.TrimSpace(token) == "" || strings.ContainsAny(token, "\r\n") {
		return nil, errors.New("HUNTFLOW_BASE_URL, HUNTFLOW_ACCESS_TOKEN and positive HUNTFLOW_ACCOUNT_ID required")
	}
	return &HuntflowClient{base: strings.TrimRight(base, "/"), token: token, account: id, store: store, http: &http.Client{Timeout: 8 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}, nil
}

// Never return external error bodies: they can contain applicant data or credentials.
func (c *HuntflowClient) request(ctx context.Context, method, path string, body any, result any) error {
	var reader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, reader)
	if err != nil {
		return errors.New("Не удалось подготовить запрос Хантфлоу")
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("User-Agent", "MGTS-Pulse/1.0 (HR integration)")
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	res, err := c.http.Do(req)
	if err != nil {
		return errors.New("Хантфлоу недоступен; повторите позже")
	}
	defer res.Body.Close()
	switch res.StatusCode {
	case 200, 201:
	case 401:
		return errors.New("Токен Хантфлоу недействителен или истёк; обновите HUNTFLOW_ACCESS_TOKEN")
	case 403:
		return errors.New("Нет доступа к организации или кандидату Хантфлоу")
	case 404:
		return errors.New("Кандидат или организация Хантфлоу не найдены")
	case 429:
		return errors.New("Лимит запросов Хантфлоу; повторите позже")
	default:
		return fmt.Errorf("Хантфлоу вернул HTTP %d", res.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(res.Body, (2<<20)+1))
	if err != nil || len(data) > 2<<20 || json.Unmarshal(data, result) != nil {
		return errors.New("Некорректный ответ Хантфлоу")
	}
	return nil
}
func (c *HuntflowClient) applicantPath(id int64) string {
	return fmt.Sprintf("/accounts/%d/applicants/%d", c.account, id)
}
func (s *Store) huntflowBinding(ctx context.Context, id string) (*HuntflowBinding, error) {
	var b HuntflowBinding
	err := s.DB.QueryRowContext(ctx, "SELECT account_id,applicant_id FROM huntflow_bindings WHERE intern_id=?", id).Scan(&b.AccountID, &b.ApplicantID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return &b, err
}
func huntflowDeliveryID(jobID string, b *HuntflowBinding) string {
	return "huntflow:" + digest(fmt.Sprintf("%d:%d:%s", b.AccountID, b.ApplicantID, jobID))
}

// The API does not promise Idempotency-Key for comments. Reconcile by an exact
// stable marker before every attempt; never POST if history could not be checked.
func (c *HuntflowClient) create(ctx context.Context, job hrTask) (TaskDelivery, error) {
	b, err := c.store.huntflowBinding(ctx, job.Intern)
	if err != nil {
		return TaskDelivery{}, err
	}
	if b == nil || b.AccountID != c.account {
		return TaskDelivery{}, errors.New("Стажёр не связан с кандидатом этой организации Хантфлоу")
	}
	marker := "[mgts-pulse:" + job.ID + "]"
	for page := 1; page <= 100; page++ {
		var logs struct {
			Page       int `json:"page"`
			TotalPages int `json:"total_pages"`
			Items      []struct {
				ID      int64  `json:"id"`
				Comment string `json:"comment"`
			} `json:"items"`
		}
		if err = c.request(ctx, "GET", c.applicantPath(b.ApplicantID)+fmt.Sprintf("/logs?personal=true&count=100&page=%d", page), nil, &logs); err != nil {
			return TaskDelivery{}, err
		}
		if logs.Page != page || logs.TotalPages < 0 || logs.TotalPages > 100 || logs.Items == nil {
			return TaskDelivery{}, errors.New("Не удалось проверить историю Хантфлоу перед отправкой")
		}
		for _, log := range logs.Items {
			if log.ID > 0 && strings.HasSuffix(log.Comment, "\n"+marker) {
				return TaskDelivery{State: "sent", ExternalID: strconv.FormatInt(log.ID, 10)}, nil
			}
		}
		if page >= logs.TotalPages {
			break
		}
	}
	// Only neutral recommendation content, no raw responses, scores or model prose.
	comment := fmt.Sprintf("МГТС · Пульс стажировки\nСтажёр: %s · месяц %d\nПолучатель: %s (%s)\n%s\n%s", job.Intern, job.Month, job.AssigneeRole, job.Assignee, job.Description, marker)
	var result struct {
		ID        int64  `json:"id"`
		Applicant int64  `json:"applicant"`
		Type      string `json:"type"`
	}
	if err = c.request(ctx, "POST", c.applicantPath(b.ApplicantID)+"/logs", map[string]string{"comment": comment}, &result); err != nil {
		return TaskDelivery{}, err
	}
	if result.ID <= 0 || result.Applicant != b.ApplicantID || result.Type != "COMMENT" {
		return TaskDelivery{}, errors.New("Не удалось подтвердить запись Хантфлоу; повторите проверку")
	}
	return TaskDelivery{State: "sent", ExternalID: strconv.FormatInt(result.ID, 10)}, nil
}

func (s *Server) huntflowStatus(w http.ResponseWriter, r *http.Request) {
	if s.Huntflow == nil {
		writeJSON(w, 200, map[string]any{"configured": false})
		return
	}
	var accounts struct {
		Items []struct {
			ID   int64  `json:"id"`
			Name string `json:"name"`
		} `json:"items"`
	}
	if err := s.Huntflow.request(r.Context(), "GET", "/accounts", nil, &accounts); err != nil {
		problem(w, 502, err.Error())
		return
	}
	for _, a := range accounts.Items {
		if a.ID == s.Huntflow.account {
			writeJSON(w, 200, map[string]any{"configured": true, "accountId": a.ID, "accountName": a.Name})
			return
		}
	}
	problem(w, 502, "Организация недоступна этому токену Хантфлоу")
}

func (s *Server) bindHuntflow(w http.ResponseWriter, r *http.Request) {
	if s.Huntflow == nil {
		problem(w, 503, "Хантфлоу не подключён")
		return
	}
	var in struct {
		InternID    string `json:"internId"`
		ApplicantID int64  `json:"applicantId"`
		Department  string `json:"department"`
		MentorID    string `json:"mentorId"`
	}
	if !decode(w, r, &in) {
		return
	}
	in.InternID = strings.TrimSpace(in.InternID)
	in.Department = strings.TrimSpace(in.Department)
	in.MentorID = strings.TrimSpace(in.MentorID)
	if in.InternID == "" && in.ApplicantID > 0 {
		in.InternID = fmt.Sprintf("HF-%d", in.ApplicantID)
	}
	if in.ApplicantID <= 0 || len(in.InternID) > 100 || in.Department == "" || len(in.Department) > 200 || in.MentorID == "" || len(in.MentorID) > 100 {
		problem(w, 422, "Укажите ID кандидата, подразделение и наставника допустимой длины")
		return
	}
	var applicant struct {
		ID int64 `json:"id"`
	}
	if err := s.Huntflow.request(r.Context(), "GET", s.Huntflow.applicantPath(in.ApplicantID), nil, &applicant); err != nil {
		problem(w, 502, err.Error())
		return
	}
	if applicant.ID != in.ApplicantID {
		problem(w, 502, "Хантфлоу вернул другого кандидата")
		return
	}
	tx, err := s.Store.DB.BeginTx(r.Context(), nil)
	if err != nil {
		s.internal(w, err)
		return
	}
	defer tx.Rollback()
	var department, mentor string
	err = tx.QueryRowContext(r.Context(), "SELECT department,mentor_id FROM people WHERE id=?", in.InternID).Scan(&department, &mentor)
	if errors.Is(err, sql.ErrNoRows) {
		_, err = tx.ExecContext(r.Context(), "INSERT INTO people(id,department,mentor_id) VALUES(?,?,?)", in.InternID, in.Department, in.MentorID)
	} else if err == nil && (department != in.Department || mentor != in.MentorID) {
		problem(w, 409, "Подразделение или наставник существующей карточки не совпадают")
		return
	}
	if err != nil {
		s.internal(w, err)
		return
	}
	var account, appID int64
	err = tx.QueryRowContext(r.Context(), "SELECT account_id,applicant_id FROM huntflow_bindings WHERE intern_id=?", in.InternID).Scan(&account, &appID)
	if err == nil && (account != s.Huntflow.account || appID != in.ApplicantID) {
		problem(w, 409, "Карточка уже связана с другим кандидатом")
		return
	}
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		s.internal(w, err)
		return
	}
	var owner string
	err = tx.QueryRowContext(r.Context(), "SELECT intern_id FROM huntflow_bindings WHERE account_id=? AND applicant_id=?", s.Huntflow.account, in.ApplicantID).Scan(&owner)
	if err == nil && owner != in.InternID {
		problem(w, 409, "Кандидат уже связан с другим стажёром")
		return
	}
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		s.internal(w, err)
		return
	}
	_, err = tx.ExecContext(r.Context(), "INSERT OR IGNORE INTO huntflow_bindings VALUES(?,?,?,?,?)", in.InternID, s.Huntflow.account, in.ApplicantID, identity(r).Username, stamp())
	if err != nil {
		s.internal(w, err)
		return
	}
	if err = tx.Commit(); err != nil {
		s.internal(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"internId": in.InternID, "accountId": s.Huntflow.account, "applicantId": in.ApplicantID})
}

func (s *Server) confirmHuntflow(w http.ResponseWriter, r *http.Request) {
	if s.Huntflow == nil {
		problem(w, 503, "Хантфлоу не подключён")
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
	rows, err := s.Store.Responses(r.Context(), id)
	if err != nil {
		s.internal(w, err)
		return
	}
	a, _, err := s.Store.Analysis(r.Context(), id, rows)
	if err != nil {
		s.internal(w, err)
		return
	}
	b, err := s.Store.huntflowBinding(r.Context(), id)
	if err != nil {
		s.internal(w, err)
		return
	}
	if b == nil || b.AccountID != s.Huntflow.account {
		problem(w, 422, "Сначала свяжите стажёра с кандидатом Хантфлоу")
		return
	}
	for _, item := range monthRecommendations(id, month, rows, a, Rules(rows, month)) {
		if item.ID != r.PathValue("recommendation") {
			continue
		}
		who := identity(r)
		assignee := who.Username
		if item.Audience == "mentor" {
			person, err := s.Store.Person(r.Context(), id)
			if err != nil {
				s.internal(w, err)
				return
			}
			assignee = person.MentorID
			if assignee == "" {
				problem(w, 422, "Наставник ещё не назначен")
				return
			}
		}
		job := hrTask{ID: huntflowDeliveryID(item.ID, b), Program: Program, Intern: id, Month: month, Action: item.ActionType, Assignee: assignee, AssigneeRole: item.Audience, Description: item.Title + ". " + item.Reason}
		out, err := s.deliverTask(r.Context(), job, who.Username, &HRTaskClient{huntflow: s.Huntflow})
		if err != nil {
			s.internal(w, err)
			return
		}
		code := 200
		if out.State == "failed" {
			code = 502
		} else if out.State == "sending" {
			code = 202
		}
		writeJSON(w, code, out)
		return
	}
	problem(w, 404, "Рекомендация за выбранный месяц не найдена")
}
