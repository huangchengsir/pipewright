// Package productrelease persists ordered release intentions. It never treats
// pipeline success or promotion metadata as proof of a deployed version.
package productrelease

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/huangchengsir/pipewright/internal/configstate"
	"github.com/huangchengsir/pipewright/internal/store"
	"github.com/huangchengsir/pipewright/internal/tenancy"
)

var (
	ErrUnavailable      = errors.New("productrelease: frozen execution unavailable")
	ErrInvalid          = errors.New("productrelease: invalid request")
	ErrConflict         = errors.New("productrelease: request or revision conflict")
	ErrNotFound         = errors.New("productrelease: not found")
	ErrBlocked          = errors.New("productrelease: release requires reconciliation")
	ErrDispatchRejected = errors.New("productrelease: run was not admitted")
)

const MaxItems = 50

type Principal struct{ TenantID, ActorID string }

// Authority must resolve an authenticated, current principal. Authorize must
// validate product membership and EVERY application's data/action scope. Write
// calls run inside the supplied transaction and must fence permission changes.
// An absent authority fails closed even on a single-administrator installation.
type Authority interface {
	Principal(context.Context) (Principal, error)
	Authorize(context.Context, string, []ItemPlan, bool) error
}

type ItemPlan struct {
	ApplicationID string   `json:"applicationId"`
	CommitSHA     string   `json:"commitSha"`
	Environment   string   `json:"environment"`
	TargetIDs     []string `json:"targetIds"`
	ConfigDigest  string   `json:"configDigest"`
	// SnapshotRef names a server-owned frozen plan, never client supplied YAML,
	// configuration JSON, credentials, or an instruction to replay an artifact.
	SnapshotRef string `json:"snapshotRef"`
}
type Plan struct {
	ProductID string     `json:"productId"`
	Items     []ItemPlan `json:"items"`
}
type Item struct {
	ApplicationID string `json:"applicationId"`
	State         string `json:"state"`
	RunID         string `json:"runId,omitempty"`
	Reason        string `json:"reason,omitempty"`
}
type Batch struct {
	ID                     string `json:"id"`
	ProductID              string `json:"productId"`
	Revision               int64  `json:"revision"`
	State                  string `json:"state"`
	Plan                   Plan   `json:"plan"`
	Items                  []Item `json:"items"`
	CreatedAt              int64  `json:"createdAt"`
	UpdatedAt              int64  `json:"updatedAt"`
	principal              Principal
	requestID, fingerprint string
}
type Identity struct{ Scope, Key, Fingerprint string }

// Receipt is internal evidence from the existing execution authority. A success
// requires independent deployment proof for the SAME frozen plan. A successful
// build, promotion row, stub, or merely accepted request is not such proof.
type Receipt struct {
	RunID              string
	State              string // running | waiting_approval | succeeded | failed | canceled | unknown
	VerifiedDeployment bool
	PlanDigest         string
}

// Executor is deliberately NOT a thin adapter over run.Service.Create.
// ValidateFrozen must reject unsupported source/target combinations and ensure
// SnapshotRef is server-owned, immutable and matches all public plan fields.
// StartFrozen must repeat authorization and exact-plan validation atomically at
// admission and preserve existing node approvals. Lookup MUST be read-only and
// must not create, re-enqueue or resume a run. Implementations must never fall
// back to branch HEAD, current configuration, unverified artifacts or system.
type Executor interface {
	ValidateFrozen(context.Context, ItemPlan) error
	StartFrozen(context.Context, ItemPlan, Identity) (Receipt, error)
	Lookup(context.Context, ItemPlan, Identity) (Receipt, bool, error)
	Observe(context.Context, ItemPlan, string) (Receipt, error)
}
type Service struct {
	db        *sql.DB
	authority Authority
	executor  Executor
}

func New(db *sql.DB, a Authority, e Executor) *Service {
	return &Service{db: db, authority: a, executor: e}
}
func (s *Service) Available() bool {
	return s != nil && s.db != nil && s.authority != nil && s.executor != nil
}
func digest(v any) string {
	b, _ := json.Marshal(v)
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}
func ItemDigest(v ItemPlan) string { return digest(v) }
func validID(v string) bool        { u, e := uuid.Parse(v); return e == nil && u.String() == v }
func validHash(v string, n int) bool {
	if len(v) != n {
		return false
	}
	b, e := hex.DecodeString(v)
	return e == nil && hex.EncodeToString(b) == v
}
func validate(p Plan) error {
	if !validID(p.ProductID) || len(p.Items) == 0 || len(p.Items) > MaxItems {
		return ErrInvalid
	}
	seen := map[string]bool{}
	for _, i := range p.Items {
		if !validID(i.ApplicationID) || seen[i.ApplicationID] || !validHash(i.CommitSHA, 40) || !validHash(i.ConfigDigest, 64) || !validID(i.SnapshotRef) || i.Environment == "" || len(i.Environment) > 128 || strings.TrimSpace(i.Environment) != i.Environment || strings.ContainsAny(i.Environment, "\r\n\x00") || len(i.TargetIDs) == 0 || len(i.TargetIDs) > 100 {
			return ErrInvalid
		}
		seen[i.ApplicationID] = true
		targets := map[string]bool{}
		for _, id := range i.TargetIDs {
			if id == "" || len(id) > 256 || strings.ContainsAny(id, "\r\n\x00") || targets[id] {
				return ErrInvalid
			}
			targets[id] = true
		}
	}
	return nil
}
func (s *Service) principal(ctx context.Context) (Principal, error) {
	if !s.Available() {
		return Principal{}, ErrUnavailable
	}
	p, e := s.authority.Principal(ctx)
	if e != nil {
		return p, e
	}
	if p.TenantID == "" || p.ActorID == "" || len(p.TenantID) > 128 || len(p.ActorID) > 128 {
		return Principal{}, ErrUnavailable
	}
	return p, nil
}

// Confirm persists only verified immutable intentions. It does not dispatch.
func (s *Service) Confirm(ctx context.Context, requestID string, plan Plan) (*Batch, error) {
	principal, e := s.principal(ctx)
	if e != nil {
		return nil, e
	}
	if !validID(requestID) {
		return nil, ErrInvalid
	}
	if e = validate(plan); e != nil {
		return nil, e
	}
	if _, available := s.executor.(Preparation); available && requestID != plan.Items[0].SnapshotRef {
		return nil, ErrConflict
	}
	// Copy caller-owned slices before validation and hashing.
	encoded, _ := json.Marshal(plan)
	if e = json.Unmarshal(encoded, &plan); e != nil {
		return nil, ErrInvalid
	}
	fingerprint := digest(plan)
	if old, e := s.byRequest(ctx, principal, requestID); e == nil {
		if old.fingerprint != fingerprint {
			return nil, ErrConflict
		}
		return s.authorized(ctx, old, false)
	} else if !errors.Is(e, ErrNotFound) {
		return nil, e
	}
	if checker, ok := s.executor.(interface {
		ValidateConfirmation(context.Context, Plan) error
	}); ok {
		if e = checker.ValidateConfirmation(ctx, plan); e != nil {
			return nil, e
		}
	}
	if e = s.authority.Authorize(ctx, plan.ProductID, plan.Items, true); e != nil {
		return nil, e
	}
	for _, i := range plan.Items {
		if e = s.executor.ValidateFrozen(ctx, i); e != nil {
			return nil, e
		}
	}
	now := time.Now().UTC().UnixMilli()
	b := &Batch{ID: uuid.NewString(), ProductID: plan.ProductID, Revision: 1, Plan: plan, CreatedAt: now, UpdatedAt: now, principal: principal, requestID: requestID, fingerprint: fingerprint, Items: make([]Item, len(plan.Items))}
	for n, i := range plan.Items {
		b.Items[n] = Item{ApplicationID: i.ApplicationID, State: "pending"}
	}
	itemJSON, _ := json.Marshal(b.Items)
	e = configstate.WithWriteTxContext(ctx, s.db, func(tx context.Context, q configstate.DBTX) error {
		current, e := s.authority.Principal(tx)
		if e != nil {
			return e
		}
		if current != principal {
			return ErrConflict
		}
		if e = s.authority.Authorize(tx, plan.ProductID, plan.Items, true); e != nil {
			return e
		}
		if checker, ok := s.executor.(interface {
			ValidateConfirmation(context.Context, Plan) error
		}); ok {
			if e = checker.ValidateConfirmation(tx, plan); e != nil {
				return e
			}
		}
		_, e = q.ExecContext(tx, `INSERT INTO product_release_batches(id,tenant_id,actor_id,product_id,request_id,fingerprint,revision,plan_json,items_json,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?)`, b.ID, principal.TenantID, principal.ActorID, b.ProductID, requestID, fingerprint, b.Revision, string(encoded), string(itemJSON), now, now)
		return e
	})
	if e != nil {
		if store.IsUniqueErr(e) {
			old, load := s.byRequest(ctx, principal, requestID)
			if load == nil && old.fingerprint == fingerprint {
				return s.authorized(ctx, old, false)
			}
			return nil, ErrConflict
		}
		return nil, e
	}
	b.State = state(b.Items)
	return b, nil
}
func state(items []Item) string {
	for _, i := range items {
		switch i.State {
		case "unknown", "dispatching":
			return "needs_reconciliation"
		case "failed", "canceled", "blocked":
			return "blocked"
		case "waiting_approval":
			return "waiting_approval"
		case "running":
			return "running"
		}
	}
	for _, i := range items {
		if i.State == "pending" {
			return "pending"
		}
	}
	return "succeeded"
}
func (s *Service) authorized(ctx context.Context, b *Batch, write bool) (*Batch, error) {
	if e := s.authority.Authorize(ctx, b.ProductID, b.Plan.Items, write); e != nil {
		return nil, e
	}
	if checker, ok := s.authority.(interface {
		AuthorizeRunRead(context.Context, string) error
	}); ok {
		for _, item := range b.Items {
			if item.RunID != "" {
				if err := checker.AuthorizeRunRead(ctx, item.RunID); err != nil {
					return nil, err
				}
			}
		}
	}
	b.State = state(b.Items)
	return b, nil
}

const columns = `id,tenant_id,actor_id,product_id,request_id,fingerprint,revision,plan_json,items_json,created_at,updated_at`

func scan(row interface{ Scan(...any) error }) (*Batch, error) {
	var b Batch
	var plan, items string
	e := row.Scan(&b.ID, &b.principal.TenantID, &b.principal.ActorID, &b.ProductID, &b.requestID, &b.fingerprint, &b.Revision, &plan, &items, &b.CreatedAt, &b.UpdatedAt)
	if errors.Is(e, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if e != nil {
		return nil, e
	}
	if json.Unmarshal([]byte(plan), &b.Plan) != nil || json.Unmarshal([]byte(items), &b.Items) != nil || len(b.Plan.Items) != len(b.Items) || validate(b.Plan) != nil || b.Plan.ProductID != b.ProductID || digest(b.Plan) != b.fingerprint {
		return nil, ErrConflict
	}
	for n, i := range b.Items {
		if i.ApplicationID != b.Plan.Items[n].ApplicationID {
			return nil, ErrConflict
		}
		switch i.State {
		case "pending", "dispatching", "running", "waiting_approval", "succeeded", "failed", "canceled", "blocked", "unknown":
		default:
			return nil, ErrConflict
		}
	}
	b.State = state(b.Items)
	return &b, nil
}
func (s *Service) byRequest(ctx context.Context, p Principal, key string) (*Batch, error) {
	return scan(configstate.Queryer(ctx, s.db).QueryRowContext(ctx, `SELECT `+columns+` FROM product_release_batches WHERE tenant_id=? AND actor_id=? AND request_id=?`, p.TenantID, p.ActorID, key))
}
func (s *Service) Get(ctx context.Context, id string) (*Batch, error) {
	p, e := s.principal(ctx)
	if e != nil {
		return nil, e
	}
	if !validID(id) {
		return nil, ErrNotFound
	}
	b, e := scan(configstate.Queryer(ctx, s.db).QueryRowContext(ctx, `SELECT `+columns+` FROM product_release_batches WHERE id=? AND tenant_id=? AND actor_id=?`, id, p.TenantID, p.ActorID))
	if e != nil {
		return nil, e
	}
	return s.authorized(ctx, b, false)
}
func identity(b *Batch, n int) Identity {
	return Identity{Scope: "product-release:" + b.ID, Key: fmt.Sprintf("item:%d", n), Fingerprint: ItemDigest(b.Plan.Items[n])}
}
func (s *Service) save(ctx context.Context, b *Batch, revision int64) error {
	raw, _ := json.Marshal(b.Items)
	b.UpdatedAt = time.Now().UTC().UnixMilli()
	r, e := configstate.Queryer(ctx, s.db).ExecContext(ctx, `UPDATE product_release_batches SET items_json=?,revision=revision+1,updated_at=? WHERE id=? AND revision=? AND tenant_id=? AND actor_id=?`, string(raw), b.UpdatedAt, b.ID, revision, b.principal.TenantID, b.principal.ActorID)
	if e != nil {
		return e
	}
	n, e := r.RowsAffected()
	if e != nil {
		return e
	}
	if n != 1 {
		return ErrConflict
	}
	b.Revision = revision + 1
	b.State = state(b.Items)
	return nil
}
func next(b *Batch) int {
	for n, i := range b.Items {
		if i.State != "succeeded" {
			return n
		}
	}
	return -1
}

// Advance claims at most one item. Persisting dispatching BEFORE calling the
// executor means a crash is reconciled, never retried with a fresh identity.
func (s *Service) Advance(ctx context.Context, id string, revision int64) (*Batch, error) {
	b, e := s.Get(ctx, id)
	if e != nil {
		return nil, e
	}
	if b.Revision != revision {
		return nil, ErrConflict
	}
	n := next(b)
	if n < 0 {
		return b, nil
	}
	if b.Items[n].State != "pending" {
		return b, ErrBlocked
	}
	if e = s.executor.ValidateFrozen(ctx, b.Plan.Items[n]); e != nil {
		if !errors.Is(e, ErrConflict) && !errors.Is(e, ErrNotFound) && !errors.Is(e, ErrUnsupported) {
			return nil, e
		}
		err := configstate.WithWriteTxContext(ctx, s.db, func(tx context.Context, q configstate.DBTX) error {
			if err := configstate.CheckMaintenanceAdmission(tx, q); err != nil {
				return err
			}
			if err := tenancy.LockAuthorization(tx, s.db); err != nil {
				return err
			}
			if _, err := s.authorized(tx, b, false); err != nil {
				return err
			}
			b.Items[n].State, b.Items[n].Reason = "blocked", "frozen_plan_changed"
			return s.save(tx, b, revision)
		})
		if err != nil {
			return nil, err
		}
		return b, nil
	}
	e = configstate.WithWriteTxContext(ctx, s.db, func(tx context.Context, _ configstate.DBTX) error {
		if _, e := s.authorized(tx, b, true); e != nil {
			return e
		}
		b.Items[n].State = "dispatching"
		return s.save(tx, b, revision)
	})
	if e != nil {
		return nil, e
	}
	receipt, startErr := s.executor.StartFrozen(ctx, b.Plan.Items[n], identity(b, n))
	switch {
	case errors.Is(startErr, ErrDispatchRejected):
		b.Items[n].State = "blocked"
		b.Items[n].Reason = "admission_rejected"
	case startErr != nil:
		b.Items[n].State = "unknown"
		b.Items[n].Reason = "dispatch_result_unknown"
	default:
		applyReceipt(b, n, receipt)
	}
	// Preserve the original identity even if recording acknowledgement fails.
	finish, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	if e = s.save(finish, b, b.Revision); e != nil {
		return nil, e
	}
	return b, nil
}
func applyReceipt(b *Batch, n int, r Receipt) {
	i := &b.Items[n]
	if r.RunID == "" || len(r.RunID) > 128 || (i.RunID != "" && i.RunID != r.RunID) {
		i.State = "unknown"
		i.Reason = "receipt_identity_mismatch"
		return
	}
	i.RunID = r.RunID
	i.Reason = ""
	switch r.State {
	case "running", "waiting_approval", "failed", "canceled", "unknown":
		i.State = r.State
	case "succeeded":
		if r.VerifiedDeployment && r.PlanDigest == ItemDigest(b.Plan.Items[n]) {
			i.State = "succeeded"
		} else {
			i.State = "unknown"
			i.Reason = "deployment_not_verified"
		}
	default:
		i.State = "unknown"
		i.Reason = "receipt_state_unknown"
	}
}

// Reconcile never dispatches. A missing lookup after a crash remains unknown;
// absence is not proof that the first attempt never ran.
func (s *Service) Reconcile(ctx context.Context, id string, revision int64) (*Batch, error) {
	b, e := s.Get(ctx, id)
	if e != nil {
		return nil, e
	}
	if b.Revision != revision {
		return nil, ErrConflict
	}
	n := next(b)
	if n < 0 {
		return b, nil
	}
	switch b.Items[n].State {
	case "running", "waiting_approval", "dispatching", "unknown":
	default:
		return b, ErrBlocked
	}
	var r Receipt
	var found bool
	if b.Items[n].RunID != "" {
		r, e = s.executor.Observe(ctx, b.Plan.Items[n], b.Items[n].RunID)
		found = e == nil
	} else {
		r, found, e = s.executor.Lookup(ctx, b.Plan.Items[n], identity(b, n))
	}
	if e != nil {
		return nil, e
	}
	if !found && b.Items[n].State == "dispatching" {
		// The original claim may still be inside admission. A missing run cannot
		// advance this revision or replace a later positive acknowledgement.
		return b, nil
	}
	if !found {
		b.Items[n].State = "unknown"
		b.Items[n].Reason = "original_run_not_proven"
	} else {
		applyReceipt(b, n, r)
	}
	e = configstate.WithWriteTxContext(ctx, s.db, func(tx context.Context, q configstate.DBTX) error {
		if err := configstate.CheckMaintenanceAdmission(tx, q); err != nil {
			return err
		}
		if err := tenancy.LockAuthorization(tx, s.db); err != nil {
			return err
		}
		if _, err := s.authorized(tx, b, false); err != nil {
			return err
		}
		return s.save(tx, b, revision)
	})
	if e != nil {
		return nil, e
	}
	return b, nil
}
