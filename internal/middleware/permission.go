package middleware

import (
	"encoding/json"
	"net/http"

	"github.com/jackc/pgx/v5/pgxpool"

	"CimplrCorpSaas/admin/internal/access"
)

// RequirePermission is the customer-facing middleware plugin.
// It checks deployment status, licence validity, and permission flags.
//
// Usage in customer codebase:
//
//mux.Handle("/api/fx/forward/create",
//    middleware.RequirePermission(pool, "fx-forward-booking", "fxForm", "showCreateButton")(
//        fxHandler.CreateForward,
//    ),
//)
func RequirePermission(pool *pgxpool.Pool, module, subModule, action string) func(http.Handler) http.Handler {
	repo := access.NewRepository(pool)

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			deploymentID := r.Header.Get("X-Deployment-ID")
			if deploymentID == "" {
				writePermError(w, http.StatusUnauthorized, map[string]any{
					"error": "missing_deployment_id",
				})
				return
			}

			sub := subModule
			if sub == "" {
				sub = "default"
			}

			allowed, reason, err := repo.CheckPermission(r.Context(), deploymentID, module, sub, action)
			if err != nil {
				writePermError(w, http.StatusInternalServerError, map[string]any{
					"error": "internal_error",
				})
				return
			}

			if !allowed {
				switch reason {
				case "deployment_suspended":
					writePermError(w, http.StatusForbidden, map[string]any{
						"error": "deployment_suspended",
					})
				case "licence_expired", "no_active_licence":
					writePermError(w, http.StatusForbidden, map[string]any{
						"error":      "licence_expired",
						"renew_url":  "/cimplrADMIN/licence/renew",
					})
				default:
					writePermError(w, http.StatusForbidden, map[string]any{
						"error":   "access_denied",
						"module":  module,
						"action":  action,
						"reason":  reason,
					})
				}
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

func writePermError(w http.ResponseWriter, status int, body map[string]any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
