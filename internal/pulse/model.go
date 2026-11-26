package pulse

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

const Program = "mgts-internship"

var ErrConflict = errors.New("conflict")
var ErrNotFound = errors.New("not found")
var ErrValidation = errors.New("validation")
var InternFields = []string{"taskClarity", "mentorSupport", "workload", "interest", "development", "teamwork", "satisfaction"}
var MentorFields = []string{"taskUnderstanding", "quality", "independence", "deadlines", "initiative", "communication"}
var Labels = map[string]string{
	"taskClarity": "Понятность задач", "mentorSupport": "Поддержка и обратная связь наставника", "workload": "Комфортность нагрузки", "interest": "Интерес к задачам", "development": "Профессиональное развитие", "teamwork": "Взаимодействие с командой", "satisfaction": "Общая удовлетворенность стажировкой",
	"taskUnderstanding": "Понимание задач и ожиданий", "quality": "Качество выполнения задач", "independence": "Самостоятельность", "deadlines": "Соблюдение сроков и организация работы", "initiative": "Вовлеченность и инициативность", "communication": "Взаимодействие с наставником и командой",
	"difficulties": "Сложности за период", "barriers": "Что мешает работать и развиваться", "feedback": "Комментарий о работе за период", "attention": "Сложности / ситуации для внимания",
}

type Person struct {
	ID         string `json:"id"`
	Department string `json:"department"`
	MentorID   string `json:"mentorId"`
}

var People = []Person{
	{"СТ-001", "Технический блок", "Н-001"}, {"СТ-002", "Коммерческий блок", "Н-002"}, {"СТ-003", "Административный блок", "Н-003"}, {"СТ-004", "HR-блок", "Н-004"}, {"СТ-005", "IT", "Н-005"},
}

func Lookup(id string) (Person, bool) {
	for _, p := range People {
		if p.ID == id {
			return p, true
		}
	}
	return Person{}, false
}
func Fields(source string) []string {
	if source == "intern" {
		return InternFields
	}
	return MentorFields
}
func CommentFields(source string) []string {
	if source == "intern" {
		return []string{"difficulties", "barriers"}
	}
	return []string{"feedback", "attention"}
}

type Response struct {
	EventID    string            `json:"eventId"`
	ProgramID  string            `json:"programId"`
	InternID   string            `json:"internId"`
	MentorID   string            `json:"mentorId,omitempty"`
	Department string            `json:"department"`
	Month      int               `json:"month"`
	Source     string            `json:"source"`
	Scores     map[string]*int   `json:"scores"`
	Comments   map[string]string `json:"comments"`
	Raw        json.RawMessage   `json:"raw,omitempty"`
	ReceivedAt string            `json:"receivedAt,omitempty"`
}

func (r Response) Validate() error {
	if len(r.EventID) < 3 || len(r.EventID) > 160 || strings.TrimSpace(r.EventID) != r.EventID {
		return errors.New("eventId: требуется стабильный идентификатор длиной 3–160")
	}
	if r.ProgramID != Program {
		return errors.New("programId: неизвестная программа")
	}
	if strings.TrimSpace(r.InternID) == "" || len(r.InternID) > 100 {
		return errors.New("internId: обязательное поле до 100 байт")
	}
	if strings.TrimSpace(r.Department) == "" || len(r.Department) > 200 {
		return errors.New("department: обязательное поле до 200 байт")
	}
	if r.Month < 1 || r.Month > 6 {
		return errors.New("month: допустимо 1–6")
	}
	if r.Source != "intern" && r.Source != "mentor" {
		return errors.New("source: допустимо intern или mentor")
	}
	if r.Source == "mentor" && (r.MentorID == "" || len(r.MentorID) > 100) {
		return errors.New("mentorId: обязательное поле до 100 байт")
	}
	if r.Source == "intern" && r.MentorID != "" {
		return errors.New("mentorId: не требуется для ответа стажера")
	}
	fields := Fields(r.Source)
	if len(r.Scores) != len(fields) {
		return errors.New("scores: неверный набор показателей; пропуски передавайте null")
	}
	for _, k := range fields {
		v, exists := r.Scores[k]
		if !exists {
			return fmt.Errorf("scores.%s: обязательное поле", k)
		}
		if v != nil && (*v < 1 || *v > 5) {
			return fmt.Errorf("scores.%s: допустимо 1–5 или null", k)
		}
	}
	if len(r.Comments) != 2 {
		return errors.New("comments: требуются два текстовых поля")
	}
	for _, k := range CommentFields(r.Source) {
		v, ok := r.Comments[k]
		if !ok || len(v) > 10000 {
			return fmt.Errorf("comments.%s: обязательное поле, максимум 10000 байт", k)
		}
	}
	if len(r.Raw) > 100000 {
		return errors.New("raw: слишком большой источник")
	}
	return nil
}
func digest(v any) string {
	b, _ := json.Marshal(v)
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}
func (r Response) Hash() string { r.ReceivedAt = ""; r.Raw = nil; return digest(r) }

type Identity struct {
	Username string `json:"username"`
	Role     string `json:"role"`
	Subject  string `json:"subject"`
}
type Evidence struct {
	EventID string `json:"eventId"`
	Field   string `json:"field"`
	Value   string `json:"value"`
}
type Signal struct {
	ID       string     `json:"id"`
	Kind     string     `json:"kind"`
	Severity string     `json:"severity"`
	Title    string     `json:"title"`
	Evidence []Evidence `json:"evidence"`
}
type Analysis struct {
	SchemaVersion   string   `json:"schemaVersion"`
	RequestID       string   `json:"requestId"`
	Revision        string   `json:"revision"`
	Summary         string   `json:"summary"`
	Signals         []Signal `json:"signals"`
	Recommendations []string `json:"recommendations"`
}

func Revision(rs []Response) string {
	vals := make([]string, 0, len(rs))
	for _, r := range rs {
		vals = append(vals, r.Hash())
	}
	return digest(vals)
}
