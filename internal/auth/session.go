package auth

import (
	"crypto/rand"
	"encoding/hex"
	"sync"
	"time"
)

const sessionTTL = 8 * time.Hour

// Session holds the state for one authenticated session.
type Session struct {
	Token     string
	UserID    string
	Role      string
	IP        string
	CreatedAt time.Time
	ExpiresAt time.Time
}

// SessionStore is a two-index in-process store backed by sync.Map.
// byToken  → *Session
// byUserID → token string  (single-session enforcement)
type SessionStore struct {
	byToken  sync.Map
	byUserID sync.Map
}

// NewSessionStore returns an initialised SessionStore.
func NewSessionStore() *SessionStore {
	return &SessionStore{}
}

// Create builds a new session, evicting any existing session for the same user.
// Returns the new token.
func (s *SessionStore) Create(userID, role, ip string) (string, error) {
	// Evict the previous session for this user (single-session rule).
	if oldToken, ok := s.byUserID.Load(userID); ok {
		s.byToken.Delete(oldToken)
	}

	tok, err := generateToken()
	if err != nil {
		return "", err
	}

	now := time.Now().UTC()
	sess := &Session{
		Token:     tok,
		UserID:    userID,
		Role:      role,
		IP:        ip,
		CreatedAt: now,
		ExpiresAt: now.Add(sessionTTL),
	}

	s.byToken.Store(tok, sess)
	s.byUserID.Store(userID, tok)
	return tok, nil
}

// Get retrieves a session by token. Returns nil,false if not found or expired.
func (s *SessionStore) Get(token string) (*Session, bool) {
	v, ok := s.byToken.Load(token)
	if !ok {
		return nil, false
	}
	sess := v.(*Session)
	if time.Now().UTC().After(sess.ExpiresAt) {
		s.delete(sess)
		return nil, false
	}
	return sess, true
}

// Delete removes a session by token.
func (s *SessionStore) Delete(token string) {
	v, ok := s.byToken.Load(token)
	if !ok {
		return
	}
	s.delete(v.(*Session))
}

func (s *SessionStore) delete(sess *Session) {
	s.byToken.Delete(sess.Token)
	s.byUserID.Delete(sess.UserID)
}

// Cleanup sweeps all expired sessions. Call on a time.Ticker.
func (s *SessionStore) Cleanup() {
	now := time.Now().UTC()
	s.byToken.Range(func(k, v any) bool {
		sess := v.(*Session)
		if now.After(sess.ExpiresAt) {
			s.delete(sess)
		}
		return true
	})
}

func generateToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
