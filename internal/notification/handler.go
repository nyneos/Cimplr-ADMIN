package notification

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Handler exposes notification management endpoints.
type Handler struct{ pool *pgxpool.Pool }

func NewHandler(pool *pgxpool.Pool) *Handler { return &Handler{pool: pool} }

// ListSent handles POST /cimplrADMIN/notification/list
func (h *Handler) ListSent(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	rows, err := h.pool.Query(ctx,
		`SELECT history_id::text, outbox_id::text, event_id, recipient_email,
		        processing_status, attempted_at
		 FROM admin_svc.send_history ORDER BY attempted_at DESC LIMIT 100`)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "db_error", err.Error())
		return
	}
	defer rows.Close()

	type row struct {
		HistoryID   string  `json:"history_id"`
		OutboxID    string  `json:"outbox_id"`
		EventID     *string `json:"event_id"`
		RecipEmail  *string `json:"recipient_email"`
		Status      *string `json:"processing_status"`
		AttemptedAt *time.Time `json:"attempted_at"`
	}
	var out []row
	for rows.Next() {
		var x row
		if err := rows.Scan(&x.HistoryID, &x.OutboxID, &x.EventID, &x.RecipEmail,
			&x.Status, &x.AttemptedAt); err != nil {
			writeError(w, http.StatusInternalServerError, "scan_error", err.Error())
			return
		}
		out = append(out, x)
	}
	if out == nil {
		out = []row{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"notifications": out})
}

// Resend handles POST /cimplrADMIN/notification/resend
func (h *Handler) Resend(w http.ResponseWriter, r *http.Request) {
	var req struct {
		OutboxID string `json:"outbox_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.OutboxID == "" {
		writeError(w, http.StatusBadRequest, "validation_error", "outbox_id required")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	// Reset the outbox record back to PENDING so the worker picks it up.
	_, err := h.pool.Exec(ctx,
		`UPDATE admin_svc.outbox
		 SET processing_status='PENDING', retry_count=0, last_error=null, scheduled_at=now()
		 WHERE outbox_id=$1`, req.OutboxID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "resend_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "queued"})
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
