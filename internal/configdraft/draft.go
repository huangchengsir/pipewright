package configdraft

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/huangchengsir/pipewright/internal/auth"
	"github.com/huangchengsir/pipewright/internal/build"
	"github.com/huangchengsir/pipewright/internal/configstate"
	"github.com/huangchengsir/pipewright/internal/pipeline"
)

type draftRow struct {
	id, token, project, request, input, policy, validator, patchDigest, applied string
	base, authz, created, expiry                                                int64
	ops                                                                         []Operation
}

const draftColumns = `id,owner_token_id,project_id,request_id,input_digest,base_revision,policy_version,validator_version,authz_revision,operations_json,patch_digest,created_at,expires_at,applied_operation_id`

func readDraft(ctx context.Context, q configstate.DBTX, where string, args ...any) (*draftRow, error) {
	d := &draftRow{}
	var raw string
	var applied sql.NullString
	e := q.QueryRowContext(ctx, `SELECT `+draftColumns+` FROM mcp_config_drafts WHERE `+where, args...).Scan(&d.id, &d.token, &d.project, &d.request, &d.input, &d.base, &d.policy, &d.validator, &d.authz, &raw, &d.patchDigest, &d.created, &d.expiry, &applied)
	if e != nil {
		return nil, e
	}
	if len(raw) > 16384 || json.Unmarshal([]byte(raw), &d.ops) != nil {
		return nil, ErrUnavailable
	}
	if !sameDigest(d.patchDigest, digest(d.ops)) {
		return nil, ErrUnavailable
	}
	d.applied = applied.String
	return d, nil
}
func (s *Service) Create(ctx context.Context, tokenID string, in CreateRequest) (*Draft, error) {
	if !validRequestID(in.RequestID) || in.ProjectID == "" {
		return nil, ErrInvalid
	}
	base, e := parseRevision(in.BaseRevision)
	if e != nil {
		return nil, e
	}
	g, e := s.prepare(ctx)
	if e != nil {
		return nil, e
	}
	ops, e := s.normalize(in.Operations, g)
	if e != nil {
		return nil, e
	}
	inputHash := digest(in)
	var out *Draft
	e = configstate.WithWriteTx(ctx, s.db, func(q configstate.DBTX) error {
		t, e := s.locks(ctx, q, tokenID, g)
		if e != nil {
			return e
		}
		if e = authorize(t, in.ProjectID, "propose", ops); e != nil {
			return e
		}
		// Persistent identity is resolved before mutable defaults and base checks.
		old, e := readDraft(ctx, q, `owner_token_id=? AND request_id=?`, tokenID, in.RequestID)
		if e == nil {
			if old.input != inputHash || old.project != in.ProjectID {
				return ErrIdempotency
			}
			out, e = s.draftView(ctx, q, t, old, g)
			return e
		}
		if !errors.Is(e, sql.ErrNoRows) {
			return e
		}
		d, e := s.load(ctx, q, in.ProjectID)
		if e != nil {
			return e
		}
		if d.rev != base {
			return ErrConflict
		}
		if d.policy != in.PolicyVersion {
			return ErrPolicy
		}
		_, diff, e := s.patch(d, ops, g)
		if e != nil {
			return e
		}
		validation := s.validateDocument(ctx, q, d, ops, g)
		if validation.State != "passed" {
			return Error(firstIssue(validation))
		}
		var count int
		if e = q.QueryRowContext(ctx, `SELECT COUNT(*) FROM mcp_config_drafts WHERE owner_token_id=? AND project_id=? AND expires_at>? AND applied_operation_id IS NULL`, tokenID, in.ProjectID, s.opts.Now().UnixMilli()).Scan(&count); e != nil {
			return e
		}
		if count >= 20 {
			return ErrQuota
		}
		if e = q.QueryRowContext(ctx, `SELECT COUNT(*) FROM mcp_config_drafts WHERE expires_at>? AND applied_operation_id IS NULL`, s.opts.Now().UnixMilli()).Scan(&count); e != nil {
			return e
		}
		if count >= 1000 {
			return ErrQuota
		}
		now := s.opts.Now()
		r := &draftRow{id: uuid.NewString(), token: tokenID, project: in.ProjectID, request: in.RequestID, input: inputHash, base: base, policy: d.policy, validator: ValidatorVersion, authz: t.AuthzRevision, ops: ops, patchDigest: digest(ops), created: now.UnixMilli(), expiry: now.Add(24 * time.Hour).UnixMilli()}
		if len(diff) == 0 {
			r.ops = []Operation{}
			r.patchDigest = digest(r.ops)
		}
		b, _ := json.Marshal(r.ops)
		_, e = q.ExecContext(ctx, `INSERT INTO mcp_config_drafts (`+draftColumns+`) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, r.id, r.token, r.project, r.request, r.input, r.base, r.policy, r.validator, r.authz, string(b), r.patchDigest, r.created, r.expiry, nil)
		if e != nil {
			return e
		}
		if e = s.audit(ctx, q, tokenID, "mcp_config_draft_create", in.ProjectID, r.id, operationNames(ops)); e != nil {
			return e
		}
		out, e = s.draftView(ctx, q, t, r, g)
		return e
	})
	if e != nil {
		return nil, e
	}
	if e = s.fresh(ctx, tokenID, g); e != nil {
		return nil, e
	}
	return out, nil
}
func firstIssue(v Validation) string {
	if len(v.Issues) > 0 {
		return v.Issues[0].Code
	}
	return "validation_blocked"
}
func operationNames(ops []Operation) []string {
	a := []string{}
	for _, o := range ops {
		if !contains(a, o.Type) {
			a = append(a, o.Type)
		}
	}
	return a
}
func (s *Service) validateDocument(ctx context.Context, q configstate.DBTX, d *document, ops []Operation, g guard) Validation {
	v := Validation{State: "passed", ApplyAllowed: true, Checks: []Check{}, Issues: []Issue{}}
	for _, category := range []string{"structural", "crossSection", "authorization", "policy", "sourceApplicability"} {
		v.Checks = append(v.Checks, Check{Category: category, Status: "passed"})
	}
	block := func(code string) {
		v.State = "blocked"
		v.ApplyAllowed = false
		v.Issues = append(v.Issues, Issue{Code: code})
		for i := range v.Checks {
			if v.Checks[i].Category == "policy" {
				v.Checks[i].Status = "failed"
				v.Checks[i].IssueCode = code
			}
		}
	}
	if err := configstate.CheckIdle(ctx, q, d.projectID); err != nil {
		code := string(ErrUnavailable)
		if errors.Is(err, configstate.ErrBusy) {
			code = string(ErrBusy)
		}
		block(code)
		v.Checks = append(v.Checks, Check{Category: "activity", Status: "failed", IssueCode: code})
		return v
	}
	v.Checks = append(v.Checks, Check{Category: "activity", Status: "passed"})
	if d.applicability != "supported" {
		block(string(ErrUnsupported))
		return v
	}
	candidate, _, patchErr := s.patch(d, ops, g)
	if patchErr != nil {
		block(Code(patchErr))
		return v
	}
	refs := map[string]bool{}
	if d.credential != "" {
		refs[d.credential] = true
	}
	for _, x := range d.settings.Build.Vars {
		if x.Secret {
			refs[x.CredentialID] = true
		}
	}
	for _, env := range d.settings.Environments {
		if env.ImageRegistry.CredentialID != "" {
			refs[env.ImageRegistry.CredentialID] = true
		}
		for _, x := range env.EnvVars {
			if x.Secret {
				refs[x.CredentialID] = true
			}
		}
	}
	for _, st := range d.settings.Steps {
		for _, x := range st.Env {
			if x.Secret {
				refs[x.CredentialID] = true
			}
		}
	}
	if len(refs) > 512 {
		block(string(ErrLarge))
		return v
	}
	for id := range refs {
		var n int
		if id == "" || q.QueryRowContext(ctx, `SELECT COUNT(*) FROM credentials WHERE id=?`, id).Scan(&n) != nil || n != 1 {
			block("reference_unknown")
			return v
		}
	}
	if d.credential == "" {
		block("reference_unknown")
		return v
	}
	// Legacy build declarations are not consumed by DAG jobs. Reuse the cross-
	// section validator with neutral build defaults, then validate actual jobs.
	in := pipeline.ValidationInput{Stages: candidate.spec.Stages, Build: pipeline.BuildConfig{Model: pipeline.BuildModelDockerfile, ArtifactType: pipeline.ArtifactImage}, ProjectCredentialOK: true, Events: d.validationEvents, BranchMappings: d.validationMappings}
	vars := func(xs []pipeline.BuildVar) []pipeline.ValidationVar {
		out := []pipeline.ValidationVar{}
		for _, x := range xs {
			out = append(out, pipeline.ValidationVar{Key: x.Key, Secret: x.Secret, CredentialID: x.CredentialID, CredentialExists: !x.Secret || refs[x.CredentialID]})
		}
		return out
	}
	in.BuildVars = vars(d.settings.Build.Vars)
	for _, env := range d.settings.Environments {
		in.Environments = append(in.Environments, pipeline.ValidationEnvironment{Name: env.Name, EnvVars: vars(env.EnvVars), ImageRegistry: pipeline.ValidationRegistry{Type: env.ImageRegistry.Type, URL: env.ImageRegistry.URL, CredentialID: env.ImageRegistry.CredentialID, CredentialExists: refs[env.ImageRegistry.CredentialID]}})
	}
	issues := append(pipeline.Validate(in), build.ConfigExecutionIssues(candidate.spec)...)
	for _, i := range issues {
		if i.Severity == pipeline.SeverityError {
			block(i.Code)
			return v
		}
	}
	return v
}
func (s *Service) reviewDigest(r *draftRow, diff []Diff) string {
	b, _ := json.Marshal(struct {
		ID, Token, Project, Policy, Validator, Patch string
		Base, Authz                                  int64
		Diff                                         []Diff
	}{r.id, r.token, r.project, r.policy, r.validator, r.patchDigest, r.base, r.authz, diff})
	m := hmac.New(sha256.New, s.cursorKey)
	m.Write(b)
	return base64.RawURLEncoding.EncodeToString(m.Sum(nil))
}
func (s *Service) draftView(ctx context.Context, q configstate.DBTX, t *auth.MCPToken, r *draftRow, g guard) (*Draft, error) {
	if r.token != t.ID {
		return nil, ErrForbidden
	}
	if len(r.ops) > 0 {
		if e := authorize(t, r.project, "propose", r.ops); e != nil {
			return nil, e
		}
	} else if !allowed(t, r.project, "read", "", "") {
		return nil, ErrForbidden
	}
	out := &Draft{DraftID: r.id, ProjectID: r.project, BaseRevision: revision(r.base), PolicyVersion: r.policy, State: "active", ExpiresAt: time.UnixMilli(r.expiry).UTC().Format(time.RFC3339Nano), Operations: []Operation{}, Diff: []Diff{}, Approval: Approval{State: "none"}, Complete: true, UntrustedData: true, ReviewURL: "/mcp/config-drafts/" + r.id + "/review"}
	if len(r.ops) == 0 {
		out.State = "no_changes"
		out.DraftID = ""
		out.ReviewURL = ""
		out.Validation = Validation{State: "passed", Checks: []Check{}, Issues: []Issue{}}
		return out, nil
	}
	if r.applied != "" {
		out.State = "applied"
	}
	if r.applied == "" && s.opts.Now().UnixMilli() >= r.expiry {
		out.State = "expired"
	}
	d, e := s.load(ctx, q, r.project)
	if e != nil {
		return nil, e
	}
	if r.base != d.rev || r.policy != d.policy || r.validator != ValidatorVersion || r.authz != t.AuthzRevision {
		if out.State != "applied" {
			out.State = "invalidated"
		}
		out.Validation = Validation{State: "stale", Checks: []Check{}, Issues: []Issue{{Code: "snapshot_stale"}}}
		out.Approval, e = s.approvalView(ctx, q, r, "")
		if e != nil {
			return nil, e
		}
		return out, nil
	}
	if _, e = s.normalize(r.ops, g); e != nil {
		return nil, ErrSecret
	}
	_, diff, e := s.patch(d, r.ops, g)
	if e != nil {
		return nil, e
	}
	out.Operations = r.ops
	out.Diff = diff
	out.Validation = s.validateDocument(ctx, q, d, r.ops, g)
	if authorize(t, r.project, "apply", r.ops) != nil {
		out.Validation.ApplyAllowed = false
		out.Validation.Issues = append(out.Validation.Issues, Issue{Code: "apply_permission_missing"})
	}
	out.ReviewDigest = s.reviewDigest(r, diff)
	if out.State != "active" {
		out.Validation.ApplyAllowed = false
	}
	out.Approval, e = s.approvalView(ctx, q, r, out.ReviewDigest)
	if e != nil {
		return nil, e
	}
	if !s.opts.ApplyEnabled {
		out.Validation.ApplyAllowed = false
		out.Approval.Eligible = false
		out.Approval.Reason = string(ErrDisabled)
	}
	if e = boundedOutput(out); e != nil {
		return nil, e
	}
	return out, nil
}
func (s *Service) Get(ctx context.Context, tokenID, draftID string) (*Draft, error) {
	return s.get(ctx, tokenID, draftID, "")
}

// GetPage is the MCP projection. Browser Review always returns the complete
// bounded diff, so a partial MCP page cannot stand in for human review.
func (s *Service) GetPage(ctx context.Context, tokenID, draftID, cursor string, size int) (*Draft, error) {
	n, e := pageSize(size)
	if e != nil {
		return nil, e
	}
	d, e := s.Get(ctx, tokenID, draftID)
	if e != nil {
		return nil, e
	}
	if cursor != "" && (d.State == "invalidated" || d.Validation.State == "stale") {
		return nil, ErrStale
	}
	c := pageCursor{Token: tokenID, Project: d.ProjectID, Section: "draft:" + draftID, Revision: d.BaseRevision, Policy: d.PolicyVersion}
	offset, e := s.offset(cursor, c)
	if e != nil {
		return nil, e
	}
	if offset > len(d.Diff) {
		return nil, ErrInvalid
	}
	end := offset + n
	if end > len(d.Diff) {
		end = len(d.Diff)
	}
	total := len(d.Diff)
	d.Diff = d.Diff[offset:end]
	d.Complete = offset == 0 && end == total
	if end < total {
		c.Offset = end
		d.NextCursor = s.cursor(c)
	}
	return d, nil
}
func (s *Service) Review(ctx context.Context, adminID, draftID string) (*Draft, error) {
	return s.get(ctx, "", draftID, adminID)
}
func (s *Service) get(ctx context.Context, tokenID, draftID, adminID string) (*Draft, error) {
	if draftID == "" {
		return nil, ErrInvalid
	}
	g, e := s.prepare(ctx)
	if e != nil {
		return nil, e
	}
	var out *Draft
	var owner string
	e = configstate.WithWriteTx(ctx, s.db, func(q configstate.DBTX) error {
		r, e := readDraft(ctx, q, `id=?`, draftID)
		if e != nil {
			return ErrForbidden
		}
		owner = r.token
		if adminID == "" && tokenID != owner {
			return ErrForbidden
		}
		t, e := s.locks(ctx, q, owner, g)
		if e != nil {
			return e
		}
		if adminID != "" {
			if s.opts.AuthorizeAdmin == nil || s.opts.AuthorizeAdmin(ctx, q, adminID) != nil {
				return ErrForbidden
			}
		}
		out, e = s.draftView(ctx, q, t, r, g)
		if e != nil {
			return e
		}
		actor := owner
		if adminID != "" {
			actor = "admin"
		}
		return s.audit(ctx, q, actor, "mcp_config_draft_read", r.project, r.id, operationNames(r.ops))
	})
	if e != nil {
		return nil, e
	}
	if e = s.fresh(ctx, owner, g); e != nil {
		return nil, e
	}
	return out, nil
}
func (s *Service) Validate(ctx context.Context, tokenID, draftID string) (*Validation, error) {
	d, e := s.Get(ctx, tokenID, draftID)
	if e != nil {
		return nil, e
	}
	return &d.Validation, nil
}

func (s *Service) approvalView(ctx context.Context, q configstate.DBTX, r *draftRow, review string) (Approval, error) {
	a := Approval{State: "none"}
	var expiry int64
	var revoked sql.NullInt64
	var consumed sql.NullString
	var digest string
	e := q.QueryRowContext(ctx, `SELECT id,expires_at,revoked_at,consumed_by_operation_id,review_digest FROM mcp_config_approvals WHERE draft_id=? ORDER BY created_at DESC,id DESC LIMIT 1`, r.id).Scan(&a.ID, &expiry, &revoked, &consumed, &digest)
	if errors.Is(e, sql.ErrNoRows) {
		return a, nil
	}
	if e != nil {
		return a, e
	}
	a.ExpiresAt = time.UnixMilli(expiry).UTC().Format(time.RFC3339Nano)
	a.State = "granted"
	a.Eligible = true
	if consumed.Valid {
		a.State = "consumed"
		a.Eligible = false
	} else if revoked.Valid {
		a.State = "revoked"
		a.Eligible = false
	} else if expiry <= s.opts.Now().UnixMilli() {
		a.State = "expired"
		a.Eligible = false
	} else if digest != review {
		a.State = "revoked"
		a.Eligible = false
		a.Reason = "snapshot_stale"
	}
	return a, nil
}
func (s *Service) Approve(ctx context.Context, adminID, draftID string, in ApprovalRequest) (*Approval, error) {
	if !s.opts.ApplyEnabled {
		return nil, ErrDisabled
	}
	g, e := s.prepare(ctx)
	if e != nil {
		return nil, e
	}
	var out *Approval
	var owner string
	e = configstate.WithWriteTx(ctx, s.db, func(q configstate.DBTX) error {
		r, e := readDraft(ctx, q, `id=?`, draftID)
		if e != nil {
			return ErrForbidden
		}
		owner = r.token
		t, e := s.locks(ctx, q, owner, g)
		if e != nil {
			return e
		}
		if s.opts.AuthorizeAdmin == nil || s.opts.AuthorizeAdmin(ctx, q, adminID) != nil {
			return ErrForbidden
		}
		if _, e = configstate.LockProject(ctx, q, s.db, r.project); e != nil {
			return e
		}
		if e = authorize(t, r.project, "apply", r.ops); e != nil {
			return e
		}
		view, e := s.draftView(ctx, q, t, r, g)
		if e != nil {
			return e
		}
		if view.State != "active" || !view.Validation.ApplyAllowed || len(view.Diff) == 0 {
			return ErrApproval
		}
		if in.ReviewDigest != view.ReviewDigest || in.BaseRevision != view.BaseRevision || in.PolicyVersion != view.PolicyVersion {
			return ErrApproval
		}
		id := uuid.NewString()
		now := s.opts.Now()
		expiry := now.Add(10 * time.Minute).UnixMilli()
		if expiry > r.expiry {
			expiry = r.expiry
		}
		if _, e = q.ExecContext(ctx, `UPDATE mcp_config_approvals SET revoked_at=? WHERE draft_id=? AND revoked_at IS NULL AND consumed_by_operation_id IS NULL`, now.UnixMilli(), r.id); e != nil {
			return e
		}
		_, e = q.ExecContext(ctx, `INSERT INTO mcp_config_approvals (id,draft_id,token_id,project_id,base_revision,policy_version,validator_version,authz_revision,patch_digest,review_digest,approver,created_at,expires_at) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?)`, id, r.id, r.token, r.project, r.base, r.policy, r.validator, r.authz, r.patchDigest, view.ReviewDigest, adminID, now.UnixMilli(), expiry)
		if e != nil {
			return e
		}
		if e = s.audit(ctx, q, "admin", "mcp_config_approve", r.project, r.id, operationNames(r.ops)); e != nil {
			return e
		}
		out = &Approval{ID: id, State: "granted", Eligible: true, ExpiresAt: time.UnixMilli(expiry).UTC().Format(time.RFC3339Nano)}
		return s.opts.AuthorizeAdmin(ctx, q, adminID)
	})
	if e != nil {
		return nil, e
	}
	if e = s.fresh(ctx, owner, g); e != nil {
		return nil, e
	}
	return out, nil
}

func sameDigest(a, b string) bool { return len(a) == len(b) && hmac.Equal([]byte(a), []byte(b)) }
