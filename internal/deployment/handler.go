package deployment

import (
	"encoding/json"
	"net/http"

	"CimplrCorpSaas/admin/internal/auth"
)

// Handler exposes HTTP handlers for deployment management.
type Handler struct{ svc *Service }

func NewHandler(svc *Service) *Handler { return &Handler{svc: svc} }

func requireRole(w http.ResponseWriter, sess *auth.Session, roles ...string) bool {
	for _, r := range roles {
		if sess.Role == r {
			return true
		}
	}
	writeError(w, http.StatusForbidden, "forbidden", "insufficient role")
	return false
}

// Create handles POST /cimplrADMIN/deployment/create
func (h *Handler) Create(w http.ResponseWriter, r *http.Request) {
	sess, _ := auth.SessionFromContext(r.Context())
	var req struct {
		CompanyName    string  `json:"company_name"`
		CompanyEmail   string  `json:"company_email"`
		CompanyPhone   *string `json:"company_phone"`
		ContactPerson  *string `json:"contact_person"`
		CompanyAddress *string `json:"company_address"`
		DBUser         string  `json:"db_user"`
		DBPassword     string  `json:"db_password"`
		DBHost         string  `json:"db_host"`
		DBPort         string  `json:"db_port"`
		DBName         string  `json:"db_name"`
		DBURL          *string `json:"db_url"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_body", err.Error())
		return
	}
	if req.CompanyName == "" || req.DBUser == "" || req.DBHost == "" || req.DBName == "" {
		writeError(w, http.StatusBadRequest, "validation_error", "company_name, db_user, db_host, db_name required")
		return
	}
	port := req.DBPort
	if port == "" {
		port = "5432"
	}
	d := &Deployment{
		CompanyName: req.CompanyName, CompanyEmail: req.CompanyEmail,
		CompanyPhone: req.CompanyPhone, ContactPerson: req.ContactPerson,
		CompanyAddress: req.CompanyAddress, DBUser: req.DBUser,
		DBPassword: req.DBPassword, DBHost: req.DBHost, DBPort: port,
		DBName: req.DBName, DBURL: req.DBURL,
	}
	id, err := h.svc.Create(r.Context(), sess.UserID, sess.Role, d)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "create_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"deployment_id": id})
}

// Approve handles POST /cimplrADMIN/deployment/approve
func (h *Handler) Approve(w http.ResponseWriter, r *http.Request) {
	sess, _ := auth.SessionFromContext(r.Context())
	if !requireRole(w, sess, "CHECKER", "MASTER") {
		return
	}
	var req struct {
		DeploymentID string `json:"deployment_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.DeploymentID == "" {
		writeError(w, http.StatusBadRequest, "validation_error", "deployment_id required")
		return
	}
	if err := h.svc.Approve(r.Context(), sess.UserID, sess.Role, req.DeploymentID); err != nil {
		writeError(w, http.StatusInternalServerError, "approve_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "approved"})
}

// Reject handles POST /cimplrADMIN/deployment/reject
func (h *Handler) Reject(w http.ResponseWriter, r *http.Request) {
	sess, _ := auth.SessionFromContext(r.Context())
	if !requireRole(w, sess, "CHECKER", "MASTER") {
		return
	}
	var req struct {
		DeploymentID string `json:"deployment_id"`
		Reason       string `json:"reason"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.DeploymentID == "" {
		writeError(w, http.StatusBadRequest, "validation_error", "deployment_id required")
		return
	}
	if err := h.svc.Reject(r.Context(), sess.UserID, sess.Role, req.DeploymentID, req.Reason); err != nil {
		writeError(w, http.StatusInternalServerError, "reject_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "rejected"})
}

// Delete handles POST /cimplrADMIN/deployment/delete
func (h *Handler) Delete(w http.ResponseWriter, r *http.Request) {
	sess, _ := auth.SessionFromContext(r.Context())
	if !requireRole(w, sess, "CHECKER", "MASTER") {
		return
	}
	var req struct {
		DeploymentID string `json:"deployment_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.DeploymentID == "" {
		writeError(w, http.StatusBadRequest, "validation_error", "deployment_id required")
		return
	}
	if err := h.svc.Delete(r.Context(), sess.UserID, sess.Role, req.DeploymentID); err != nil {
		writeError(w, http.StatusInternalServerError, "delete_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

// Get handles POST /cimplrADMIN/deployment/get
func (h *Handler) Get(w http.ResponseWriter, r *http.Request) {
	var req struct {
		DeploymentID string `json:"deployment_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.DeploymentID == "" {
		writeError(w, http.StatusBadRequest, "validation_error", "deployment_id required")
		return
	}
	d, err := h.svc.Get(r.Context(), req.DeploymentID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "get_failed", err.Error())
		return
	}
	if d == nil {
		writeError(w, http.StatusNotFound, "not_found", "deployment not found")
		return
	}
	writeJSON(w, http.StatusOK, d)
}

// GetAll handles POST /cimplrADMIN/deployment/get-all
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
		list = []*Deployment{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"deployments": list})
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
