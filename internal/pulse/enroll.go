package pulse

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
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
