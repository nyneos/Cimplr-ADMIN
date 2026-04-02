// Package cimplr provides a drop-in permission + licence middleware for any
// Go HTTP service managed by CimplrAdmin.
//
// Drop this single file into your client project (e.g. internal/cimplr/cimplr_middleware.go)
// and change the package declaration to match your own module.
//
// # How it works
//
// CimplrAdmin pushes a full permission + licence snapshot into two tables in
// YOUR OWN Postgres database:
//
//config.permissions  (module, sub_module, action, is_allowed, synced_at)
//config.settings     (key TEXT PRIMARY KEY, value TEXT, synced_at)
//
// Those tables are created automatically on the first sync.
// Your code reads them locally — zero runtime network call to CimplrAdmin.
//
// # Quick start
//
//db, _ := pgxpool.New(ctx, os.Getenv("DATABASE_URL"))
//cm  := cimplr.New(db)
//
//// Protect a route
//mux.Handle("/invoices", cm.Require("billing", "invoices", "read")(invoiceHandler))
//
//// Manual check
//if !cm.Can(ctx, "billing", "invoices", "write") { ... }
//
//// Licence check
//if cm.LicenceStatus(ctx) != "ACTIVE" { ... }
package cimplr

import (
	"context"
	"net/http"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// permKey is the cache map key.
type permKey struct{ module, subModule, action string }

// Client holds a cached view of permissions and licence settings.
// It refreshes the in-memory cache from the local DB every CacheTTL (default 60 s).
type Client struct {
	pool *pgxpool.Pool

	mu          sync.RWMutex
	perms       map[permKey]bool
	settings    map[string]string
	lastRefresh time.Time

	// CacheTTL controls how long before the cache is considered stale.
	// Default is 60 seconds. Set to 0 for always-fresh (hits DB every request).
	CacheTTL time.Duration
}

// New creates a Client backed by the given pool.
func New(pool *pgxpool.Pool) *Client {
	return &Client{
		pool:     pool,
		perms:    map[permKey]bool{},
		settings: map[string]string{},
		CacheTTL: 60 * time.Second,
	}
}

// ─── permission checks ───────────────────────────────────────────────────────

// Can returns true when the permission exists AND is_allowed = true.
// Uses cached data; re-reads DB when cache is stale.
func (c *Client) Can(ctx context.Context, module, subModule, action string) bool {
	c.maybeRefresh(ctx)
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.perms[permKey{module, subModule, action}]
}

// ─── licence helpers ─────────────────────────────────────────────────────────

// LicenceStatus returns the cached licence status.
// Possible values: "ACTIVE", "GRACE", "EXPIRED", or "" if not yet synced.
func (c *Client) LicenceStatus(ctx context.Context) string {
	c.maybeRefresh(ctx)
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.settings["licence_status"]
}

// IsDeploymentActive returns true when the deployment has not been suspended.
func (c *Client) IsDeploymentActive(ctx context.Context) bool {
	c.maybeRefresh(ctx)
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.settings["deployment_is_active"] == "true"
}

// LicenceExpiresAt returns the expiry timestamp as an ISO-8601 string.
func (c *Client) LicenceExpiresAt(ctx context.Context) string {
	c.maybeRefresh(ctx)
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.settings["licence_expires_at"]
}

// ─── HTTP middleware ──────────────────────────────────────────────────────────

// Require returns an http.Handler middleware that:
//  1. Rejects suspended deployments with 403.
//  2. Rejects EXPIRED licences with 403 (GRACE is allowed through).
//  3. Rejects requests lacking the named permission with 403.
func (c *Client) Require(module, subModule, action string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !c.IsDeploymentActive(r.Context()) {
				http.Error(w, `{"error":"deployment_suspended"}`, http.StatusForbidden)
				return
			}
			switch c.LicenceStatus(r.Context()) {
			case "ACTIVE", "GRACE":
				// allowed
			default:
				http.Error(w, `{"error":"licence_expired"}`, http.StatusForbidden)
				return
			}
			if !c.Can(r.Context(), module, subModule, action) {
				http.Error(w, `{"error":"permission_denied"}`, http.StatusForbidden)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// RequireLicence is a lighter middleware that only checks deployment active +
// licence status, without any module-level permission check.
func (c *Client) RequireLicence() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !c.IsDeploymentActive(r.Context()) {
				http.Error(w, `{"error":"deployment_suspended"}`, http.StatusForbidden)
				return
			}
			switch c.LicenceStatus(r.Context()) {
			case "ACTIVE", "GRACE":
				next.ServeHTTP(w, r)
			default:
				http.Error(w, `{"error":"licence_expired"}`, http.StatusForbidden)
			}
		})
	}
}

// ─── cache ───────────────────────────────────────────────────────────────────

func (c *Client) maybeRefresh(ctx context.Context) {
	c.mu.RLock()
	stale := time.Since(c.lastRefresh) > c.CacheTTL
	c.mu.RUnlock()
	if !stale {
		return
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	// Re-check after acquiring write lock (double-checked locking).
	if time.Since(c.lastRefresh) <= c.CacheTTL {
		return
	}

	// Load permissions
	newPerms := map[permKey]bool{}
	if rows, err := c.pool.Query(ctx,
		`SELECT module, sub_module, action, is_allowed FROM config.permissions`); err == nil {
		for rows.Next() {
			var m, s, a string
			var allowed bool
			if rows.Scan(&m, &s, &a, &allowed) == nil {
				newPerms[permKey{m, s, a}] = allowed
			}
		}
		rows.Close()
	}

	// Load settings
	newSettings := map[string]string{}
	if rows, err := c.pool.Query(ctx,
		`SELECT key, value FROM config.settings`); err == nil {
		for rows.Next() {
			var k, v string
			if rows.Scan(&k, &v) == nil {
				newSettings[k] = v
			}
		}
		rows.Close()
	}

	c.perms = newPerms
	c.settings = newSettings
	c.lastRefresh = time.Now()
}

// ForceRefresh immediately invalidates the cache and reloads from DB.
// Useful after receiving a webhook or manual trigger from CimplrAdmin.
func (c *Client) ForceRefresh(ctx context.Context) {
	c.mu.Lock()
	c.lastRefresh = time.Time{} // zero makes the next maybeRefresh re-read
	c.mu.Unlock()
	c.maybeRefresh(ctx)
}
