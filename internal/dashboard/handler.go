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

// ── KPI detail types ──────────────────────────────────────────────────────────

type DeploymentKPI struct {
	Count   int              `json:"count"`
	Records []DeploymentItem `json:"records"`
}
type DeploymentItem struct {
	DeploymentID string     `json:"deployment_id"`
	CompanyName  string     `json:"company_name"`
	Status       string     `json:"status"`
	IsActive     bool       `json:"is_active"`
	CreatedAt    time.Time  `json:"created_at"`
	ApprovedAt   *time.Time `json:"approved_at"`
}

type UserKPI struct {
	Count   int        `json:"count"`
	Records []UserItem `json:"records"`
}
type UserItem struct {
	UserID    string    `json:"user_id"`
	Username  string    `json:"username"`
	Email     string    `json:"email"`
	Role      string    `json:"role"`
	Status    string    `json:"status"`
	CreatedAt time.Time `json:"created_at"`
}

type LicenceKPI struct {
	Count   int           `json:"count"`
	Records []LicenceItem `json:"records"`
}
type LicenceItem struct {
	LicenceID    string    `json:"licence_id"`
	DeploymentID string    `json:"deployment_id"`
	CompanyName  string    `json:"company_name"`
	Status       string    `json:"status"`
	ExpiresAt    time.Time `json:"expires_at"`
	GraceDays    int       `json:"grace_days"`
}

type NotificationKPI struct {
	Count   int                `json:"count"`
	Records []NotificationItem `json:"records"`
}
type NotificationItem struct {
	HistoryID   string     `json:"history_id"`
	OutboxID    string     `json:"outbox_id"`
	EventID     *string    `json:"event_id"`
	RecipEmail  *string    `json:"recipient_email"`
	Status      *string    `json:"processing_status"`
	LastError   *string    `json:"last_error"`
	AttemptedAt *time.Time `json:"attempted_at"`
}

type AlertKPI struct {
	Count   int         `json:"count"`
	Records []AlertItem `json:"records"`
}
type AlertItem struct {
	AlertID      string          `json:"alert_id"`
	AlertType    string          `json:"alert_type"`
	Severity     string          `json:"severity"`
	DeploymentID *string         `json:"deployment_id"`
	CompanyName  *string         `json:"company_name"`
	Title        string          `json:"title"`
	Detail       json.RawMessage `json:"detail"`
	CreatedAt    time.Time       `json:"created_at"`
}

// KPIResponse is the full dashboard payload.
type KPIResponse struct {
	// Counts (quick display)
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
	UnresolvedAlerts       int `json:"unresolved_alerts"`

	// Detail arrays (drill-down)
	ActiveDeploymentsList     DeploymentKPI   `json:"active_deployments_list"`
	PendingDeploymentsList    DeploymentKPI   `json:"pending_deployments_list"`
	PendingUsersList          UserKPI         `json:"pending_users_list"`
	LicencesExpiring7DaysList LicenceKPI      `json:"licences_expiring_7days_list"`
	LicencesInGraceList       LicenceKPI      `json:"licences_in_grace_list"`
	LicencesExpiredList       LicenceKPI      `json:"licences_expired_list"`
	FailedNotificationsList   NotificationKPI `json:"failed_notifications_list"`
	ActiveAlerts              AlertKPI        `json:"active_alerts"`
}

// KPIs handles POST /cimplrADMIN/dashboard/kpis
func (h *Handler) KPIs(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()

	var kpi KPIResponse

	// ── 1. Aggregate counts ───────────────────────────────────────────────────
	err := h.pool.QueryRow(ctx, `
		WITH
		dep AS (
			SELECT COUNT(*) AS total,
				COUNT(*) FILTER (WHERE is_active=true)   AS active,
				COUNT(*) FILTER (WHERE status='PENDING') AS pending
			FROM admin_svc.deployments
		),
		usr AS (
			SELECT COUNT(*) AS total,
				COUNT(*) FILTER (WHERE status='APPROVED') AS active,
				COUNT(*) FILTER (WHERE status='PENDING')  AS pending
			FROM admin_svc.users
		),
		lic AS (
			SELECT
				COUNT(*) FILTER (WHERE status='ACTIVE' AND expires_at <= now()+interval '7 days') AS expiring_7days,
				COUNT(*) FILTER (WHERE status='GRACE')   AS in_grace,
				COUNT(*) FILTER (WHERE status='EXPIRED') AS expired
			FROM admin_svc.licences
		),
		notif AS (
			SELECT
				COUNT(*) FILTER (WHERE processing_status='SENT' AND attempted_at >= current_date) AS sent_today,
				COUNT(*) FILTER (WHERE processing_status='DEAD') AS failed
			FROM admin_svc.send_history
		),
		alrt AS (
			SELECT COUNT(*) FILTER (WHERE is_resolved=false) AS unresolved
			FROM admin_svc.alerts
		)
		SELECT dep.total, dep.active, dep.pending,
		       usr.total, usr.active, usr.pending,
		       lic.expiring_7days, lic.in_grace, lic.expired,
		       notif.sent_today, notif.failed,
		       alrt.unresolved
		FROM dep, usr, lic, notif, alrt
	`).Scan(
		&kpi.TotalDeployments, &kpi.ActiveDeployments, &kpi.PendingDeployments,
		&kpi.TotalUsers, &kpi.ActiveUsers, &kpi.PendingUsers,
		&kpi.LicencesExpiring7Days, &kpi.LicencesInGrace, &kpi.LicencesExpired,
		&kpi.NotificationsSentToday, &kpi.NotificationsFailed,
		&kpi.UnresolvedAlerts,
	)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "db_error", err.Error())
		return
	}

	// ── 2. Active deployments ─────────────────────────────────────────────────
	kpi.ActiveDeploymentsList = DeploymentKPI{Count: kpi.ActiveDeployments, Records: []DeploymentItem{}}
	if rows, err := h.pool.Query(ctx,
		`SELECT deployment_id::text, company_name, status, is_active, created_at, approved_at
		 FROM admin_svc.deployments WHERE is_active=true ORDER BY created_at DESC`); err == nil {
		for rows.Next() {
			var d DeploymentItem
			_ = rows.Scan(&d.DeploymentID, &d.CompanyName, &d.Status, &d.IsActive, &d.CreatedAt, &d.ApprovedAt)
			kpi.ActiveDeploymentsList.Records = append(kpi.ActiveDeploymentsList.Records, d)
		}
		rows.Close()
	}

	// ── 3. Pending deployments ────────────────────────────────────────────────
	kpi.PendingDeploymentsList = DeploymentKPI{Count: kpi.PendingDeployments, Records: []DeploymentItem{}}
	if rows, err := h.pool.Query(ctx,
		`SELECT deployment_id::text, company_name, status, is_active, created_at, approved_at
		 FROM admin_svc.deployments WHERE status='PENDING' ORDER BY created_at DESC`); err == nil {
		for rows.Next() {
			var d DeploymentItem
			_ = rows.Scan(&d.DeploymentID, &d.CompanyName, &d.Status, &d.IsActive, &d.CreatedAt, &d.ApprovedAt)
			kpi.PendingDeploymentsList.Records = append(kpi.PendingDeploymentsList.Records, d)
		}
		rows.Close()
	}

	// ── 4. Pending users ──────────────────────────────────────────────────────
	kpi.PendingUsersList = UserKPI{Count: kpi.PendingUsers, Records: []UserItem{}}
	if rows, err := h.pool.Query(ctx,
		`SELECT user_id::text, username, email, role, status, created_at
		 FROM admin_svc.users WHERE status='PENDING' ORDER BY created_at DESC`); err == nil {
		for rows.Next() {
			var u UserItem
			_ = rows.Scan(&u.UserID, &u.Username, &u.Email, &u.Role, &u.Status, &u.CreatedAt)
			kpi.PendingUsersList.Records = append(kpi.PendingUsersList.Records, u)
		}
		rows.Close()
	}

	// ── 5. Licences expiring in 7 days ────────────────────────────────────────
	kpi.LicencesExpiring7DaysList = LicenceKPI{Count: kpi.LicencesExpiring7Days, Records: []LicenceItem{}}
	if rows, err := h.pool.Query(ctx,
		`SELECT l.licence_id::text, l.deployment_id::text, d.company_name, l.status, l.expires_at, l.grace_days
		 FROM admin_svc.licences l
		 JOIN admin_svc.deployments d ON d.deployment_id = l.deployment_id
		 WHERE l.status='ACTIVE' AND l.expires_at <= now()+interval '7 days'
		 ORDER BY l.expires_at ASC`); err == nil {
		for rows.Next() {
			var li LicenceItem
			_ = rows.Scan(&li.LicenceID, &li.DeploymentID, &li.CompanyName, &li.Status, &li.ExpiresAt, &li.GraceDays)
			kpi.LicencesExpiring7DaysList.Records = append(kpi.LicencesExpiring7DaysList.Records, li)
		}
		rows.Close()
	}

	// ── 6. Licences in grace ──────────────────────────────────────────────────
	kpi.LicencesInGraceList = LicenceKPI{Count: kpi.LicencesInGrace, Records: []LicenceItem{}}
	if rows, err := h.pool.Query(ctx,
		`SELECT l.licence_id::text, l.deployment_id::text, d.company_name, l.status, l.expires_at, l.grace_days
		 FROM admin_svc.licences l
		 JOIN admin_svc.deployments d ON d.deployment_id = l.deployment_id
		 WHERE l.status='GRACE' ORDER BY l.expires_at ASC`); err == nil {
		for rows.Next() {
			var li LicenceItem
			_ = rows.Scan(&li.LicenceID, &li.DeploymentID, &li.CompanyName, &li.Status, &li.ExpiresAt, &li.GraceDays)
			kpi.LicencesInGraceList.Records = append(kpi.LicencesInGraceList.Records, li)
		}
		rows.Close()
	}

	// ── 7. Expired licences ───────────────────────────────────────────────────
	kpi.LicencesExpiredList = LicenceKPI{Count: kpi.LicencesExpired, Records: []LicenceItem{}}
	if rows, err := h.pool.Query(ctx,
		`SELECT l.licence_id::text, l.deployment_id::text, d.company_name, l.status, l.expires_at, l.grace_days
		 FROM admin_svc.licences l
		 JOIN admin_svc.deployments d ON d.deployment_id = l.deployment_id
		 WHERE l.status='EXPIRED' ORDER BY l.expires_at DESC`); err == nil {
		for rows.Next() {
			var li LicenceItem
			_ = rows.Scan(&li.LicenceID, &li.DeploymentID, &li.CompanyName, &li.Status, &li.ExpiresAt, &li.GraceDays)
			kpi.LicencesExpiredList.Records = append(kpi.LicencesExpiredList.Records, li)
		}
		rows.Close()
	}

	// ── 8. Failed notifications (DEAD) ────────────────────────────────────────
	kpi.FailedNotificationsList = NotificationKPI{Count: kpi.NotificationsFailed, Records: []NotificationItem{}}
	if rows, err := h.pool.Query(ctx,
		`SELECT h.history_id::text, h.outbox_id::text, h.event_id, h.recipient_email,
		        h.processing_status, o.last_error, h.attempted_at
		 FROM admin_svc.send_history h
		 JOIN admin_svc.outbox o ON o.outbox_id = h.outbox_id
		 WHERE h.processing_status='DEAD'
		 ORDER BY h.attempted_at DESC LIMIT 100`); err == nil {
		for rows.Next() {
			var n NotificationItem
			_ = rows.Scan(&n.HistoryID, &n.OutboxID, &n.EventID, &n.RecipEmail, &n.Status, &n.LastError, &n.AttemptedAt)
			kpi.FailedNotificationsList.Records = append(kpi.FailedNotificationsList.Records, n)
		}
		rows.Close()
	}

	// ── 9. Active (unresolved) alerts ────────────────────────────────────────
	kpi.ActiveAlerts = AlertKPI{Count: kpi.UnresolvedAlerts, Records: []AlertItem{}}
	if rows, err := h.pool.Query(ctx,
		`SELECT a.alert_id::text, a.alert_type, a.severity,
		        a.deployment_id::text, d.company_name,
		        a.title, COALESCE(a.detail,'{}'), a.created_at
		 FROM admin_svc.alerts a
		 LEFT JOIN admin_svc.deployments d ON d.deployment_id = a.deployment_id
		 WHERE a.is_resolved=false
		 ORDER BY a.created_at DESC LIMIT 50`); err == nil {
		for rows.Next() {
			var a AlertItem
			var detailBytes []byte
			_ = rows.Scan(&a.AlertID, &a.AlertType, &a.Severity,
				&a.DeploymentID, &a.CompanyName,
				&a.Title, &detailBytes, &a.CreatedAt)
			a.Detail = json.RawMessage(detailBytes)
			kpi.ActiveAlerts.Records = append(kpi.ActiveAlerts.Records, a)
		}
		rows.Close()
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


