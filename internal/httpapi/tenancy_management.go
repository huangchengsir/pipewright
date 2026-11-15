package httpapi

import (
	"github.com/go-chi/chi/v5"
	"github.com/huangchengsir/pipewright/internal/audit"
	"github.com/huangchengsir/pipewright/internal/auth"
	"github.com/huangchengsir/pipewright/internal/authz"
	"github.com/huangchengsir/pipewright/internal/tenancy"
	"net"
	"net/http"
)

func WithTenancyRepository(repository *tenancy.Repository) Option {
	return func(o *options) { o.accessRepository = repository }
}
func mountTenancyManagement(router chi.Router, repository *tenancy.Repository, aud audit.Recorder) {
	if repository == nil {
		return
	}
	var peerBudget tenancy.LookupBudget
	handle := func(method, path string, fn func(http.ResponseWriter, *http.Request, tenancy.Actor)) {
		router.MethodFunc(method, path, func(w http.ResponseWriter, r *http.Request) {
			platformHeaders(w)
			actor, ok := accessActor(w, r)
			if !ok {
				return
			}
			fn(w, r, actor)
		})
	}
	handle("GET", "/access/teams", func(w http.ResponseWriter, r *http.Request, a tenancy.Actor) {
		out, err := repository.Teams(r.Context(), a)
		if err != nil {
			accessError(w, err)
			return
		}
		writeJSON(w, 200, out)
	})
	handle("POST", "/access/teams", func(w http.ResponseWriter, r *http.Request, a tenancy.Actor) {
		var in struct {
			Name string `json:"name"`
		}
		if !platformJSON(w, r, &in) {
			return
		}
		out, err := repository.CreateTeamAs(r.Context(), a, in.Name)
		if err != nil {
			accessError(w, err)
			return
		}
		accessAudit(r, aud, "team_create", out.ID)
		writeJSON(w, 201, out)
	})
	handle("GET", "/access/catalog", func(w http.ResponseWriter, r *http.Request, a tenancy.Actor) {
		if _, err := repository.Teams(r.Context(), a); err != nil {
			accessError(w, err)
			return
		}
		actions := []authz.Action{}
		for _, action := range authz.Catalog() {
			if !action.InstanceOnly {
				actions = append(actions, action)
			}
		}
		writeJSON(w, 200, map[string]any{"version": authz.CatalogVersion, "actions": actions})
	})

	handle("GET", "/access/teams/{teamId}/navigation", func(w http.ResponseWriter, r *http.Request, a tenancy.Actor) {
		out, err := repository.Navigation(r.Context(), a, chi.URLParam(r, "teamId"))
		if err != nil {
			accessError(w, err)
			return
		}
		writeJSON(w, 200, out)
	})
	handle("POST", "/access/teams/{teamId}/decisions", func(w http.ResponseWriter, r *http.Request, a tenancy.Actor) {
		var in struct {
			Queries []tenancy.DecisionQuery `json:"queries"`
		}
		if !platformJSON(w, r, &in) {
			return
		}
		out, err := repository.Decisions(r.Context(), a, chi.URLParam(r, "teamId"), in.Queries)
		if err != nil {
			accessError(w, err)
			return
		}
		writeJSON(w, 200, out)
	})
	handle("POST", "/access/teams/{teamId}/preview", func(w http.ResponseWriter, r *http.Request, a tenancy.Actor) {
		var in tenancy.PreviewInput
		if !platformJSON(w, r, &in) {
			return
		}
		out, err := repository.Preview(r.Context(), a, chi.URLParam(r, "teamId"), in)
		if err != nil {
			accessError(w, err)
			return
		}
		writeJSON(w, 200, out)
	})
	handle("GET", "/access/teams/{teamId}/capabilities", func(w http.ResponseWriter, r *http.Request, a tenancy.Actor) {
		out, err := repository.ManagementCapabilities(r.Context(), a, chi.URLParam(r, "teamId"))
		if err != nil {
			accessError(w, err)
			return
		}
		writeJSON(w, 200, out)
	})
	handle("GET", "/access/teams/{teamId}/roles", func(w http.ResponseWriter, r *http.Request, a tenancy.Actor) {
		out, err := repository.ReadRoles(r.Context(), a, chi.URLParam(r, "teamId"))
		if err != nil {
			accessError(w, err)
			return
		}
		writeJSON(w, 200, out)
	})
	saveRole := func(w http.ResponseWriter, r *http.Request, a tenancy.Actor) {
		var in tenancy.RoleInput
		if !platformJSON(w, r, &in) {
			return
		}
		out, err := repository.SaveRole(r.Context(), a, chi.URLParam(r, "teamId"), chi.URLParam(r, "roleId"), in)
		if err != nil {
			accessError(w, err)
			return
		}
		accessAudit(r, aud, "role_save", out.ID)
		writeJSON(w, 200, out)
	}
	handle("POST", "/access/teams/{teamId}/roles", saveRole)
	handle("PUT", "/access/teams/{teamId}/roles/{roleId}", saveRole)
	handle("DELETE", "/access/teams/{teamId}/roles/{roleId}", func(w http.ResponseWriter, r *http.Request, a tenancy.Actor) {
		var in struct {
			ExpectedRevision uint64 `json:"expectedRevision"`
		}
		if !platformJSON(w, r, &in) {
			return
		}
		id := chi.URLParam(r, "roleId")
		if err := repository.DeleteRole(r.Context(), a, chi.URLParam(r, "teamId"), id, in.ExpectedRevision); err != nil {
			accessError(w, err)
			return
		}
		accessAudit(r, aud, "role_delete", id)
		w.WriteHeader(204)
	})
	handle("GET", "/access/teams/{teamId}/members", func(w http.ResponseWriter, r *http.Request, a tenancy.Actor) {
		out, err := repository.ReadMembers(r.Context(), a, chi.URLParam(r, "teamId"))
		if err != nil {
			accessError(w, err)
			return
		}
		writeJSON(w, 200, out)
	})
	saveMember := func(w http.ResponseWriter, r *http.Request, a tenancy.Actor) {
		var in tenancy.MemberInput
		if !platformJSON(w, r, &in) {
			return
		}
		out, err := repository.SaveMember(r.Context(), a, chi.URLParam(r, "teamId"), chi.URLParam(r, "memberId"), in)
		if err != nil {
			accessError(w, err)
			return
		}
		accessAudit(r, aud, "member_save", out.ID)
		writeJSON(w, 200, out)
	}
	handle("POST", "/access/teams/{teamId}/members", saveMember)
	handle("PUT", "/access/teams/{teamId}/members/{memberId}", saveMember)
	handle("POST", "/access/teams/{teamId}/lookup-account", func(w http.ResponseWriter, r *http.Request, a tenancy.Actor) {
		var in struct {
			Username string `json:"username"`
		}
		if !platformJSON(w, r, &in) {
			return
		}
		peer, _, err := net.SplitHostPort(r.RemoteAddr)
		if err != nil {
			peer = r.RemoteAddr
		}
		if retry := peerBudget.Allow(peer, 60); retry > 0 {
			accessError(w, &auth.ErrLockedOut{RetryAfter: retry})
			return
		}
		out, err := repository.LookupExisting(r.Context(), a, chi.URLParam(r, "teamId"), in.Username)
		if err != nil {
			accessError(w, err)
			return
		}
		writeJSON(w, 200, out)
	})
	handle("GET", "/access/teams/{teamId}/choices", func(w http.ResponseWriter, r *http.Request, a tenancy.Actor) {
		out, err := repository.Choices(r.Context(), a, chi.URLParam(r, "teamId"))
		if err != nil {
			accessError(w, err)
			return
		}
		writeJSON(w, 200, out)
	})
	handle("PUT", "/access/teams/{teamId}/environments/{environmentId}/purpose", func(w http.ResponseWriter, r *http.Request, a tenancy.Actor) {
		var in struct {
			Purpose string `json:"purpose"`
		}
		if !platformJSON(w, r, &in) {
			return
		}
		id := chi.URLParam(r, "environmentId")
		if err := repository.SetEnvironmentPurpose(r.Context(), a, chi.URLParam(r, "teamId"), id, in.Purpose); err != nil {
			accessError(w, err)
			return
		}
		accessAudit(r, aud, "environment_purpose_change", id)
		w.WriteHeader(204)
	})
}
