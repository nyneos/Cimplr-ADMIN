package access

import (
	"encoding/json"
	"net/http"
)

// Handler exposes HTTP handlers for access/package management.
type Handler struct{ svc *Service }

func NewHandler(svc *Service) *Handler { return &Handler{svc: svc} }

// PackageCreate handles POST /cimplrADMIN/access/package/create
func (h *Handler) PackageCreate(w http.ResponseWriter, r *http.Request) {
	var req struct {
		PackageCode string  `json:"package_code"`
		DisplayName string  `json:"display_name"`
		Description *string `json:"description"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.PackageCode == "" || req.DisplayName == "" {
		writeError(w, http.StatusBadRequest, "validation_error", "package_code and display_name required")
		return
	}
	id, err := h.svc.CreatePackage(r.Context(), req.PackageCode, req.DisplayName, req.Description)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "create_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"package_id": id})
}

// PackageGet handles POST /cimplrADMIN/access/package/get
func (h *Handler) PackageGet(w http.ResponseWriter, r *http.Request) {
	var req struct {
		PackageID string `json:"package_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.PackageID == "" {
		writeError(w, http.StatusBadRequest, "validation_error", "package_id required")
		return
	}
	p, err := h.svc.GetPackage(r.Context(), req.PackageID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "get_failed", err.Error())
		return
	}
	if p == nil {
		writeError(w, http.StatusNotFound, "not_found", "package not found")
		return
	}
	writeJSON(w, http.StatusOK, p)
}

// PackageGetAll handles POST /cimplrADMIN/access/package/get-all
func (h *Handler) PackageGetAll(w http.ResponseWriter, r *http.Request) {
	list, err := h.svc.ListPackages(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "list_failed", err.Error())
		return
	}
	if list == nil {
		list = []*Package{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"packages": list})
}

// PackageDelete handles POST /cimplrADMIN/access/package/delete
func (h *Handler) PackageDelete(w http.ResponseWriter, r *http.Request) {
	var req struct {
		PackageID string `json:"package_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.PackageID == "" {
		writeError(w, http.StatusBadRequest, "validation_error", "package_id required")
		return
	}
	if err := h.svc.DeletePackage(r.Context(), req.PackageID); err != nil {
		writeError(w, http.StatusInternalServerError, "delete_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

// PermissionSet handles POST /cimplrADMIN/access/permission/set
func (h *Handler) PermissionSet(w http.ResponseWriter, r *http.Request) {
	var req struct {
		PackageID string `json:"package_id"`
		Module    string `json:"module"`
		SubModule string `json:"sub_module"`
		Action    string `json:"action"`
		IsAllowed bool   `json:"is_allowed"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_body", err.Error())
		return
	}
	if req.PackageID == "" || req.Module == "" || req.Action == "" {
		writeError(w, http.StatusBadRequest, "validation_error", "package_id, module, action required")
		return
	}
	sub := req.SubModule
	if sub == "" {
		sub = "default"
	}
	if err := h.svc.SetPermission(r.Context(), Permission{
		PackageID: req.PackageID, Module: req.Module,
		SubModule: sub, Action: req.Action, IsAllowed: req.IsAllowed,
	}); err != nil {
		writeError(w, http.StatusInternalServerError, "set_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// PermissionBulkSet handles POST /cimplrADMIN/access/permission/bulk-set
func (h *Handler) PermissionBulkSet(w http.ResponseWriter, r *http.Request) {
	var req struct {
		PackageID   string `json:"package_id"`
		Permissions []struct {
			Module    string `json:"module"`
			SubModule string `json:"sub_module"`
			Action    string `json:"action"`
			IsAllowed bool   `json:"is_allowed"`
		} `json:"permissions"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.PackageID == "" {
		writeError(w, http.StatusBadRequest, "validation_error", "package_id and permissions required")
		return
	}
	var perms []Permission
	for _, p := range req.Permissions {
		sub := p.SubModule
		if sub == "" {
			sub = "default"
		}
		perms = append(perms, Permission{
			PackageID: req.PackageID, Module: p.Module,
			SubModule: sub, Action: p.Action, IsAllowed: p.IsAllowed,
		})
	}
	if err := h.svc.BulkSetPermissions(r.Context(), req.PackageID, perms); err != nil {
		writeError(w, http.StatusInternalServerError, "bulk_set_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"set": len(perms)})
}

// PermissionGet handles POST /cimplrADMIN/access/permission/get
func (h *Handler) PermissionGet(w http.ResponseWriter, r *http.Request) {
	var req struct {
		PackageID string `json:"package_id"`
		Module    string `json:"module"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.PackageID == "" {
		writeError(w, http.StatusBadRequest, "validation_error", "package_id required")
		return
	}
	perms, err := h.svc.GetPermissions(r.Context(), req.PackageID, req.Module)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "get_failed", err.Error())
		return
	}
	if perms == nil {
		perms = []*Permission{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"permissions": perms})
}

// AssignPackage handles POST /cimplrADMIN/access/deployment/assign-package
func (h *Handler) AssignPackage(w http.ResponseWriter, r *http.Request) {
	var req struct {
		DeploymentID string `json:"deployment_id"`
		PackageID    string `json:"package_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.DeploymentID == "" || req.PackageID == "" {
		writeError(w, http.StatusBadRequest, "validation_error", "deployment_id and package_id required")
		return
	}
	if err := h.svc.AssignPackage(r.Context(), req.DeploymentID, req.PackageID, ""); err != nil {
		writeError(w, http.StatusInternalServerError, "assign_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "assigned"})
}

// Check handles POST /cimplrADMIN/access/check (no session required, must be fast)
func (h *Handler) Check(w http.ResponseWriter, r *http.Request) {
	var req struct {
		DeploymentID string `json:"deployment_id"`
		Module       string `json:"module"`
		SubModule    string `json:"sub_module"`
		Action       string `json:"action"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.DeploymentID == "" {
		writeJSON(w, http.StatusOK, map[string]any{"allowed": false, "reason": "invalid_request"})
		return
	}
	sub := req.SubModule
	if sub == "" {
		sub = "default"
	}
	allowed, reason, err := h.svc.CheckPermission(r.Context(), req.DeploymentID, req.Module, sub, req.Action)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"allowed": false, "reason": "internal_error"})
		return
	}
	if !allowed {
		writeJSON(w, http.StatusOK, map[string]any{"allowed": false, "reason": reason})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"allowed": true})
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
