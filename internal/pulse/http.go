package pulse

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"io"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"
)

type Server struct {
	HRTasks                                                                                        *HRTaskClient
	Store                                                                                          *Store
	AgentURL, WebhookSecret, TallySecret, InternFormID, MentorFormID, InternFormURL, MentorFormURL string
	SecureCookie                                                                                   bool
	loginLimit                                                                                     limiter
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		slog.Error("encode response", "error", err)
	}
}
func problem(w http.ResponseWriter, code int, message string) {
	writeJSON(w, code, map[string]any{"error": map[string]any{"code": http.StatusText(code), "message": message}})
}
func decode(w http.ResponseWriter, r *http.Request, dst any) bool {
	if !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
		problem(w, 415, "Требуется application/json")
		return false
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	d := json.NewDecoder(r.Body)
	d.DisallowUnknownFields()
	if err := d.Decode(dst); err != nil {
		problem(w, 400, "Некорректный JSON или неизвестные поля")
		return false
	}
	var x any
	if d.Decode(&x) != io.EOF {
		problem(w, 400, "Ожидается один JSON-объект")
		return false
	}
	return true
}
func (s *Server) internal(w http.ResponseWriter, err error) {
	slog.Error("request failed", "error", err)
	problem(w, 500, "Внутренняя ошибка сервиса")
}
func (s *Server) auth(roles string, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		who, err := s.Store.Session(r.Context(), r)
		if err != nil {
			if !errors.Is(err, sql.ErrNoRows) && !errors.Is(err, ErrNotFound) {
				s.internal(w, err)
				return
			}
			problem(w, 401, "Войдите в систему")
			return
		}
		if !strings.Contains(","+roles+",", ","+who.Role+",") {
			problem(w, 403, "Недостаточно прав")
			return
		}
		next(w, r.WithContext(context.WithValue(r.Context(), identityKey{}, who)))
	}
}
func (s *Server) Handler(assets fs.FS) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, 200, map[string]string{"status": "alive"}) })
	mux.HandleFunc("GET /readyz", func(w http.ResponseWriter, r *http.Request) {
		if s.Store.Ready(r.Context()) != nil {
			problem(w, 503, "База не готова")
			return
		}
		writeJSON(w, 200, map[string]string{"status": "ready"})
	})
	mux.HandleFunc("POST /api/v1/auth/login", s.login)
	mux.HandleFunc("POST /api/v1/auth/logout", s.auth("hr,mentor,intern", s.logout))
	mux.HandleFunc("GET /api/v1/me", s.auth("hr,mentor,intern", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, 200, identity(r)) }))
	mux.HandleFunc("GET /api/v1/schema", s.auth("hr,mentor,intern", func(w http.ResponseWriter, r *http.Request) {
		who := identity(r)
		all, err := s.Store.People(r.Context())
		if err != nil {
			s.internal(w, err)
			return
		}
		people := []Person{}
		for _, p := range all {
			if s.Store.Allowed(r.Context(), who, p.ID) {
				people = append(people, p)
			}
		}
		writeJSON(w, 200, map[string]any{"programId": Program, "people": people, "internFields": InternFields, "mentorFields": MentorFields, "labels": Labels, "internFormUrl": s.InternFormURL, "mentorFormUrl": s.MentorFormURL})
	}))
	mux.HandleFunc("POST /api/v1/hr/enrollments", s.auth("hr", s.enroll))
	mux.HandleFunc("POST /api/v1/hr/interns/{id}/recommendations/{recommendation}/confirm", s.auth("hr", s.confirmRecommendation))
	mux.HandleFunc("GET /api/v1/hr/overview", s.auth("hr", s.overview))
	mux.HandleFunc("GET /api/v1/hr/integrations", s.auth("hr", s.integrations))
	mux.HandleFunc("GET /api/v1/hr/export", s.auth("hr", s.export))
	mux.HandleFunc("GET /api/v1/hr/actions", s.auth("hr", s.listActions))
	mux.HandleFunc("POST /api/v1/hr/actions", s.auth("hr", s.createAction))
	mux.HandleFunc("PATCH /api/v1/hr/actions/{id}", s.auth("hr", s.closeAction))
	for _, role := range []string{"hr", "mentor", "intern"} {
		mux.HandleFunc("GET /api/v1/"+role+"/interns/{id}", s.auth(role, s.profile))
	}
	mux.HandleFunc("POST /api/v1/responses", s.auth("hr,mentor,intern", s.response))
	mux.HandleFunc("POST /api/v1/mentor/interns/{id}/feedback", s.auth("mentor", s.publish))
	mux.HandleFunc("POST /api/v1/hr/interns/{id}/analysis", s.auth("hr", s.analyze))
	mux.HandleFunc("POST /api/v1/hr/jobs/{id}/retry", s.auth("hr", s.retry))
	mux.HandleFunc("POST /api/v1/integrations/responses", s.webhook)
	mux.HandleFunc("POST /api/v1/integrations/tally", s.tally)
	mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) { problem(w, 404, "Ресурс не найден") })
	files := http.FileServer(http.FS(assets))
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			problem(w, 405, "Метод не поддерживается")
			return
		}
		switch r.URL.Path {
		case "/", "/hr", "/mentor", "/intern":
			b, err := fs.ReadFile(assets, "index.html")
			if err != nil {
				s.internal(w, err)
				return
			}
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = w.Write(b)
		default:
			files.ServeHTTP(w, r)
		}
	})
	protected := http.NewCrossOriginProtection().Handler(mux)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestID := randomID()[:16]
		w.Header().Set("X-Request-ID", requestID)
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "same-origin")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self' data:; connect-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'self'")
		start := time.Now()
		defer func() {
			if v := recover(); v != nil {
				slog.Error("panic", "requestId", requestID)
				problem(w, 500, "Внутренняя ошибка")
			}
			slog.Info("http", "requestId", requestID, "method", r.Method, "durationMs", time.Since(start).Milliseconds())
		}()
		protected.ServeHTTP(w, r)
	})
}
func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	host, _, _ := net.SplitHostPort(r.RemoteAddr)
	if !s.loginLimit.Allow(host) {
		problem(w, 429, "Слишком много попыток. Повторите через минуту")
		return
	}
	var in struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if !decode(w, r, &in) {
		return
	}
	if len(in.Username) > 100 || len(in.Password) > 256 {
		problem(w, 400, "Слишком длинное имя или пароль")
		return
	}
	token, who, err := s.Store.Login(r.Context(), in.Username, in.Password)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			problem(w, 401, "Неверное имя пользователя или пароль")
		} else {
			s.internal(w, err)
		}
		return
	}
	http.SetCookie(w, &http.Cookie{Name: "mgts_session", Value: token, Path: "/", HttpOnly: true, Secure: s.SecureCookie, SameSite: http.SameSiteStrictMode, MaxAge: 28800})
	writeJSON(w, 200, who)
}
func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	c, _ := r.Cookie("mgts_session")
	if c != nil {
		if _, err := s.Store.DB.ExecContext(r.Context(), "DELETE FROM sessions WHERE token_hash=?", digest(c.Value)); err != nil {
			s.internal(w, err)
			return
		}
	}
	http.SetCookie(w, &http.Cookie{Name: "mgts_session", Path: "/", HttpOnly: true, Secure: s.SecureCookie, SameSite: http.SameSiteStrictMode, MaxAge: -1})
	writeJSON(w, 200, map[string]bool{"ok": true})
}
func selectedMonth(r *http.Request) (int, error) {
	v := r.URL.Query().Get("month")
	if v == "" {
		return 6, nil
	}
	m, err := strconv.Atoi(v)
	if err != nil || m < 1 || m > 6 {
		return 0, errors.New("Месяц должен быть от 1 до 6")
	}
	return m, nil
}
func (s *Server) scope(w http.ResponseWriter, r *http.Request) (string, bool) {
	id := r.PathValue("id")
	if _, err := s.Store.Person(r.Context(), id); err != nil {
		problem(w, 404, "Стажер не найден")
		return "", false
	}
	if !s.Store.Allowed(r.Context(), identity(r), id) {
		problem(w, 403, "Нет доступа к этому стажеру")
		return "", false
	}
	return id, true
}
func (s *Server) profile(w http.ResponseWriter, r *http.Request) {
	id, ok := s.scope(w, r)
	if !ok {
		return
	}
	month, err := selectedMonth(r)
	if err != nil {
		problem(w, 400, err.Error())
		return
	}
	rs, err := s.Store.Responses(r.Context(), id)
	if err != nil {
		s.internal(w, err)
		return
	}
	for i := range rs {
		rs[i].Raw = nil
	}
	fb, err := s.Store.Feedback(r.Context(), id)
	if err != nil {
		s.internal(w, err)
		return
	}
	a, state, err := s.Store.Analysis(r.Context(), id, rs)
	if err != nil {
		s.internal(w, err)
		return
	}
	if s.AgentURL == "" {
		state = "not_connected"
	}
	p, _ := s.Store.Person(r.Context(), id)
	rules := Rules(rs, month)
	recommendations := monthRecommendations(id, month, rs, a, rules)
	revision := Revision(rs)
	hidden := []Signal{}
	plans := []ConversationPlan{}
	if identity(r).Role == "hr" {
		hidden = append(hidden, hiddenSignalsForMonth(a, rs, month)...)
		plans = conversationPlansForMonth(a, rs, month)
	} else {
		a = nil
		state = ""
		if identity(r).Role == "mentor" {
			for i := range rs {
				if rs[i].Source == "intern" {
					rs[i].Comments = map[string]string{}
				}
			}
		}
	}
	timeline := []any{}
	for m := 1; m <= 6; m++ {
		timeline = append(timeline, map[string]any{"month": m, "signals": Rules(rs, m)})
	}
	// Original mentor questionnaire comments are visible to its own intern by product policy.
	active := append(append([]Signal{}, rules...), hidden...)
	internStats, mentorStats := ScoresForMonth(rs, month)

	payload := map[string]any{"recommendations": []Recommendation{}, "hrTaskConfigured": s.HRTasks != nil}
	if identity(r).Role == "hr" {
		for i := range recommendations {
			recommendations[i].Delivery, err = s.Store.taskDelivery(r.Context(), recommendations[i].ID)
			if err != nil {
				s.internal(w, err)
				return
			}
		}
		payload["recommendations"] = recommendations
	} else if identity(r).Role == "mentor" {
		for _, item := range recommendations {
			if item.Audience == "mentor" {
				payload["recommendations"] = append(payload["recommendations"].([]Recommendation), item)
			}
		}
	}
	if identity(r).Role == "intern" {
		delete(payload, "recommendations")
		delete(payload, "hrTaskConfigured")
	}
	for key, value := range map[string]any{"person": p, "responses": rs, "feedback": fb, "month": month, "internStats": internStats, "mentorStats": mentorStats, "ruleSignals": rules, "hiddenSignals": hidden, "conversationPlans": plans, "timeline": timeline, "status": profileStatus(rs, month, active), "ai": a, "aiState": state, "revision": revision} {
		payload[key] = value
	}
	writeJSON(w, 200, payload)
}
func (s *Server) overview(w http.ResponseWriter, r *http.Request) {
	month, err := selectedMonth(r)
	if err != nil {
		problem(w, 400, err.Error())
		return
	}
	all, err := s.Store.Responses(r.Context(), "")
	if err != nil {
		s.internal(w, err)
		return
	}
	people, err := s.Store.People(r.Context())
	if err != nil {
		s.internal(w, err)
		return
	}
	items := []any{}
	submitted, expected := 0, len(people)*2
	critical, attention, positive, missing := 0, 0, 0, 0
	for _, p := range people {
		rs := []Response{}
		for _, r := range all {
			if r.InternID == p.ID {
				rs = append(rs, r)
			}
		}
		intern, mentor := false, false
		internStats, mentorStats := ScoresForMonth(rs, month)
		complete := internStats.Answered == internStats.Expected && mentorStats.Answered == mentorStats.Expected
		for _, r := range rs {
			if r.Month != month {
				continue
			}
			if r.Source == "intern" {
				intern = true
			} else {
				mentor = true
			}
		}
		if intern {
			submitted++
		}
		if mentor {
			submitted++
		}
		signals := Rules(rs, month)
		if len(rs) > 0 {
			a, _, err := s.Store.Analysis(r.Context(), p.ID, rs)
			if err != nil {
				s.internal(w, err)
				return
			}
			signals = append(signals, hiddenSignalsForMonth(a, rs, month)...)
		}
		st := status(signals)
		if !intern || !mentor || !complete {
			missing++
			if st == "positive" {
				st = "missing"
			}
		}
		switch st {
		case "critical":
			critical++
		case "attention":
			attention++
		case "positive":
			positive++
		}
		items = append(items, map[string]any{"person": p, "internSubmitted": intern, "mentorSubmitted": mentor, "scoresComplete": complete, "internAverage": internStats.Average, "mentorAverage": mentorStats.Average, "internStats": internStats, "mentorStats": mentorStats, "status": st, "signals": signals})
	}
	var closed, totalActions int
	err = s.Store.DB.QueryRowContext(r.Context(), "SELECT count(*),coalesce(sum(CASE WHEN status='closed' THEN 1 ELSE 0 END),0) FROM actions").Scan(&totalActions, &closed)
	if err != nil {
		s.internal(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"month": month, "items": items, "totalResponses": len(all), "submitted": submitted, "expected": expected, "critical": critical, "attention": attention, "positive": positive, "incomplete": missing, "actions": totalActions, "closedActions": closed, "agentConnected": s.AgentURL != ""})
}
func (s *Server) ingest(w http.ResponseWriter, r *http.Request, row Response, source string) {
	if err := row.Validate(); err != nil {
		_ = s.Store.RecordDelivery(r.Context(), row.EventID, "rejected", source)
		problem(w, 422, err.Error())
		return
	}
	var n int
	var err error
	if source == "webhook" || source == "tally" {
		n, err = s.Store.ImportTrusted(r.Context(), []Response{row})
	} else {
		n, err = s.Store.Import(r.Context(), []Response{row})
	}
	if err != nil {
		if errors.Is(err, ErrValidation) {
			_ = s.Store.RecordDelivery(r.Context(), row.EventID, "rejected", source)
			problem(w, 422, err.Error())
		} else if errors.Is(err, ErrConflict) {
			_ = s.Store.RecordDelivery(r.Context(), row.EventID, "conflict", source)
			problem(w, 409, "Ответ за этот период или измененный eventId уже существует")
		} else {
			s.internal(w, err)
		}
		return
	}
	// Retry-safe even if the response committed but queuing failed: redelivery queues again.
	if _, err = s.Store.Queue(r.Context(), row.InternID); err != nil {
		s.internal(w, err)
		return
	}
	state := "accepted"
	code := 201
	if n == 0 {
		state = "duplicate"
		code = 200
	}
	if err = s.Store.RecordDelivery(r.Context(), row.EventID, state, source); err != nil {
		s.internal(w, err)
		return
	}
	writeJSON(w, code, map[string]any{"eventId": row.EventID, "status": state, "created": n})
}
func (s *Server) response(w http.ResponseWriter, r *http.Request) {
	var row Response
	if !decode(w, r, &row) {
		return
	}
	who := identity(r)
	if !s.Store.Allowed(r.Context(), who, row.InternID) || (who.Role != "hr" && who.Role != row.Source) || (who.Role == "mentor" && who.Subject != row.MentorID) {
		problem(w, 403, "Нельзя отправить ответ от этого участника")
		return
	}
	row.Raw = nil
	row.ReceivedAt = ""
	s.ingest(w, r, row, "portal")
}
func (s *Server) publish(w http.ResponseWriter, r *http.Request) {
	id, ok := s.scope(w, r)
	if !ok {
		return
	}
	var in struct {
		Month int    `json:"month"`
		Text  string `json:"text"`
	}
	if !decode(w, r, &in) {
		return
	}
	if in.Month < 1 || in.Month > 6 || strings.TrimSpace(in.Text) == "" || len(in.Text) > 10000 {
		problem(w, 422, "Нужны месяц 1–6 и текст до 10000 байт")
		return
	}
	f := Feedback{randomID(), id, identity(r).Subject, in.Month, strings.TrimSpace(in.Text), stamp()}
	if _, err := s.Store.DB.ExecContext(r.Context(), "INSERT INTO feedback VALUES(?,?,?,?,?,?)", f.ID, f.InternID, f.MentorID, f.Month, f.Text, f.PublishedAt); err != nil {
		s.internal(w, err)
		return
	}
	writeJSON(w, 201, f)
}
func (s *Server) analyze(w http.ResponseWriter, r *http.Request) {
	id, ok := s.scope(w, r)
	if !ok {
		return
	}
	if s.AgentURL == "" {
		problem(w, 503, "Python-агент не подключен: задайте AGENT_URL")
		return
	}
	job, err := s.Store.RequestAnalysis(r.Context(), id)
	if err != nil {
		s.internal(w, err)
		return
	}
	writeJSON(w, 202, map[string]string{"jobId": job})
}
func (s *Server) retry(w http.ResponseWriter, r *http.Request) {
	if s.AgentURL == "" {
		problem(w, 503, "Агент не подключен")
		return
	}
	res, err := s.Store.DB.ExecContext(r.Context(), "UPDATE jobs SET state='pending',attempts=0,next_at=0,last_error='' WHERE id=? AND state='failed'", r.PathValue("id"))
	if err != nil {
		s.internal(w, err)
		return
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		problem(w, 409, "Задание не найдено или не находится в failed")
		return
	}
	writeJSON(w, 202, map[string]bool{"queued": true})
}
func (s *Server) export(w http.ResponseWriter, r *http.Request) {
	rs, err := s.Store.Responses(r.Context(), "")
	if err != nil {
		s.internal(w, err)
		return
	}
	for i := range rs {
		rs[i].Raw = nil
	}
	w.Header().Set("Content-Disposition", "attachment; filename=mgts-responses.json")
	writeJSON(w, 200, rs)
}
func (s *Server) integrations(w http.ResponseWriter, r *http.Request) {
	deliveries := []any{}
	rows, err := s.Store.DB.QueryContext(r.Context(), "SELECT d.event_id,d.received_at,d.status,d.source,r.intern_id,r.month,r.source FROM deliveries d LEFT JOIN responses r ON r.event_id=d.event_id ORDER BY d.id DESC LIMIT 50")
	if err != nil {
		s.internal(w, err)
		return
	}
	for rows.Next() {
		var event, at, st, src string
		var intern, respondent sql.NullString
		var month sql.NullInt64
		if err = rows.Scan(&event, &at, &st, &src, &intern, &month, &respondent); err != nil {
			break
		}
		item := map[string]any{"eventId": event, "receivedAt": at, "status": st, "source": src, "internId": nil, "month": nil, "respondent": nil}
		if intern.Valid {
			item["internId"] = intern.String
		}
		if month.Valid {
			item["month"] = month.Int64
		}
		if respondent.Valid {
			item["respondent"] = respondent.String
		}
		deliveries = append(deliveries, item)
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		s.internal(w, err)
		return
	}
	jobs := []any{}
	rows, err = s.Store.DB.QueryContext(r.Context(), "SELECT id,intern_id,state,attempts,last_error,created_at FROM jobs ORDER BY created_at DESC LIMIT 50")
	if err != nil {
		s.internal(w, err)
		return
	}
	defer rows.Close()
	for rows.Next() {
		var id, intern, st, msg, at string
		var attempts int
		if err = rows.Scan(&id, &intern, &st, &attempts, &msg, &at); err != nil {
			s.internal(w, err)
			return
		}
		jobs = append(jobs, map[string]any{"id": id, "internId": intern, "state": st, "attempts": attempts, "lastError": msg, "createdAt": at})
	}
	if err = rows.Err(); err != nil {
		s.internal(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"agentConfigured": s.AgentURL != "", "webhookConfigured": s.WebhookSecret != "", "tallyConfigured": s.TallySecret != "" && s.InternFormID != "" && s.MentorFormID != "", "deliveries": deliveries, "jobs": jobs})
}

type Action struct {
	ID        string  `json:"id"`
	InternID  string  `json:"internId"`
	Owner     string  `json:"owner"`
	Note      string  `json:"note"`
	Status    string  `json:"status"`
	CreatedAt string  `json:"createdAt"`
	ClosedAt  *string `json:"closedAt"`
}

func (s *Server) listActions(w http.ResponseWriter, r *http.Request) {
	rows, err := s.Store.DB.QueryContext(r.Context(), "SELECT id,intern_id,owner,note,status,created_at,closed_at FROM actions ORDER BY created_at DESC LIMIT 200")
	if err != nil {
		s.internal(w, err)
		return
	}
	defer rows.Close()
	out := []Action{}
	for rows.Next() {
		var a Action
		if err = rows.Scan(&a.ID, &a.InternID, &a.Owner, &a.Note, &a.Status, &a.CreatedAt, &a.ClosedAt); err != nil {
			s.internal(w, err)
			return
		}
		out = append(out, a)
	}
	if err = rows.Err(); err != nil {
		s.internal(w, err)
		return
	}
	writeJSON(w, 200, out)
}
func (s *Server) createAction(w http.ResponseWriter, r *http.Request) {
	var in struct {
		InternID string `json:"internId"`
		Note     string `json:"note"`
	}
	if !decode(w, r, &in) {
		return
	}
	if _, err := s.Store.Person(r.Context(), in.InternID); err != nil || strings.TrimSpace(in.Note) == "" || len(in.Note) > 5000 {
		problem(w, 422, "Нужны известный стажер и описание до 5000 байт")
		return
	}
	a := Action{ID: randomID(), InternID: in.InternID, Owner: identity(r).Username, Note: strings.TrimSpace(in.Note), Status: "open", CreatedAt: stamp()}
	if _, err := s.Store.DB.ExecContext(r.Context(), "INSERT INTO actions(id,intern_id,owner,note,status,created_at) VALUES(?,?,?,?,?,?)", a.ID, a.InternID, a.Owner, a.Note, a.Status, a.CreatedAt); err != nil {
		s.internal(w, err)
		return
	}
	writeJSON(w, 201, a)
}
func (s *Server) closeAction(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Status string `json:"status"`
	}
	if !decode(w, r, &in) {
		return
	}
	if in.Status != "closed" {
		problem(w, 422, "Допустим status=closed")
		return
	}
	res, err := s.Store.DB.ExecContext(r.Context(), "UPDATE actions SET status='closed',closed_at=? WHERE id=? AND status='open'", stamp(), r.PathValue("id"))
	if err != nil {
		s.internal(w, err)
		return
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		problem(w, 409, "Мера не найдена или уже закрыта")
		return
	}
	writeJSON(w, 200, map[string]string{"status": "closed"})
}
