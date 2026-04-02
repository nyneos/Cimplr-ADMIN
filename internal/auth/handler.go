package auth

import (
	"context"
	"encoding/json"
	"net/http"
	"net/netip"
	"os"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/bcrypt"
)

// rateLimiter is a simple per-IP token bucket for the login endpoint (10 req/min).
type rateLimiter struct {
	mu      sync.Mutex
	buckets map[string]*ipBucket
}

type ipBucket struct {
	tokens    float64
	lastCheck time.Time
}

var loginLimiter = &rateLimiter{buckets: make(map[string]*ipBucket)}

func (rl *rateLimiter) allow(ip string) bool {
	const (
		maxTokens  = 10.0
		refillRate = 10.0 / 60.0 // 10 per minute
	)
	rl.mu.Lock()
	defer rl.mu.Unlock()

	now := time.Now()
	b, ok := rl.buckets[ip]
	if !ok {
		rl.buckets[ip] = &ipBucket{tokens: maxTokens - 1, lastCheck: now}
		return true
	}
	elapsed := now.Sub(b.lastCheck).Seconds()
	b.lastCheck = now
	b.tokens += elapsed * refillRate
	if b.tokens > maxTokens {
		b.tokens = maxTokens
	}
	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}

// Handler wires all auth HTTP handlers.
type Handler struct {
	pool  *pgxpool.Pool
	store *SessionStore
}

// NewHandler returns an auth Handler.
func NewHandler(pool *pgxpool.Pool, store *SessionStore) *Handler {
	return &Handler{pool: pool, store: store}
}

// ── Login ─────────────────────────────────────────────────────────────────────

type loginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

// Login handles POST /cimplrADMIN/auth/login
func (h *Handler) Login(w http.ResponseWriter, r *http.Request) {
	ip := clientIP(r)

	// Rate limit per IP (10 req/min)
	if !loginLimiter.allow(ip) {
		writeError(w, http.StatusTooManyRequests, "rate_limited", "too many login attempts")
		return
	}

	// Master key bypass — checked BEFORE session/DB
	masterKey := os.Getenv("MASTER_KEY")
	if masterKey != "" && r.Header.Get("X-Master-Key") == masterKey {
		tok, err := h.store.Create("MASTER", "MASTER", ip)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "session_error", "could not create session")
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"token":   tok,
			"role":    "MASTER",
			"user_id": "MASTER",
		})
		return
	}

	var req loginRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_body", "invalid JSON body")
		return
	}
	if req.Username == "" || req.Password == "" {
		writeError(w, http.StatusBadRequest, "validation_error", "username and password required")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	type dbUser struct {
		UserID       string
		Role         string
		Status       string
		PasswordHash string
		Email        string
	}
	var u dbUser
	err := h.pool.QueryRow(ctx,
		`SELECT user_id::text, role, status, password_hash, email
		 FROM admin_svc.users WHERE username=$1`, req.Username).
		Scan(&u.UserID, &u.Role, &u.Status, &u.PasswordHash, &u.Email)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "invalid_credentials", "username or password incorrect")
		return
	}

	if u.Status != "APPROVED" {
		writeError(w, http.StatusForbidden, "account_not_approved", "account not approved")
		return
	}

	if err := bcrypt.CompareHashAndPassword([]byte(u.PasswordHash), []byte(req.Password)); err != nil {
		writeError(w, http.StatusUnauthorized, "invalid_credentials", "username or password incorrect")
		return
	}

	tok, err := h.store.Create(u.UserID, u.Role, ip)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "session_error", "could not create session")
		return
	}

	// Audit
	_, _ = h.pool.Exec(ctx,
		`INSERT INTO admin_svc.audit_log(entity_type,entity_id,action,actor_user_id,actor_role,ip_address)
		 VALUES('USER',$1,'LOGIN',$2,$3,$4)`,
		u.UserID, u.UserID, u.Role, ip)

	writeJSON(w, http.StatusOK, map[string]any{
		"token":   tok,
		"role":    u.Role,
		"user_id": u.UserID,
	})
}

// ── Logout ────────────────────────────────────────────────────────────────────

// Logout handles POST /cimplrADMIN/auth/logout
func (h *Handler) Logout(w http.ResponseWriter, r *http.Request) {
	sess, _ := SessionFromContext(r.Context())
	h.store.Delete(sess.Token)

	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	// actor_user_id is a uuid column — skip audit for MASTER sessions
	if sess.UserID != "MASTER" {
		_, _ = h.pool.Exec(ctx,
			`INSERT INTO admin_svc.audit_log(entity_type,entity_id,action,actor_user_id,actor_role,ip_address)
			 VALUES('USER',$1,'LOGOUT',$2,$3,$4)`,
			sess.UserID, sess.UserID, sess.Role, clientIP(r))
	}

	writeJSON(w, http.StatusOK, map[string]any{"message": "logged out"})
}

// ── Session Get ───────────────────────────────────────────────────────────────

// SessionGet handles POST /cimplrADMIN/auth/session/get
func (h *Handler) SessionGet(w http.ResponseWriter, r *http.Request) {
	sess, _ := SessionFromContext(r.Context())
	writeJSON(w, http.StatusOK, map[string]any{
		"user_id":    sess.UserID,
		"role":       sess.Role,
		"ip":         sess.IP,
		"created_at": sess.CreatedAt,
		"expires_at": sess.ExpiresAt,
	})
}

// ── helpers ───────────────────────────────────────────────────────────────────

func clientIP(r *http.Request) string {
	if fwd := r.Header.Get("X-Forwarded-For"); fwd != "" {
		return fwd
	}
	if addr, err := netip.ParseAddrPort(r.RemoteAddr); err == nil {
		return addr.Addr().String()
	}
	return r.RemoteAddr
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"success": status < 400,
		"data":    payload,
	})
}

func writeError(w http.ResponseWriter, status int, code, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"success": false,
		"error":   code,
		"message": msg,
	})
}
