package kubernetes

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"sort"
	"strings"

	"github.com/google/uuid"
	"github.com/huangchengsir/pipewright/internal/configstate"
)

const ReleaseProfileProtocol = "kube-gateway-v2"
const ReleaseHelmVersion = "v4.3.1"

type ReleaseToolProfile struct {
	Runtime     ToolProfile `json:"runtime"`
	HelperPath  string      `json:"helperPath"`
	HelmPath    string      `json:"helmPath"`
	HelmVersion string      `json:"helmVersion"`
}
type ReleaseProfile struct {
	ConnectionID string             `json:"connectionId"`
	Revision     int64              `json:"revision"`
	Enabled      bool               `json:"enabled"`
	Profile      ReleaseToolProfile `json:"profile"`
}
type ReleaseAccess struct {
	ID         ReleaseResourceID `json:"resource"`
	Actions    []string          `json:"actions"`
	Containers []string          `json:"containers,omitempty"`
	Fields     []string          `json:"fields,omitempty"`
}
type ReleaseGrantInput struct {
	ProjectID       string          `json:"projectId"`
	Environment     string          `json:"environment"`
	Executor        Executor        `json:"executor"`
	Resources       []ReleaseAccess `json:"resources"`
	RequireApproval bool            `json:"requireApproval"`
	Enabled         bool            `json:"enabled"`
}
type ReleaseGrant struct {
	ID           string `json:"id"`
	ConnectionID string `json:"connectionId"`
	Revision     int64  `json:"revision"`
	ReleaseGrantInput
	CreatedAt int64 `json:"createdAt"`
	UpdatedAt int64 `json:"updatedAt"`
}
type ReleaseAuthorizationRequest struct {
	ProjectID    string          `json:"projectId"`
	Environment  string          `json:"environment"`
	ConnectionID string          `json:"connectionId"`
	CredentialID string          `json:"credentialId"`
	Context      string          `json:"context"`
	Executor     Executor        `json:"executor"`
	Resources    []ReleaseAccess `json:"resources"`
}
type ReleaseAuthorization struct {
	Request            ReleaseAuthorizationRequest `json:"request"`
	ClusterKey         string                      `json:"clusterKey"`
	ConnectionRevision int64                       `json:"connectionRevision"`
	CredentialRevision int64                       `json:"credentialRevision"`
	Profile            ReleaseProfile              `json:"profile"`
	GrantRevisions     map[string]int64            `json:"grantRevisions"`
	RequireApproval    bool                        `json:"requireApproval"`
	Fingerprint        string                      `json:"fingerprint"`
}

func validReleaseAccess(a ReleaseAccess) bool {
	id := a.ID
	if (id.Group != "" && !validDeploymentName(id.Group)) || !validID(id.Version) || !validDeploymentName(id.Resource) || !validDeploymentName(id.Name) || id.Kind == "" || strings.ContainsAny(id.Kind, "*/\x00\n\r") || (id.Namespace != "" && !validName(id.Namespace, 63)) || len(a.Actions) == 0 || len(a.Actions) > 16 || len(a.Containers) > 32 || len(a.Fields) > 128 {
		return false
	}
	seen := map[string]bool{}
	for _, v := range a.Actions {
		if seen[v] {
			return false
		}
		seen[v] = true
		switch v {
		case "get", "list", "watch", "create", "patch", "update", "delete", "rollback", "scale", "bind", "escalate", "impersonate", "probe", "helm-storage", "namespace-security", "related-pods", "related-replicasets", "related-endpointslices", "related-jobs", "related-hpas", "related-controllerrevisions", "helm-hook", "coredns":
		default:
			return false
		}
	}
	if seen[releaseDaemonRevisionAction] && !releaseDaemonID(id) {
		return false
	}
	for _, v := range a.Containers {
		if !validName(v, 63) {
			return false
		}
	}
	for _, v := range a.Fields {
		if len(v) > 512 || v == "" || v[0] != '/' || strings.ContainsAny(v, "*\x00\r\n") {
			return false
		}
	}
	return true
}
func validateReleaseToolProfile(p ReleaseToolProfile) error {
	base := p.Runtime
	if base.Protocol != ReleaseProfileProtocol || p.HelmVersion != ReleaseHelmVersion || !absoluteToolPath.MatchString(p.HelperPath) || !absoluteToolPath.MatchString(p.HelmPath) {
		return ErrInvalid
	}
	base.Protocol = ProfileProtocol
	return validateProfile(base)
}
func (s *Service) SaveReleaseProfile(ctx context.Context, connection string, expected int64, in ReleaseToolProfile, enabled bool) (ReleaseProfile, error) {
	out := ReleaseProfile{}
	if !validID(connection) || expected < 0 || validateReleaseToolProfile(in) != nil {
		return out, ErrInvalid
	}
	e := s.configWrite(ctx, func(ctx context.Context, q dbtx) error {
		if _, e := s.connection(ctx, q, connection); e != nil {
			return e
		}
		var old int64
		e := q.QueryRowContext(ctx, `SELECT revision FROM k8s_release_profiles WHERE connection_id=?`, connection).Scan(&old)
		if e != nil && !errors.Is(e, sql.ErrNoRows) {
			return e
		}
		if old != expected {
			return ErrConflict
		}
		out = ReleaseProfile{connection, old + 1, enabled, in}
		b, _ := json.Marshal(in)
		if old == 0 {
			_, e = q.ExecContext(ctx, `INSERT INTO k8s_release_profiles(connection_id,revision,enabled,profile_json,updated_at) VALUES(?,?,?,?,?)`, connection, 1, enabled, string(b), s.now().UnixMilli())
			return e
		}
		return requireOne(q.ExecContext(ctx, `UPDATE k8s_release_profiles SET revision=?,enabled=?,profile_json=?,updated_at=? WHERE connection_id=? AND revision=?`, out.Revision, enabled, string(b), s.now().UnixMilli(), connection, expected))
	})
	return out, e
}
func (s *Service) releaseProfile(ctx context.Context, q dbtx, connection string) (ReleaseProfile, error) {
	p := ReleaseProfile{ConnectionID: connection}
	var body string
	e := q.QueryRowContext(ctx, `SELECT revision,enabled,profile_json FROM k8s_release_profiles WHERE connection_id=?`, connection).Scan(&p.Revision, &p.Enabled, &body)
	if e != nil {
		return p, rowError(e)
	}
	if json.Unmarshal([]byte(body), &p.Profile) != nil || validateReleaseToolProfile(p.Profile) != nil {
		return p, ErrInvalid
	}
	return p, nil
}
func (s *Service) GetReleaseProfile(ctx context.Context, connection string) (ReleaseProfile, error) {
	return s.releaseProfile(ctx, configstate.Queryer(ctx, s.db), connection)
}
func (s *Service) releaseGrant(ctx context.Context, q dbtx, id string) (ReleaseGrant, error) {
	var g ReleaseGrant
	var body string
	e := q.QueryRowContext(ctx, `SELECT grant_json FROM k8s_release_grants WHERE id=?`, id).Scan(&body)
	if e != nil {
		return g, rowError(e)
	}
	if json.Unmarshal([]byte(body), &g) != nil || g.ID != id {
		return g, ErrInvalid
	}
	return g, nil
}
func (s *Service) SaveReleaseGrant(ctx context.Context, connection, id string, expected int64, in ReleaseGrantInput) (ReleaseGrant, error) {
	out := ReleaseGrant{}
	if !validID(in.ProjectID) || !cleanText(in.Environment, 128) || !validExecutor(in.Executor) || len(in.Resources) == 0 || len(in.Resources) > 512 {
		return out, ErrInvalid
	}
	seen := map[string]bool{}
	for _, r := range in.Resources {
		if !validReleaseAccess(r) || seen[r.ID.Key()] {
			return out, ErrInvalid
		}
		seen[r.ID.Key()] = true
	}
	if id == "" {
		if expected != 0 {
			return out, ErrInvalid
		}
		id = "krg_" + uuid.NewString()
	} else if !validID(id) || !validRevision(expected) {
		return out, ErrInvalid
	}
	e := s.configWrite(ctx, func(ctx context.Context, q dbtx) error {
		c, e := s.connection(ctx, q, connection)
		if e != nil {
			return e
		}
		allowed := false
		for _, ex := range c.Executors {
			allowed = allowed || ex == in.Executor
		}
		if !allowed {
			return ErrDenied
		}
		var count int
		if e = q.QueryRowContext(ctx, `SELECT COUNT(*) FROM projects WHERE id=?`, in.ProjectID).Scan(&count); e != nil {
			return e
		}
		if count != 1 {
			return ErrNotFound
		}
		now := s.now().UnixMilli()
		created := now
		if expected > 0 {
			old, e := s.releaseGrant(ctx, q, id)
			if e != nil {
				return e
			}
			if old.Revision != expected || old.ConnectionID != connection {
				return ErrConflict
			}
			created = old.CreatedAt
		}
		out = ReleaseGrant{ID: id, ConnectionID: connection, Revision: expected + 1, ReleaseGrantInput: in, CreatedAt: created, UpdatedAt: now}
		b, _ := json.Marshal(out)
		if len(b) > 4<<20 {
			return ErrInvalid
		}
		if expected == 0 {
			_, e = q.ExecContext(ctx, `INSERT INTO k8s_release_grants(id,connection_id,project_id,revision,enabled,grant_json,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?)`, id, connection, in.ProjectID, out.Revision, in.Enabled, string(b), created, now)
			return e
		}
		return requireOne(q.ExecContext(ctx, `UPDATE k8s_release_grants SET project_id=?,revision=?,enabled=?,grant_json=?,updated_at=? WHERE id=? AND revision=?`, in.ProjectID, out.Revision, in.Enabled, string(b), now, id, expected))
	})
	return out, e
}
func (s *Service) ListReleaseGrants(ctx context.Context, connection string) ([]ReleaseGrant, error) {
	q := configstate.Queryer(ctx, s.db)
	rows, e := q.QueryContext(ctx, `SELECT grant_json FROM k8s_release_grants WHERE connection_id=? ORDER BY created_at,id`, connection)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []ReleaseGrant{}
	for rows.Next() {
		var b string
		var g ReleaseGrant
		if rows.Scan(&b) != nil || json.Unmarshal([]byte(b), &g) != nil {
			return nil, ErrInvalid
		}
		out = append(out, g)
	}
	return out, rows.Err()
}
func releaseStringSetContains(allowed, want []string) bool {
	for _, w := range want {
		found := false
		for _, a := range allowed {
			found = found || a == w
		}
		if !found {
			return false
		}
	}
	return true
}
func releaseAccessContains(a, w ReleaseAccess) bool {
	mutating := false
	for _, verb := range w.Actions {
		switch verb {
		case "create", "patch", "update", "delete", "rollback", "scale":
			mutating = true
		}
	}
	if mutating && len(a.Containers) > 0 && len(w.Containers) == 0 {
		return false
	}
	return a.ID == w.ID && releaseStringSetContains(a.Actions, w.Actions) && releaseStringSetContains(a.Containers, w.Containers) && (len(a.Fields) == 0 || releaseStringSetContains(a.Fields, w.Fields) && (!mutating || len(w.Fields) > 0))
}
func (s *Service) authorizeRelease(ctx context.Context, q dbtx, in ReleaseAuthorizationRequest) (ReleaseAuthorization, error) {
	a := ReleaseAuthorization{Request: in}
	if !validID(in.ProjectID) || !cleanText(in.Environment, 128) || !validExecutor(in.Executor) || len(in.Resources) == 0 || len(in.Resources) > 512 {
		return a, ErrInvalid
	}
	for _, r := range in.Resources {
		if !validReleaseAccess(r) {
			return a, ErrInvalid
		}
	}
	c, e := s.connection(ctx, q, in.ConnectionID)
	if e != nil {
		return a, e
	}
	cred, _, e := s.credential(ctx, q, in.CredentialID)
	if e != nil {
		return a, e
	}
	if !c.Enabled || !cred.Enabled || c.CredentialID != in.CredentialID || c.Context != in.Context || cred.Context != in.Context || c.ClusterKey != cred.ClusterKey {
		return a, ErrDenied
	}
	allowed := false
	for _, ex := range c.Executors {
		allowed = allowed || ex == in.Executor
	}
	if !allowed {
		return a, ErrDenied
	}
	profile, e := s.releaseProfile(ctx, q, c.ID)
	if e != nil || !profile.Enabled {
		return a, ErrDenied
	}
	rows, e := q.QueryContext(ctx, `SELECT grant_json FROM k8s_release_grants WHERE connection_id=? AND project_id=? AND enabled=1 ORDER BY id`, c.ID, in.ProjectID)
	if e != nil {
		return a, e
	}
	var grants []ReleaseGrant
	for rows.Next() {
		var b string
		var g ReleaseGrant
		if e = rows.Scan(&b); e != nil {
			rows.Close()
			return a, e
		}
		if json.Unmarshal([]byte(b), &g) != nil {
			rows.Close()
			return a, ErrInvalid
		}
		if g.Environment == in.Environment && g.Executor == in.Executor {
			grants = append(grants, g)
		}
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return a, e
	}
	a.GrantRevisions = map[string]int64{}
	for _, wanted := range in.Resources {
		found := false
		for _, g := range grants {
			for _, scope := range g.Resources {
				if releaseAccessContains(scope, wanted) {
					found = true
					a.GrantRevisions[g.ID] = g.Revision
					a.RequireApproval = a.RequireApproval || g.RequireApproval
					break
				}
			}

		}
		if !found {
			return a, ErrDenied
		}
	}
	a.ClusterKey = c.ClusterKey
	a.ConnectionRevision = c.Revision
	a.CredentialRevision = cred.Revision
	a.Profile = profile
	a.Fingerprint = fingerprint(a)
	return a, nil
}
func (s *Service) AuthorizeRelease(ctx context.Context, in ReleaseAuthorizationRequest) (ReleaseAuthorization, error) {
	var out ReleaseAuthorization
	e := configstate.WithReadTxContext(ctx, s.db, func(ctx context.Context, q dbtx) error {
		var e error
		out, e = s.authorizeRelease(ctx, q, in)
		return e
	})
	return out, e
}
func (s *Service) validateReleaseAuthorization(ctx context.Context, q dbtx, a ReleaseAuthorization) error {
	current, e := s.authorizeRelease(ctx, q, a.Request)
	if e != nil {
		return e
	}
	hash := a.Fingerprint
	a.Fingerprint = ""
	if hash != fingerprint(a) || hash != current.Fingerprint {
		return ErrConflict
	}
	return nil
}
func (s *Service) ValidateReleaseAuthorization(ctx context.Context, a ReleaseAuthorization) error {
	return s.validateReleaseAuthorization(ctx, configstate.Queryer(ctx, s.db), a)
}
func canonicalReleaseAccess(in []ReleaseAccess) []ReleaseAccess {
	out := append([]ReleaseAccess(nil), in...)
	sort.Slice(out, func(i, j int) bool { return out[i].ID.Key() < out[j].ID.Key() })
	return out
}

// ReleasePolicyCatalog freezes only explicit v2 grants. It is an admission
// catalog, not a blanket permission for the resources eventually rendered.
// Final preview obtains a narrower authorization for the actual plan/action set.
func (s *Service) ReleasePolicyCatalog(ctx context.Context, in ReleaseAuthorizationRequest) (ReleaseAuthorization, error) {
	var out ReleaseAuthorization
	e := configstate.WithReadTxContext(ctx, s.db, func(ctx context.Context, q dbtx) error {
		connection, e := s.connection(ctx, q, in.ConnectionID)
		if e != nil {
			return e
		}
		if in.Executor.Kind == "ssh" && in.Executor.HostKeySHA256 == "" {
			found := false
			for _, approved := range connection.Executors {
				if approved.Kind == in.Executor.Kind && approved.ServerID == in.Executor.ServerID {
					if found {
						return ErrDenied
					}
					in.Executor = approved
					found = true
				}
			}
			if !found {
				return ErrDenied
			}
		}
		rows, e := q.QueryContext(ctx, `SELECT grant_json FROM k8s_release_grants WHERE connection_id=? AND project_id=? AND enabled=1 ORDER BY id`, in.ConnectionID, in.ProjectID)
		if e != nil {
			return e
		}
		scopes := map[string]ReleaseAccess{}
		for rows.Next() {
			var body string
			var grant ReleaseGrant
			if e = rows.Scan(&body); e != nil {
				rows.Close()
				return e
			}
			if json.Unmarshal([]byte(body), &grant) != nil {
				rows.Close()
				return ErrInvalid
			}
			if grant.Environment != in.Environment || grant.Executor != in.Executor {
				continue
			}
			for _, scope := range grant.Resources {
				if !hasReleaseAction(scope.Actions, "get") {
					continue
				}
				entry := scopes[scope.ID.Key()]
				entry.ID = scope.ID
				if len(entry.Actions) == 0 {
					entry.Actions = []string{"get"}
				}
				for _, action := range []string{"related-pods", "related-replicasets", "related-endpointslices", "related-jobs", "related-hpas", "related-controllerrevisions", "helm-storage"} {
					if hasReleaseAction(scope.Actions, action) && !hasReleaseAction(entry.Actions, action) {
						entry.Actions = append(entry.Actions, action)
					}
				}
				scopes[scope.ID.Key()] = entry
			}
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			return e
		}
		in.Resources = nil
		for _, scope := range scopes {
			in.Resources = append(in.Resources, scope)
		}
		in.Resources = canonicalReleaseAccess(in.Resources)
		if len(in.Resources) == 0 {
			return ErrDenied
		}
		out, e = s.authorizeRelease(ctx, q, in)
		return e
	})
	return out, e
}
func hasReleaseAction(actions []string, want string) bool {
	for _, action := range actions {
		if action == want {
			return true
		}
	}
	return false
}

func (s *Service) DeleteReleaseGrant(ctx context.Context, connection, id string, expected int64) error {
	if !validRevision(expected) {
		return ErrInvalid
	}
	return s.configWrite(ctx, func(ctx context.Context, q dbtx) error {
		g, e := s.releaseGrant(ctx, q, id)
		if e != nil {
			return e
		}
		if g.ConnectionID != connection || g.Revision != expected {
			return ErrConflict
		}
		rows, e := q.QueryContext(ctx, `SELECT id FROM k8s_releases WHERE connection_id=? AND (owner_fenced=0 OR cleanup_state<>'verified' OR state IN ('paused','resume_requested','outcome_unknown'))`, connection)
		if e != nil {
			return e
		}
		var ids []string
		for rows.Next() {
			var id string
			if e = rows.Scan(&id); e != nil {
				rows.Close()
				return e
			}
			ids = append(ids, id)
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			return e
		}
		for _, releaseID := range ids {
			r, e := s.release(ctx, q, releaseID)
			if e != nil {
				return e
			}
			if _, used := r.Plan.Authorization.GrantRevisions[id]; used {
				return ErrInUse
			}
		}
		return requireOne(q.ExecContext(ctx, `DELETE FROM k8s_release_grants WHERE id=? AND revision=?`, id, expected))
	})
}
