package pulse

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
)

func (s *Server) webhook(w http.ResponseWriter, r *http.Request) {
	if s.WebhookSecret == "" {
		problem(w, 503, "Внешняя интеграция не настроена")
		return
	}
	if !secureEqual(r.Header.Get("Authorization"), "Bearer "+s.WebhookSecret) {
		problem(w, 401, "Неверный секрет интеграции")
		return
	}
	var row Response
	if !decode(w, r, &row) {
		return
	}
	row.ReceivedAt = ""
	s.ingest(w, r, row, "webhook")
}

type TallyField struct {
	Label string          `json:"label"`
	Value json.RawMessage `json:"value"`
}
type TallyEvent struct {
	EventID   string `json:"eventId"`
	EventType string `json:"eventType"`
	Data      struct {
		FormID string       `json:"formId"`
		Fields []TallyField `json:"fields"`
	} `json:"data"`
}

func TallyResponse(body []byte, source string) (Response, error) {
	var event TallyEvent
	if err := json.Unmarshal(body, &event); err != nil {
		return Response{}, err
	}
	if event.EventType != "FORM_RESPONSE" {
		return Response{}, errors.New("неверный eventType")
	}
	vals := map[string]string{}
	for _, f := range event.Data.Fields {
		if _, exists := vals[f.Label]; exists {
			return Response{}, errors.New("дублирующаяся подпись поля")
		}
		var v any
		if err := json.Unmarshal(f.Value, &v); err != nil {
			return Response{}, err
		}
		switch x := v.(type) {
		case nil:
			vals[f.Label] = ""
		case string:
			vals[f.Label] = strings.TrimSpace(x)
		case float64:
			vals[f.Label] = strconv.FormatFloat(x, 'f', -1, 64)
		default:
			return Response{}, errors.New("используйте текстовые и числовые поля Tally, без dropdown")
		}
	}
	required := []string{"ID стажера", "Подразделение / направление стажировки", "Контрольный период"}
	if source == "mentor" {
		required = append(required, "ID наставника")
	}
	for _, k := range append(Fields(source), CommentFields(source)...) {
		required = append(required, Labels[k])
	}
	for _, k := range required {
		if _, ok := vals[k]; !ok {
			return Response{}, fmt.Errorf("нет поля %s", k)
		}
	}
	period := strings.TrimPrefix(vals["Контрольный период"], "Месяц ")
	month, err := strconv.Atoi(period)
	if err != nil {
		return Response{}, errors.New("неверный контрольный период")
	}
	r := Response{EventID: "tally:" + event.EventID, ProgramID: Program, InternID: vals["ID стажера"], Department: vals["Подразделение / направление стажировки"], Month: month, Source: source, Scores: map[string]*int{}, Comments: map[string]string{}}
	if source == "mentor" {
		r.MentorID = vals["ID наставника"]
	}
	for _, k := range Fields(source) {
		v := vals[Labels[k]]
		if v == "" {
			r.Scores[k] = nil
			continue
		}
		n, err := strconv.Atoi(v)
		if err != nil {
			return r, fmt.Errorf("оценка %s: ожидается целое число", k)
		}
		r.Scores[k] = &n
	}
	for _, k := range CommentFields(source) {
		r.Comments[k] = vals[Labels[k]]
	}
	// Preserve question answers only: full Tally payload can contain PDF access tokens.
	r.Raw, _ = json.Marshal(vals)
	if event.EventID == "" {
		return r, errors.New("не задан eventId")
	}
	return r, r.Validate()
}
func (s *Server) tally(w http.ResponseWriter, r *http.Request) {
	if s.TallySecret == "" || s.InternFormID == "" || s.MentorFormID == "" {
		problem(w, 503, "Tally не настроен")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	body, err := io.ReadAll(r.Body)
	if err != nil {
		problem(w, 413, "Слишком большой запрос")
		return
	}
	sig, err := base64.StdEncoding.DecodeString(r.Header.Get("Tally-Signature"))
	mac := hmac.New(sha256.New, []byte(s.TallySecret))
	mac.Write(body)
	if err != nil || !hmac.Equal(sig, mac.Sum(nil)) {
		problem(w, 401, "Неверная подпись Tally")
		return
	}
	var event TallyEvent
	if json.Unmarshal(body, &event) != nil {
		problem(w, 400, "Некорректный JSON Tally")
		return
	}
	source := ""
	if event.Data.FormID == s.InternFormID {
		source = "intern"
	} else if event.Data.FormID == s.MentorFormID {
		source = "mentor"
	}
	if source == "" {
		problem(w, 422, "Неизвестный formId")
		return
	}
	row, err := TallyResponse(body, source)
	if err != nil {
		_ = s.Store.RecordDelivery(r.Context(), "tally:"+event.EventID, "rejected", "tally")
		problem(w, 422, err.Error())
		return
	}
	s.ingest(w, r, row, "tally")
}
