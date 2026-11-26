package pulse

import (
	"net/http"
	"strings"
)

func (s *Server) enroll(w http.ResponseWriter, r *http.Request) {
	var p Person
	if !decode(w, r, &p) {
		return
	}
	if strings.TrimSpace(p.ID) == "" || len(p.ID) > 100 || strings.TrimSpace(p.Department) == "" || len(p.Department) > 200 || strings.TrimSpace(p.MentorID) == "" || len(p.MentorID) > 100 {
		problem(w, 422, "Нужны id, department, mentorId допустимой длины")
		return
	}
	// Membership is immutable in this prototype: reassignment needs an audited workflow.
	tx, err := s.Store.DB.BeginTx(r.Context(), nil)
	if err != nil {
		s.internal(w, err)
		return
	}
	defer tx.Rollback()
	var n int
	if err = tx.QueryRowContext(r.Context(), "SELECT count(*) FROM people WHERE id=?", p.ID).Scan(&n); err != nil {
		s.internal(w, err)
		return
	}
	if n > 0 {
		problem(w, 409, "Стажер уже зарегистрирован")
		return
	}
	if _, err = tx.ExecContext(r.Context(), "INSERT INTO people VALUES(?,?,?)", p.ID, p.Department, p.MentorID); err != nil {
		s.internal(w, err)
		return
	}
	if err = tx.Commit(); err != nil {
		s.internal(w, err)
		return
	}
	writeJSON(w, 201, p)
}
