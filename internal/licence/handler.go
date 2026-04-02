package licence

import (
	"encoding/json"
	"net/http"
	"time"

	"CimplrCorpSaas/admin/internal/auth"
)

// Handler exposes HTTP handlers for licence management.
type Handler struct{ svc *Service }

func NewHandler(svc *Service) *Handler { return &Handler{svc: svc} }

// Create handles POST /cimplrADMIN/licence/create
func (h *Handler) Create(w http.ResponseWriter, r *http.Request) {
	sess, _ := auth.SessionFromContext(r.Context())
	var req struct {
		DeploymentID string    `json:"deployment_id"`
		StartsAt     time.Time `json:"starts_at"`
		ExpiresAt    time.Time `json:"expires_at"`
		GraceDays    int       `json:"grace_days"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_body", err.Error())
		return
	}
	if req.DeploymentID == "" || req.ExpiresAt.IsZero() {
		writeError(w, http.StatusBadRequest, "validation_error", "deployment_id and expires_at required")
		return
	}
	grace := req.GraceDays
	if grace == 0 {
		grace = 7
	}
	starts := req.StartsAt
	if starts.IsZero() {
		starts = time.Now().UTC()
	}
	id, err := h.svc.Create(r.Context(), sess.UserID, req.DeploymentID, starts, req.ExpiresAt, grace)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "create_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"licence_id": id})
}

// Renew handles POST /cimplrADMIN/licence/renew
func (h *Handler) Renew(w http.ResponseWriter, r *http.Request) {
	sess, _ := auth.SessionFromContext(r.Context())
	var req struct {
		LicenceID    string    `json:"licence_id"`
		NewExpiresAt time.Time `json:"new_expires_at"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_body", err.Error())
		return
	}
	if req.LicenceID == "" || req.NewExpiresAt.IsZero() {
		writeError(w, http.StatusBadRequest, "validation_error", "licence_id and new_expires_at required")
		return
	}
	if err := h.svc.Renew(r.Context(), sess.UserID, req.LicenceID, req.NewExpiresAt); err != nil {
		writeError(w, http.StatusInternalServerError, "renew_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "renewed"})
}

// Get handles POST /cimplrADMIN/licence/get
func (h *Handler) Get(w http.ResponseWriter, r *http.Request) {
	var req struct {
		DeploymentID string `json:"deployment_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.DeploymentID == "" {
		writeError(w, http.StatusBadRequest, "validation_error", "deployment_id required")
		return
	}
	list, err := h.svc.GetByDeployment(r.Context(), req.DeploymentID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "get_failed", err.Error())
		return
	}
	if list == nil {
		list = []*Licence{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"licences": list})
}

// GetAll handles POST /cimplrADMIN/licence/get-all
func (h *Handler) GetAll(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Status string `json:"status"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)
	list, err := h.svc.List(r.Context(), req.Status)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "list_failed", err.Error())
		return
	}
	if list == nil {
		list = []*Licence{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"licences": list})
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
