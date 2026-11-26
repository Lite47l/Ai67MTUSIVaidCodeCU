package pulse

import (
	"context"
	"testing"
)

func TestAutomaticEnrollmentBothArrivalOrders(t *testing.T) {
	for _, mentorFirst := range []bool{false, true} {
		t.Run(map[bool]string{false: "intern_first", true: "mentor_first"}[mentorFirst], func(t *testing.T) {
			s := testStore(t)
			ctx := context.Background()
			a, b := fixture(t)[0], fixture(t)[30]
			a.InternID = "СТ-AUTO"
			a.Department = "IT"
			a.EventID = "auto-intern"
			b.InternID = "СТ-AUTO"
			b.Department = "IT"
			b.MentorID = "Н-AUTO"
			b.EventID = "auto-mentor"
			first, second := a, b
			if mentorFirst {
				first, second = b, a
			}
			if _, err := s.Import(ctx, []Response{first}); err == nil {
				t.Fatal("untrusted import enrolled a participant")
			}
			if _, err := s.ImportTrusted(ctx, []Response{first}); err != nil {
				t.Fatal(err)
			}
			p, err := s.Person(ctx, "СТ-AUTO")
			if err != nil {
				t.Fatal(err)
			}
			if !mentorFirst && p.MentorID != "" {
				t.Fatal("mentor invented")
			}
			if _, err = s.ImportTrusted(ctx, []Response{second}); err != nil {
				t.Fatal(err)
			}
			p, _ = s.Person(ctx, "СТ-AUTO")
			if p.MentorID != "Н-AUTO" {
				t.Fatal("mentor not bound")
			}
			for _, r := range []Response{first, second} {
				if n, err := s.ImportTrusted(ctx, []Response{r}); n != 0 || err != nil {
					t.Fatal("replay", n, err)
				}
			}
			b.Month = 2
			b.EventID = "other-month"
			b.MentorID = "Н-ATTACK"
			if _, err = s.ImportTrusted(ctx, []Response{b}); err == nil {
				t.Fatal("mentor reassignment accepted")
			}
			p, _ = s.Person(ctx, "СТ-AUTO")
			if p.MentorID != "Н-AUTO" {
				t.Fatal("binding changed")
			}
		})
	}
}
func TestAutomaticEnrollmentRollsBackWithResponse(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	a, b := fixture(t)[0], fixture(t)[1]
	a.InternID = "СТ-ROLLBACK"
	a.Department = "IT"
	a.EventID = "same-event"
	b.InternID = "СТ-ROLLBACK2"
	b.Department = "IT"
	b.EventID = "same-event"
	if _, err := s.ImportTrusted(ctx, []Response{a, b}); err == nil {
		t.Fatal("conflict accepted")
	}
	for _, id := range []string{a.InternID, b.InternID} {
		if _, err := s.Person(ctx, id); err == nil {
			t.Fatal("orphan registration", id)
		}
	}
	n := 0
	a.Scores["workload"] = &n
	if _, err := s.ImportTrusted(ctx, []Response{a}); err == nil {
		t.Fatal("invalid score accepted")
	}
	if _, err := s.Person(ctx, a.InternID); err == nil {
		t.Fatal("invalid response created a participant")
	}
}
