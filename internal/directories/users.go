// SPDX-License-Identifier: GPL-3.0-or-later

package directories

import (
	"net/http"
	"net/mail"
	"strconv"
	"strings"

	"github.com/joernott/dcdash/internal/audit"
	"github.com/joernott/dcdash/internal/auth"
	"github.com/joernott/dcdash/internal/directories/ad"
	"github.com/joernott/dcdash/internal/httpx"
	"github.com/joernott/dcdash/internal/rbac"
)

// Audit actions for users.
const (
	AuditUserCreate   = "directory.user.create"
	AuditUserUpdate   = "directory.user.update"
	AuditUserDelete   = "directory.user.delete"
	AuditUserEnable   = "directory.user.enable"
	AuditUserDisable  = "directory.user.disable"
	AuditUserUnlock   = "directory.user.unlock"
	AuditUserPassword = "directory.user.password"
)

// Limits of user lists.
const (
	defaultListLimit = 500
	maxListLimit     = 5000
)

// UserView is a user with what the requesting user may do with it.
type UserView struct {
	ad.User
	// CanOperate: disable, enable, unlock, reset the password.
	CanOperate bool `json:"canOperate"`
	// CanWrite: change and delete.
	CanWrite bool `json:"canWrite"`
}

func userView(subj *rbac.Subject, b Backend, u ad.User) UserView {
	res := UserResource(u.Name)
	return UserView{User: u,
		CanOperate: may(subj, b, res, u.Managed, u.Protected, rbac.ActionOperate),
		CanWrite:   may(subj, b, res, u.Managed, u.Protected, rbac.ActionWrite)}
}

// listLimit reads the limit query parameter.
func listLimit(r *http.Request) int {
	n, err := strconv.Atoi(r.URL.Query().Get("limit"))
	if err != nil || n <= 0 {
		return defaultListLimit
	}
	return min(n, maxListLimit)
}

func (s *Service) handleUsers(w http.ResponseWriter, r *http.Request) {
	b, subj, ok := s.lookup(w, r)
	if !ok {
		return
	}
	users, truncated, err := b.Users(r.Context(), r.URL.Query().Get("q"), listLimit(r))
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	out := make([]UserView, 0, len(users))
	for _, u := range users {
		if subj.Can(perm(b, UserResource(u.Name), rbac.ActionRead)) {
			out = append(out, userView(subj, b, u))
		}
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"users": out, "truncated": truncated})
}

// findUser reads the user of the request and checks that the requesting user may perform
// action on it (read: may see it). It writes 404 or 403 otherwise.
func (s *Service) findUser(w http.ResponseWriter, r *http.Request, action rbac.Action) (Backend, *rbac.Subject, ad.User, bool) {
	b, subj, ok := s.lookup(w, r)
	if !ok {
		return nil, nil, ad.User{}, false
	}
	name := r.PathValue("name")
	if !subj.Can(perm(b, UserResource(name), rbac.ActionRead)) {
		httpx.WriteError(w, http.StatusNotFound, "not_found", "user not found")
		return nil, nil, ad.User{}, false
	}
	u, err := b.User(r.Context(), name)
	if err != nil {
		s.writeError(w, r, err)
		return nil, nil, ad.User{}, false
	}
	if action > rbac.ActionRead && !may(subj, b, UserResource(u.Name), u.Managed, u.Protected, action) {
		msg := "permission denied"
		switch {
		case !u.Managed:
			msg = "the user is outside the containers dcdash manages"
		case u.Protected:
			msg = "the user is protected; changing it needs the admin permission"
		}
		httpx.WriteError(w, http.StatusForbidden, "forbidden", msg)
		return nil, nil, ad.User{}, false
	}
	return b, subj, u, true
}

func (s *Service) handleUser(w http.ResponseWriter, r *http.Request) {
	b, subj, u, ok := s.findUser(w, r, rbac.ActionRead)
	if !ok {
		return
	}
	httpx.WriteJSON(w, http.StatusOK, userView(subj, b, u))
}

// validText checks optional attributes: length, no control characters, and the mail
// address.
func validText(fields map[string]string) string {
	for k, v := range fields {
		if len(v) > maxText || strings.ContainsFunc(v, func(r rune) bool { return r < 0x20 }) {
			return k + ": at most " + strconv.Itoa(maxText) + " characters, no control characters"
		}
		if (k == "mail" || k == "upn") && v != "" {
			if a, err := mail.ParseAddress(v); err != nil || a.Address != v {
				return k + ": not a valid address"
			}
		}
	}
	return ""
}

func validPassword(pw string) string {
	if pw == "" || len([]rune(pw)) > maxPassword {
		return "the password must have 1 to " + strconv.Itoa(maxPassword) + " characters"
	}
	return ""
}

func (s *Service) handleCreateUser(w http.ResponseWriter, r *http.Request) {
	b, subj, ok := s.lookup(w, r)
	if !ok {
		return
	}
	var req ad.NewUser
	if err := httpx.DecodeJSON(w, r, &req); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	msg := validName(req.Name, 20)
	if msg == "" {
		msg = validText(map[string]string{"givenName": req.GivenName, "surname": req.Surname, "displayName": req.DisplayName,
			"mail": req.Mail, "description": req.Description, "upn": req.UPN})
	}
	if msg == "" {
		msg = validPassword(req.Password)
	}
	if msg == "" && !b.Config().Managed(req.OU) {
		msg = "ou: not a container dcdash manages"
	}
	if msg != "" {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_user", msg)
		return
	}
	if !auth.Check(w, r, perm(b, UserResource(req.Name), rbac.ActionWrite)) {
		return
	}
	tgt := b.Name() + "/" + UserResource(req.Name)
	details := map[string]any{"ou": req.OU, "mustChangePassword": req.MustChangePassword, "disabled": req.Disabled}
	u, err := b.CreateUser(r.Context(), req)
	if err != nil {
		s.fail(w, r, AuditUserCreate, tgt, details, err)
		return
	}
	s.audit.RecordRequest(r, AuditUserCreate, tgt, audit.ResultSuccess, details)
	httpx.WriteJSON(w, http.StatusCreated, userView(subj, b, u))
}

func (s *Service) handleUpdateUser(w http.ResponseWriter, r *http.Request) {
	var req ad.UserChange
	if err := httpx.DecodeJSON(w, r, &req); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	fields := map[string]string{}
	details := map[string]any{}
	for k, v := range map[string]*string{"givenName": req.GivenName, "surname": req.Surname, "displayName": req.DisplayName,
		"mail": req.Mail, "description": req.Description, "upn": req.UPN} {
		if v != nil {
			fields[k] = *v
			details[k] = *v
		}
	}
	if msg := validText(fields); msg != "" {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_user", msg)
		return
	}
	if req.UPN != nil && *req.UPN == "" {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_user", "upn: required")
		return
	}
	b, subj, u, ok := s.findUser(w, r, rbac.ActionWrite)
	if !ok {
		return
	}
	tgt := b.Name() + "/" + UserResource(u.Name)
	if err := b.UpdateUser(r.Context(), u.Name, req); err != nil {
		s.fail(w, r, AuditUserUpdate, tgt, details, err)
		return
	}
	s.audit.RecordRequest(r, AuditUserUpdate, tgt, audit.ResultSuccess, details)
	s.respondUser(w, r, b, subj, u.Name)
}

// respondUser reads the user again and writes it.
func (s *Service) respondUser(w http.ResponseWriter, r *http.Request, b Backend, subj *rbac.Subject, name string) {
	u, err := b.User(r.Context(), name)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, userView(subj, b, u))
}

func (s *Service) handleDeleteUser(w http.ResponseWriter, r *http.Request) {
	b, _, u, ok := s.findUser(w, r, rbac.ActionWrite)
	if !ok {
		return
	}
	tgt := b.Name() + "/" + UserResource(u.Name)
	details := map[string]any{"dn": u.DN}
	if err := b.DeleteUser(r.Context(), u.Name); err != nil {
		s.fail(w, r, AuditUserDelete, tgt, details, err)
		return
	}
	s.audit.RecordRequest(r, AuditUserDelete, tgt, audit.ResultSuccess, details)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Service) handleEnableUser(w http.ResponseWriter, r *http.Request) {
	s.userOperation(w, r, AuditUserEnable, func(b Backend, u ad.User) error { return b.SetUserEnabled(r.Context(), u.Name, true) })
}

func (s *Service) handleDisableUser(w http.ResponseWriter, r *http.Request) {
	s.userOperation(w, r, AuditUserDisable, func(b Backend, u ad.User) error { return b.SetUserEnabled(r.Context(), u.Name, false) })
}

func (s *Service) handleUnlockUser(w http.ResponseWriter, r *http.Request) {
	s.userOperation(w, r, AuditUserUnlock, func(b Backend, u ad.User) error { return b.UnlockUser(r.Context(), u.Name) })
}

// userOperation runs an operate action on a user, audits it and returns the user.
func (s *Service) userOperation(w http.ResponseWriter, r *http.Request, action string, f func(Backend, ad.User) error) {
	b, subj, u, ok := s.findUser(w, r, rbac.ActionOperate)
	if !ok {
		return
	}
	tgt := b.Name() + "/" + UserResource(u.Name)
	if err := f(b, u); err != nil {
		s.fail(w, r, action, tgt, nil, err)
		return
	}
	s.audit.RecordRequest(r, action, tgt, audit.ResultSuccess, nil)
	s.respondUser(w, r, b, subj, u.Name)
}

// PasswordReset is the body of POST .../users/{name}/password.
type PasswordReset struct {
	Password           string `json:"password"`
	MustChangePassword bool   `json:"mustChangePassword"`
}

func (s *Service) handleResetPassword(w http.ResponseWriter, r *http.Request) {
	var req PasswordReset
	if err := httpx.DecodeJSON(w, r, &req); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	if msg := validPassword(req.Password); msg != "" {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_password", msg)
		return
	}
	b, subj, u, ok := s.findUser(w, r, rbac.ActionOperate)
	if !ok {
		return
	}
	tgt := b.Name() + "/" + UserResource(u.Name)
	details := map[string]any{"mustChangePassword": req.MustChangePassword}
	if err := b.ResetPassword(r.Context(), u.Name, req.Password, req.MustChangePassword); err != nil {
		s.fail(w, r, AuditUserPassword, tgt, details, err)
		return
	}
	s.audit.RecordRequest(r, AuditUserPassword, tgt, audit.ResultSuccess, details)
	s.respondUser(w, r, b, subj, u.Name)
}

// fail audits a failed change and writes the error.
func (s *Service) fail(w http.ResponseWriter, r *http.Request, action, target string, details map[string]any, err error) {
	if details == nil {
		details = map[string]any{}
	}
	details["error"] = err.Error()
	s.audit.RecordRequest(r, action, target, audit.ResultFailure, details)
	s.writeError(w, r, err)
}
