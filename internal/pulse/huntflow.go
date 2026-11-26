package pulse

import (
	"bytes"
	"context"
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

type HuntflowClient struct {
	base      string
	accountID int64
	token     string
	bindings  map[string]huntflowBinding
	http      *http.Client
}

type huntflowBinding struct {
	ApplicantID int64  `json:"applicantId"`
	ProfileURL  string `json:"profileUrl"`
}

type huntflowTarget struct {
	huntflowBinding
	AccountID int64  `json:"accountId"`
	Base      string `json:"apiBase"`
}

func NewHuntflowClient(base, account, token, mapping string) (*HuntflowClient, error) {
	if account == "" && token == "" && mapping == "" {
		return nil, nil
	}
	if base == "" {
		base = "https://api.huntflow.ru"
	}
	if !safeTaskURL(base) {
		return nil, errors.New("HUNTFLOW_API_URL requires HTTPS or loopback HTTP")
	}
	parsed, _ := url.Parse(base)
	if (parsed.Path != "" && parsed.Path != "/") || parsed.RawQuery != "" || parsed.Fragment != "" {
		return nil, errors.New("HUNTFLOW_API_URL must be an origin without /v2")
	}
	id, err := strconv.ParseInt(account, 10, 64)
	if err != nil || id <= 0 || strings.TrimSpace(token) == "" {
		return nil, errors.New("Huntflow requires a positive HUNTFLOW_ACCOUNT_ID and HUNTFLOW_ACCESS_TOKEN")
	}
	var bindings map[string]huntflowBinding
	if json.Unmarshal([]byte(mapping), &bindings) != nil || len(bindings) == 0 {
		return nil, errors.New("HUNTFLOW_APPLICANT_MAP must map intern IDs to Huntflow cards")
	}
	seen := map[int64]bool{}
	for intern, binding := range bindings {
		if strings.TrimSpace(intern) == "" || binding.ApplicantID <= 0 ||
			!safeTaskURL(binding.ProfileURL) || seen[binding.ApplicantID] {
			return nil, errors.New("HUNTFLOW_APPLICANT_MAP contains an invalid or duplicate binding")
		}
		seen[binding.ApplicantID] = true
	}
	return &HuntflowClient{
		base: strings.TrimRight(base, "/"), accountID: id, token: token, bindings: bindings,
		http: &http.Client{Timeout: 8 * time.Second,
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }},
	}, nil
}

func (c *HuntflowClient) target(intern string) *huntflowTarget {
	if c == nil {
		return nil
	}
	binding, ok := c.bindings[intern]
	if !ok {
		return nil
	}
	return &huntflowTarget{huntflowBinding: binding, AccountID: c.accountID, Base: c.base}
}

func huntflowDeliveryID(recommendation string) string {
	return digest("huntflow:" + recommendation)
}

type huntflowLog struct {
	ID        int64  `json:"id"`
	Applicant int64  `json:"applicant"`
	Type      string `json:"type"`
	Comment   string `json:"comment"`
}

func (c *HuntflowClient) request(ctx context.Context, method, endpoint string, body []byte, dst any) (int, error) {
	req, err := http.NewRequestWithContext(ctx, method, endpoint, bytes.NewReader(body))
	if err != nil {
		return 0, errors.New("Не удалось подготовить запрос к Huntflow")
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	res, err := c.http.Do(req)
	if err != nil {
		return 0, errors.New("Huntflow недоступен")
	}
	defer res.Body.Close()
	code := res.StatusCode
	if code != http.StatusOK && code != http.StatusCreated {
		return code, fmt.Errorf("Huntflow вернул HTTP %d", code)
	}
	const maxBody = 8 << 20
	data, err := io.ReadAll(io.LimitReader(res.Body, maxBody+1))
	if err != nil || len(data) > maxBody || json.Unmarshal(data, dst) != nil {
		return code, errors.New("Huntflow вернул некорректный ответ")
	}
	return code, nil
}

func (c *HuntflowClient) findNote(ctx context.Context, endpoint, marker string) (int64, error) {
	for page := 1; page <= 25; page++ {
		query := url.Values{"type": {"COMMENT"}, "personal": {"true"}, "page": {strconv.Itoa(page)}, "count": {"30"}}
		var logs struct {
			Page       int           `json:"page"`
			TotalPages int           `json:"total_pages"`
			Items      []huntflowLog `json:"items"`
		}
		if _, err := c.request(ctx, http.MethodGet, endpoint+"?"+query.Encode(), nil, &logs); err != nil {
			return 0, err
		}
		if logs.Page != page || logs.TotalPages < 0 || logs.Items == nil {
			return 0, errors.New("Huntflow вернул неполную страницу журнала")
		}
		for _, item := range logs.Items {
			if item.ID > 0 && item.Type == "COMMENT" && strings.Contains(item.Comment, marker) {
				return item.ID, nil
			}
		}
		if page >= logs.TotalPages {
			return 0, nil
		}
	}
	return 0, errors.New("Превышен лимит проверки журнала Huntflow")
}

func (c *HuntflowClient) create(ctx context.Context, job hrTask, reconcileOnly bool) (TaskDelivery, error) {
	target := job.Huntflow
	if target == nil || target.Base != c.base || target.AccountID != c.accountID {
		if reconcileOnly {
			return TaskDelivery{State: "uncertain"}, errors.New("Проверьте сохранённую привязку к аккаунту Huntflow")
		}
		return TaskDelivery{}, errors.New("Проверьте сохранённую привязку к аккаунту Huntflow")
	}
	endpoint := fmt.Sprintf("%s/v2/accounts/%d/applicants/%d/logs", target.Base, target.AccountID, target.ApplicantID)
	marker := "[pulse:" + huntflowDeliveryID(job.ID) + "]"
	existing, err := c.findNote(ctx, endpoint, marker)
	if err != nil {
		if reconcileOnly {
			return TaskDelivery{State: "uncertain"}, err
		}
		return TaskDelivery{}, err
	}
	if existing > 0 {
		return TaskDelivery{State: "sent", ExternalID: strconv.FormatInt(existing, 10), ExternalURL: target.ProfileURL}, nil
	}
	if reconcileOnly {
		return TaskDelivery{State: "uncertain"}, errors.New("Запись пока не найдена. Проверьте карточку кандидата; повторная проверка не создаёт новую заметку")
	}
	payload := struct {
		Comment string `json:"comment"`
	}{fmt.Sprintf("Пульс стажировки · месяц %d\n%s\n%s", job.Month, job.Description, marker)}
	body, err := json.Marshal(payload)
	if err != nil {
		return TaskDelivery{}, err
	}
	var log huntflowLog
	code, err := c.request(ctx, http.MethodPost, endpoint, body, &log)
	if err != nil {
		if code == 0 || code >= 500 || code == http.StatusRequestTimeout || code == http.StatusConflict ||
			(code >= 200 && code < 300) {
			return TaskDelivery{State: "uncertain"}, err
		}
		return TaskDelivery{}, err
	}
	if log.ID <= 0 || log.Applicant != target.ApplicantID || log.Type != "COMMENT" {
		return TaskDelivery{State: "uncertain"}, errors.New("Huntflow не подтвердил ID заметки в нужной карточке")
	}
	return TaskDelivery{State: "sent", ExternalID: strconv.FormatInt(log.ID, 10), ExternalURL: target.ProfileURL}, nil
}
