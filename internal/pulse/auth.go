package pulse

import (
	"context"
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"time"
)

func randomID() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}
func passwordHash(password, salt string) string {
	b, err := pbkdf2.Key(sha256.New, password, []byte(salt), 120000, 32)
	if err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}
func secureEqual(a, b string) bool { return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1 }
func (s *Store) AddUser(ctx context.Context, name, password, role, subject string) error {
	if len(password) < 12 {
		return errors.New("пароль должен содержать минимум 12 символов")
	}
	if name == "" || len(name) > 100 {
		return errors.New("неверное имя пользователя")
	}
	valid := role == "hr" && subject == ""
	if role == "intern" {
		_, err := s.Person(ctx, subject)
		valid = err == nil
	}
	if role == "mentor" {
		var count int
		err := s.DB.QueryRowContext(ctx, "SELECT count(*) FROM people WHERE mentor_id=?", subject).Scan(&count)
		valid = err == nil && count > 0
	}
	if !valid {
		return errors.New("неверная роль или привязка")
	}
	salt := randomID()
	_, err := s.DB.ExecContext(ctx, "INSERT INTO users VALUES(?,?,?,?,?)", name, salt, passwordHash(password, salt), role, subject)
	return err
}
func (s *Store) Login(ctx context.Context, username, password string) (string, Identity, error) {
	var salt, hash string
	var who Identity
	err := s.DB.QueryRowContext(ctx, "SELECT username,salt,password_hash,role,subject FROM users WHERE username=?", username).Scan(&who.Username, &salt, &hash, &who.Role, &who.Subject)
	if errors.Is(err, sql.ErrNoRows) {
		_ = passwordHash(password, "dummy-user-salt")
		return "", who, ErrNotFound
	}
	if err != nil {
		return "", who, err
	}
	if !secureEqual(hash, passwordHash(password, salt)) {
		return "", who, ErrNotFound
	}
	token := randomID()
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return "", who, err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, "DELETE FROM sessions WHERE expires_at<?", time.Now().Unix()); err != nil {
		return "", who, err
	}
	if _, err = tx.ExecContext(ctx, "INSERT INTO sessions VALUES(?,?,?)", digest(token), username, time.Now().Add(8*time.Hour).Unix()); err != nil {
		return "", who, err
	}
	err = tx.Commit()
	return token, who, err
}
func (s *Store) Session(ctx context.Context, r *http.Request) (Identity, error) {
	var who Identity
	c, err := r.Cookie("mgts_session")
	if err != nil {
		return who, ErrNotFound
	}
	err = s.DB.QueryRowContext(ctx, "SELECT u.username,u.role,u.subject FROM sessions s JOIN users u ON s.username=u.username WHERE s.token_hash=? AND s.expires_at>?", digest(c.Value), time.Now().Unix()).Scan(&who.Username, &who.Role, &who.Subject)
	return who, err
}

type identityKey struct{}

func identity(r *http.Request) Identity {
	v, _ := r.Context().Value(identityKey{}).(Identity)
	return v
}

type loginBucket struct {
	Count int
	Start time.Time
}
type limiter struct {
	mu      sync.Mutex
	buckets map[string]loginBucket
}

func (l *limiter) Allow(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	if l.buckets == nil {
		l.buckets = map[string]loginBucket{}
	}
	for k, b := range l.buckets {
		if now.Sub(b.Start) > time.Minute {
			delete(l.buckets, k)
		}
	}
	b := l.buckets[key]
	if b.Start.IsZero() {
		b.Start = now
	}
	b.Count++
	l.buckets[key] = b
	return b.Count <= 20
}
func (s *Store) DemoUsers(ctx context.Context) error {
	if err := s.AddUser(ctx, "hr", "Mgts-demo-2026!", "hr", ""); err != nil {
		return fmt.Errorf("demo hr: %w", err)
	}
	for i, p := range People {
		if err := s.AddUser(ctx, fmt.Sprintf("intern%d", i+1), "Mgts-demo-2026!", "intern", p.ID); err != nil {
			return err
		}
		if err := s.AddUser(ctx, fmt.Sprintf("mentor%d", i+1), "Mgts-demo-2026!", "mentor", p.MentorID); err != nil {
			return err
		}
	}
	return nil
}
