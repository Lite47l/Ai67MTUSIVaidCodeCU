package pulse

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"strings"
)

// Called inside the response transaction only for authenticated integrations.
// An intern can arrive first; a later mentor form fills the empty binding.
func autoEnroll(ctx context.Context, tx *sql.Tx, r Response) error {
	var department, mentor string
	err := tx.QueryRowContext(ctx, "SELECT department,mentor_id FROM people WHERE id=?", r.InternID).Scan(&department, &mentor)
	if errors.Is(err, sql.ErrNoRows) {
		_, err = tx.ExecContext(ctx, "INSERT INTO people(id,department,mentor_id) VALUES(?,?,?)", r.InternID, r.Department, r.MentorID)
		return err
	}
	if err != nil {
		return err
	}
	if department != r.Department {
		return fmt.Errorf("%w: подразделение не соответствует участнику", ErrValidation)
	}
	if r.Source == "mentor" {
		if mentor != "" && mentor != r.MentorID {
			return fmt.Errorf("%w: наставник не соответствует участнику", ErrValidation)
		}
		if mentor == "" {
			_, err = tx.ExecContext(ctx, "UPDATE people SET mentor_id=? WHERE id=? AND mentor_id=''", r.MentorID, r.InternID)
		}
	}
	return err
}

func (s *Server) enroll(w http.ResponseWriter, r *http.Request) {
	var p Person
	if !decode(w, r, &p) {
		return
	}
	p.ID = strings.TrimSpace(p.ID)
	p.Department = strings.TrimSpace(p.Department)
	p.MentorID = strings.TrimSpace(p.MentorID)
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
