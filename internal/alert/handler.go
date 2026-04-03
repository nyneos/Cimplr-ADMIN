package alert

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Handler exposes alert management endpoints.
type Handler struct{ pool *pgxpool.Pool }

func NewHandler(pool *pgxpool.Pool) *Handler { return &Handler{pool: pool} }

// Alert is the domain model.
type Alert struct {
	AlertID      string          `json:"alert_id"`
	AlertType    string          `json:"alert_type"`
	Severity     string          `json:"severity"`
	DeploymentID *string         `json:"deployment_id"`
	CompanyName  *string         `json:"company_name"`
	Title        string          `json:"title"`
	Detail       json.RawMessage `json:"detail"`
	IsResolved   bool            `json:"is_resolved"`
	ResolvedAt   *time.Time      `json:"resolved_at"`
	ResolvedBy   *string         `json:"resolved_by"`
	CreatedAt    time.Time       `json:"created_at"`
}

// List handles POST /cimplrADMIN/alerts/list
// Body (optional): {"resolved": false, "deployment_id": "...", "severity": "CRITICAL"}
func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Resolved     *bool   `json:"resolved"`
		DeploymentID *string `json:"deployment_id"`
		Severity     *string `json:"severity"`
		Limit        int     `json:"limit"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)
	if req.Limit <= 0 || req.Limit > 500 {
		req.Limit = 100
	}

	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	rows, err := h.pool.Query(ctx,
		`SELECT a.alert_id::text, a.alert_type, a.severity,
		        a.deployment_id::text, d.company_name,
		        a.title, COALESCE(a.detail,'{}'), a.is_resolved,
		        a.resolved_at,
		        u.username,
		        a.created_at
		 FROM admin_svc.alerts a
		 LEFT JOIN admin_svc.deployments d ON d.deployment_id = a.deployment_id
		 LEFT JOIN admin_svc.users u       ON u.user_id = a.resolved_by
		 WHERE ($1::boolean IS NULL OR a.is_resolved = $1)
		   AND ($2::uuid IS NULL OR a.deployment_id = $2::uuid)
		   AND ($3::text IS NULL OR a.severity = $3)
		 ORDER BY a.created_at DESC
		 LIMIT $4`,
		req.Resolved, req.DeploymentID, req.Severity, req.Limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "db_error", err.Error())
		return
	}
	defer rows.Close()

	var out []Alert
	for rows.Next() {
		var a Alert
		var detailBytes []byte
		if err := rows.Scan(
			&a.AlertID, &a.AlertType, &a.Severity,
			&a.DeploymentID, &a.CompanyName,
			&a.Title, &detailBytes, &a.IsResolved,
			&a.ResolvedAt, &a.ResolvedBy,
			&a.CreatedAt,
		); err != nil {
			writeError(w, http.StatusInternalServerError, "scan_error", err.Error())
			return
		}
		a.Detail = json.RawMessage(detailBytes)
		out = append(out, a)
	}
	if out == nil {
		out = []Alert{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"alerts": out, "total": len(out)})
}

// Resolve handles POST /cimplrADMIN/alerts/resolve
func (h *Handler) Resolve(w http.ResponseWriter, r *http.Request) {
	var req struct {
		AlertID  string `json:"alert_id"`
		ResolvedBy string `json:"resolved_by"` // user_id
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.AlertID == "" {
		writeError(w, http.StatusBadRequest, "validation_error", "alert_id required")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	tag, err := h.pool.Exec(ctx,
		`UPDATE admin_svc.alerts
		 SET is_resolved=true, resolved_at=now(), resolved_by=$2
		 WHERE alert_id=$1 AND is_resolved=false`,
		req.AlertID, nullUUID(req.ResolvedBy))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "db_error", err.Error())
		return
	}
	if tag.RowsAffected() == 0 {
		writeError(w, http.StatusNotFound, "not_found", "alert not found or already resolved")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "resolved"})
}

func nullUUID(s string) any {
	if s == "" {
		return nil
	}
	return s
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
