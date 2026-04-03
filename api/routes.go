package api

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"runtime/debug"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"CimplrCorpSaas/admin/internal/access"
	"CimplrCorpSaas/admin/internal/alert"
	"CimplrCorpSaas/admin/internal/auth"
	"CimplrCorpSaas/admin/internal/dashboard"
	"CimplrCorpSaas/admin/internal/deployment"
	"CimplrCorpSaas/admin/internal/licence"
	applogger "CimplrCorpSaas/admin/internal/logger"
	"CimplrCorpSaas/admin/internal/notification"
	"CimplrCorpSaas/admin/internal/user"
)

// Dependencies holds all wired-up handlers.
type Dependencies struct {
	Pool         *pgxpool.Pool
	SessionStore *auth.SessionStore
	Auth         *auth.Handler
	User         *user.Handler
	Deployment   *deployment.Handler
	Access       *access.Handler
	Licence      *licence.Handler
	Notification *notification.Handler
	Dashboard    *dashboard.Handler
	Alert        *alert.Handler
}

// NewDependencies wires all handlers from the pool.
func NewDependencies(pool *pgxpool.Pool) *Dependencies {
	store := auth.NewSessionStore()

	// Start session cleanup ticker
	go func() {
		t := time.NewTicker(30 * time.Minute)
		for range t.C {
			store.Cleanup()
		}
	}()

	return &Dependencies{
		Pool:         pool,
		SessionStore: store,
		Auth:         auth.NewHandler(pool, store),
		User:         user.NewHandler(user.NewService(pool)),
		Deployment:   deployment.NewHandler(deployment.NewService(pool)),
		Access:       access.NewHandler(access.NewService(pool)),
		Licence:      licence.NewHandler(licence.NewService(pool)),
		Notification: notification.NewHandler(pool),
		Dashboard:    dashboard.NewHandler(pool),
		Alert:        alert.NewHandler(pool),
	}
}

// RegisterRoutes mounts all CimplrAdmin routes onto mux.
func RegisterRoutes(mux *http.ServeMux, deps *Dependencies) {
	sessionMW := auth.RequireSession(deps.SessionStore)
	wrap := func(h http.HandlerFunc) http.Handler {
		return cors(recovery(logging(sessionMW(detachCtx(h)))))
	}
	noAuth := func(h http.HandlerFunc) http.Handler {
		return cors(recovery(logging(detachCtx(h))))
	}

	// ── Health ──────────────────────────────────────────────────────────────
	mux.Handle("/cimplrADMIN/health", noAuth(healthHandler(deps.Pool)))

	// ── Auth ────────────────────────────────────────────────────────────────
	mux.Handle("/cimplrADMIN/auth/login",       noAuth(deps.Auth.Login))
	mux.Handle("/cimplrADMIN/auth/logout",       wrap(deps.Auth.Logout))
	mux.Handle("/cimplrADMIN/auth/session/get",  wrap(deps.Auth.SessionGet))

	// ── Users ────────────────────────────────────────────────────────────────
	mux.Handle("/cimplrADMIN/user/create",   wrap(deps.User.Create))
	mux.Handle("/cimplrADMIN/user/approve",  wrap(deps.User.Approve))
	mux.Handle("/cimplrADMIN/user/reject",   wrap(deps.User.Reject))
	mux.Handle("/cimplrADMIN/user/delete",   wrap(deps.User.Delete))
	mux.Handle("/cimplrADMIN/user/get",      wrap(deps.User.Get))
	mux.Handle("/cimplrADMIN/user/get-all",  wrap(deps.User.GetAll))

	// ── Deployments ──────────────────────────────────────────────────────────
	mux.Handle("/cimplrADMIN/deployment/create",   wrap(deps.Deployment.Create))
	mux.Handle("/cimplrADMIN/deployment/approve",  wrap(deps.Deployment.Approve))
	mux.Handle("/cimplrADMIN/deployment/reject",   wrap(deps.Deployment.Reject))
	mux.Handle("/cimplrADMIN/deployment/delete",   wrap(deps.Deployment.Delete))
	mux.Handle("/cimplrADMIN/deployment/get",      wrap(deps.Deployment.Get))
	mux.Handle("/cimplrADMIN/deployment/get-all",  wrap(deps.Deployment.GetAll))

	// ── Access packages ───────────────────────────────────────────────────────
	mux.Handle("/cimplrADMIN/access/package/create",            wrap(deps.Access.PackageCreate))
	mux.Handle("/cimplrADMIN/access/package/get",               wrap(deps.Access.PackageGet))
	mux.Handle("/cimplrADMIN/access/package/get-all",           wrap(deps.Access.PackageGetAll))
	mux.Handle("/cimplrADMIN/access/package/delete",            wrap(deps.Access.PackageDelete))
	mux.Handle("/cimplrADMIN/access/permission/set",            wrap(deps.Access.PermissionSet))
	mux.Handle("/cimplrADMIN/access/permission/bulk-set",       wrap(deps.Access.PermissionBulkSet))
	mux.Handle("/cimplrADMIN/access/permission/get",            wrap(deps.Access.PermissionGet))
	mux.Handle("/cimplrADMIN/access/deployment/assign-package", wrap(deps.Access.AssignPackage))
	mux.Handle("/cimplrADMIN/access/deployment/sync",           wrap(deps.Access.SyncDeployment))
	mux.Handle("/cimplrADMIN/access/deployment/sync-all",       wrap(deps.Access.SyncAll))
	mux.Handle("/cimplrADMIN/access/deployment/permissions",    noAuth(deps.Access.GetAllPermissions))
	// Fast check endpoint — no session required
	mux.Handle("/cimplrADMIN/access/check", noAuth(deps.Access.Check))

	// ── Licences ─────────────────────────────────────────────────────────────
	mux.Handle("/cimplrADMIN/licence/create",  wrap(deps.Licence.Create))
	mux.Handle("/cimplrADMIN/licence/renew",   wrap(deps.Licence.Renew))
	mux.Handle("/cimplrADMIN/licence/get",     wrap(deps.Licence.Get))
	mux.Handle("/cimplrADMIN/licence/get-all", wrap(deps.Licence.GetAll))

	// ── Notifications ────────────────────────────────────────────────────────
	mux.Handle("/cimplrADMIN/notification/list",   wrap(deps.Notification.ListSent))
	mux.Handle("/cimplrADMIN/notification/resend", wrap(deps.Notification.Resend))

	// ── Alerts ───────────────────────────────────────────────────────────────
	mux.Handle("/cimplrADMIN/alerts/list",    wrap(deps.Alert.List))
	mux.Handle("/cimplrADMIN/alerts/resolve", wrap(deps.Alert.Resolve))

	// ── Dashboard ────────────────────────────────────────────────────────────
	mux.Handle("/cimplrADMIN/dashboard/kpis", wrap(deps.Dashboard.KPIs))
}

// ── healthHandler ─────────────────────────────────────────────────────────────

// healthHandler never touches the database.
// pool.Stat() is a pure in-memory read — zero network calls, zero connections
// consumed. Render health probes can hammer this at any rate with zero impact.
func healthHandler(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		stat := pool.Stat()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"status":     "ok",
			"db":         "ok",
			"pool_total": stat.TotalConns(),
			"pool_idle":  stat.IdleConns(),
			"time":       time.Now().UTC(),
		})
	}
}

// ── Middleware helpers ────────────────────────────────────────────────────────

// cors sets permissive CORS headers and short-circuits OPTIONS preflight requests.
func cors(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, X-Session-Token, X-Master-Key, Authorization")
		w.Header().Set("Access-Control-Max-Age", "86400")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// detachCtx replaces r.Context() with a context.WithoutCancel copy so that
// HTTP-level cancellations (client disconnect, Render LB timeout) cannot abort
// in-flight database queries. DB handlers must use their own timeouts.
func detachCtx(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		next.ServeHTTP(w, r.WithContext(context.WithoutCancel(r.Context())))
	}
}

// recovery wraps a handler with panic recovery → 500.
func recovery(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				log.Printf("[recovery] panic: %v\n%s", rec, debug.Stack())
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusInternalServerError)
				_ = json.NewEncoder(w).Encode(map[string]any{
					"success": false, "error": "internal_error", "message": "an unexpected error occurred",
				})
			}
		}()
		next.ServeHTTP(w, r)
	})
}

// bodyCapture wraps ResponseWriter to capture status code and response body.
type bodyCapture struct {
	http.ResponseWriter
	status int
	buf    bytes.Buffer
}

func (bc *bodyCapture) WriteHeader(s int) {
	bc.status = s
	bc.ResponseWriter.WriteHeader(s)
}

func (bc *bodyCapture) Write(b []byte) (int, error) {
	bc.buf.Write(b)
	return bc.ResponseWriter.Write(b)
}

// logging logs each request with method, path, status, duration, actor,
// request payload and response body (structured JSON via applogger).
func logging(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()

		// Read and restore request body so the handler can still read it.
		var reqBody []byte
		if r.Body != nil {
			reqBody, _ = io.ReadAll(r.Body)
			r.Body = io.NopCloser(bytes.NewReader(reqBody))
		}

		bc := &bodyCapture{ResponseWriter: w, status: http.StatusOK}

		next.ServeHTTP(bc, r)

		durationMs := time.Since(start).Milliseconds()

		actorID := "-"
		if sess, ok := auth.SessionFromContext(r.Context()); ok {
			actorID = sess.UserID
		}

		// Parse payload and response as any for structured logging
		payload := applogger.ParseBodyAsAny(reqBody)
		response := applogger.ParseBodyAsAny(bc.buf.Bytes())

		// Extract error string from response if status >= 400
		errMsg := ""
		if bc.status >= 400 {
			if m, ok := response.(map[string]any); ok {
				if msg, ok := m["message"].(string); ok {
					errMsg = msg
				} else if e, ok := m["error"].(string); ok {
					errMsg = e
				}
			}
		}

		applogger.LogHTTP(r.Method, r.URL.Path, bc.status, durationMs,
			actorID, payload, response, errMsg)
	})
}
