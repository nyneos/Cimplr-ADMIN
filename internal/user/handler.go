package user

import (
	"encoding/json"
	"net/http"

	"CimplrCorpSaas/admin/internal/auth"
)

// Handler exposes HTTP handlers for user management.
type Handler struct {
	svc *Service
}

// NewHandler returns a user Handler.
func NewHandler(svc *Service) *Handler {
	return &Handler{svc: svc}
}

func (h *Handler) requireRole(w http.ResponseWriter, sess *auth.Session, roles ...string) bool {
	for _, r := range roles {
		if sess.Role == r {
			return true
		}
	}
	writeError(w, http.StatusForbidden, "forbidden", "insufficient role")
	return false
}

// Create handles POST /cimplrADMIN/user/create
func (h *Handler) Create(w http.ResponseWriter, r *http.Request) {
	sess, _ := auth.SessionFromContext(r.Context())
	if !h.requireRole(w, sess, "CHECKER", "MASTER") {
		return
	}
	var req struct {
		Username string `json:"username"`
		Email    string `json:"email"`
		FullName string `json:"full_name"`
		Phone    string `json:"phone"`
		Role     string `json:"role"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_body", err.Error())
		return
	}
	if req.Username == "" || req.Email == "" || req.Password == "" || req.Role == "" {
		writeError(w, http.StatusBadRequest, "validation_error", "username, email, role, password required")
		return
	}

	id, err := h.svc.CreateUser(r.Context(), sess.UserID, sess.Role,
		req.Username, req.Email, req.FullName, req.Phone, req.Role, req.Password)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "create_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"user_id": id})
}

// Approve handles POST /cimplrADMIN/user/approve
func (h *Handler) Approve(w http.ResponseWriter, r *http.Request) {
	sess, _ := auth.SessionFromContext(r.Context())
	if !h.requireRole(w, sess, "CHECKER", "MASTER") {
		return
	}
	var req struct {
		UserID string `json:"user_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.UserID == "" {
		writeError(w, http.StatusBadRequest, "validation_error", "user_id required")
		return
	}
	if err := h.svc.ApproveUser(r.Context(), sess.UserID, sess.Role, req.UserID); err != nil {
		status := http.StatusInternalServerError
		if err == ErrNotFound {
			status = http.StatusNotFound
		} else if err == ErrForbidden {
			status = http.StatusForbidden
		}
		writeError(w, status, "approve_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "approved"})
}

// Reject handles POST /cimplrADMIN/user/reject
func (h *Handler) Reject(w http.ResponseWriter, r *http.Request) {
	sess, _ := auth.SessionFromContext(r.Context())
	if !h.requireRole(w, sess, "CHECKER", "MASTER") {
		return
	}
	var req struct {
		UserID string `json:"user_id"`
		Reason string `json:"reason"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.UserID == "" {
		writeError(w, http.StatusBadRequest, "validation_error", "user_id required")
		return
	}
	if err := h.svc.RejectUser(r.Context(), sess.UserID, sess.Role, req.UserID, req.Reason); err != nil {
		writeError(w, http.StatusInternalServerError, "reject_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "rejected"})
}

// Delete handles POST /cimplrADMIN/user/delete
func (h *Handler) Delete(w http.ResponseWriter, r *http.Request) {
	sess, _ := auth.SessionFromContext(r.Context())
	if !h.requireRole(w, sess, "CHECKER", "MASTER") {
		return
	}
	var req struct {
		UserID string `json:"user_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.UserID == "" {
		writeError(w, http.StatusBadRequest, "validation_error", "user_id required")
		return
	}
	if err := h.svc.DeleteUser(r.Context(), sess.UserID, sess.Role, req.UserID); err != nil {
		writeError(w, http.StatusInternalServerError, "delete_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

// Get handles POST /cimplrADMIN/user/get
func (h *Handler) Get(w http.ResponseWriter, r *http.Request) {
	var req struct {
		UserID string `json:"user_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.UserID == "" {
		writeError(w, http.StatusBadRequest, "validation_error", "user_id required")
		return
	}
	u, err := h.svc.GetUser(r.Context(), req.UserID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "get_failed", err.Error())
		return
	}
	if u == nil {
		writeError(w, http.StatusNotFound, "not_found", "user not found")
		return
	}
	writeJSON(w, http.StatusOK, u)
}

// GetAll handles POST /cimplrADMIN/user/get-all
func (h *Handler) GetAll(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Status string `json:"status"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)
	users, err := h.svc.ListUsers(r.Context(), req.Status)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "list_failed", err.Error())
		return
	}
	if users == nil {
		users = []*User{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"users": users})
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
