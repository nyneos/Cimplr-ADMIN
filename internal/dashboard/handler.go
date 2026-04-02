package dashboard

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Handler exposes dashboard KPI aggregation.
type Handler struct{ pool *pgxpool.Pool }

func NewHandler(pool *pgxpool.Pool) *Handler { return &Handler{pool: pool} }

type KPIResponse struct {
	TotalDeployments       int `json:"total_deployments"`
	ActiveDeployments      int `json:"active_deployments"`
	PendingDeployments     int `json:"pending_deployments"`
	TotalUsers             int `json:"total_users"`
	ActiveUsers            int `json:"active_users"`
	PendingUsers           int `json:"pending_users"`
	LicencesExpiring7Days  int `json:"licences_expiring_in_7_days"`
	LicencesInGrace        int `json:"licences_in_grace"`
	LicencesExpired        int `json:"licences_expired"`
	NotificationsSentToday int `json:"notifications_sent_today"`
	NotificationsFailed    int `json:"notifications_failed"`
}

// KPIs handles POST /cimplrADMIN/dashboard/kpis
func (h *Handler) KPIs(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	var kpi KPIResponse
	err := h.pool.QueryRow(ctx, `
		WITH
		dep AS (
			SELECT
				COUNT(*)                                              AS total_deployments,
				COUNT(*) FILTER (WHERE is_active = true)             AS active_deployments,
				COUNT(*) FILTER (WHERE status = 'PENDING')           AS pending_deployments
			FROM admin_svc.deployments
		),
		usr AS (
			SELECT
				COUNT(*)                                              AS total_users,
				COUNT(*) FILTER (WHERE status = 'APPROVED')          AS active_users,
				COUNT(*) FILTER (WHERE status = 'PENDING')           AS pending_users
			FROM admin_svc.users
		),
		lic AS (
			SELECT
				COUNT(*) FILTER (
					WHERE status = 'ACTIVE'
					  AND expires_at <= now() + interval '7 days'
				)                                                     AS expiring_7days,
				COUNT(*) FILTER (WHERE status = 'GRACE')             AS in_grace,
				COUNT(*) FILTER (WHERE status = 'EXPIRED')           AS expired
			FROM admin_svc.licences
		),
		notif AS (
			SELECT
				COUNT(*) FILTER (
					WHERE processing_status = 'SENT'
					  AND attempted_at >= current_date
				)                                                     AS sent_today,
				COUNT(*) FILTER (WHERE processing_status = 'DEAD')   AS failed
			FROM admin_svc.send_history
		)
		SELECT
			dep.total_deployments, dep.active_deployments, dep.pending_deployments,
			usr.total_users,       usr.active_users,       usr.pending_users,
			lic.expiring_7days,    lic.in_grace,           lic.expired,
			notif.sent_today,      notif.failed
		FROM dep, usr, lic, notif
	`).Scan(
		&kpi.TotalDeployments, &kpi.ActiveDeployments, &kpi.PendingDeployments,
		&kpi.TotalUsers, &kpi.ActiveUsers, &kpi.PendingUsers,
		&kpi.LicencesExpiring7Days, &kpi.LicencesInGrace, &kpi.LicencesExpired,
		&kpi.NotificationsSentToday, &kpi.NotificationsFailed,
	)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "db_error", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, kpi)
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"success": status < 400, "data": payload})
}

func writeError(w http.ResponseWriter, status int, code, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"success": false, "error": code, "message": msg})
}
