package kubernetes

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"github.com/huangchengsir/pipewright/internal/store"
	"sort"

	"github.com/google/uuid"
	"github.com/huangchengsir/pipewright/internal/configstate"
)

const releasePlanPurpose = "pipewright.k8s.release-plan.v2"
const releaseActionPurpose = "pipewright.k8s.release-action.v2"
const releaseActionReceiptPurpose = "pipewright.k8s.release-receipt.v2"
const ReleaseActionAnnotation = "pipewright.io/release-action"

// ReleasePlan is private, immutable and authenticated. Public identities are
// hashes of its randomized ciphertext, never hashes of low-entropy secrets.
type ReleasePlan struct {
	MutationVersion  int                        `json:"mutationVersion,omitempty"`
	ForceApproval    bool                       `json:"forceApproval,omitempty"`
	RollbackRevision int64                      `json:"rollbackRevision,omitempty"`
	RollbackPlanID   string                     `json:"rollbackPlanId,omitempty"`
	Retained         []ReleaseRollbackRetention `json:"retained,omitempty"`
	CoreDNS          *ReleaseCoreDNSPlan        `json:"coreDNS,omitempty"`
	RetryOf          string                     `json:"retryOf,omitempty"`
	Verify           []ReleaseResource          `json:"verify,omitempty"`
	Helm             *ReleaseHelmPlan           `json:"helm,omitempty"`
	Progressive      *ReleaseProgressivePlan    `json:"progressive,omitempty"`
	Deletes          []ReleaseResourceID        `json:"deletes,omitempty"`
	Version          int                        `json:"version"`
	Mode             string                     `json:"mode"`
	Authorization    ReleaseAuthorization       `json:"authorization"`
	Resources        []ReleaseResource          `json:"resources"`
	Batches          [][]ReleaseResource        `json:"batches,omitempty"`
	Input            *FrozenManifest            `json:"input,omitempty"`
	Config           json.RawMessage            `json:"config"`
	TimeoutSeconds   int                        `json:"timeoutSeconds"`
	ObserveSeconds   int                        `json:"observeSeconds"`
	RollbackOf       string                     `json:"rollbackOf,omitempty"`
	Baseline         []ReleaseResource          `json:"baseline,omitempty"`
	Chart            []byte                     `json:"chart,omitempty"`
	Values           []byte                     `json:"values,omitempty"`
}
type ReleaseRecord struct {
	ReaderOwnerEpoch string       `json:"-"`
	RollbackOf       string       `json:"rollbackOf,omitempty"`
	ID               string       `json:"id"`
	RunID            string       `json:"runId"`
	NodeKey          string       `json:"nodeKey"`
	OwnerEpoch       string       `json:"-"`
	Revision         int64        `json:"revision"`
	State            string       `json:"state"`
	Cleanup          CleanupState `json:"cleanup"`
	OwnerFenced      bool         `json:"ownerFenced"`
	PlanID           string       `json:"planId"`
	ApprovedID       string       `json:"approvedId,omitempty"`
	Cursor           int          `json:"cursor"`
	ReasonCode       string       `json:"reasonCode,omitempty"`
	CreatedAt        int64        `json:"createdAt"`
	UpdatedAt        int64        `json:"updatedAt"`
	Plan             ReleasePlan  `json:"-"`
}
type ReleaseActionIntent struct {
	MutationVersion          int               `json:"mutationVersion,omitempty"`
	ContentType              string            `json:"contentType,omitempty"`
	Version                  int               `json:"version"`
	ParentID                 string            `json:"parentId"`
	StepKey                  string            `json:"stepKey"`
	Resource                 ReleaseResourceID `json:"resource"`
	Verb                     string            `json:"verb"`
	Body                     []byte            `json:"body,omitempty"`
	ExpectedUID              string            `json:"expectedUid,omitempty"`
	ExpectedRV               string            `json:"expectedRv,omitempty"`
	AuthorizationFingerprint string            `json:"authorizationFingerprint"`
}
type ReleaseActionReceipt struct {
	Deleted         bool                  `json:"deleted,omitempty"`
	Checks          []ReleaseCheckReceipt `json:"checks,omitempty"`
	Resource        ReleaseResourceID     `json:"resource"`
	UID             string                `json:"uid"`
	ResourceVersion string                `json:"resourceVersion"`
	Object          map[string]any        `json:"object,omitempty"`
	Health          ReleaseHealth         `json:"health"`
	ObservedAt      int64                 `json:"observedAt"`
}
type ReleaseAction struct {
	ID         string                `json:"id"`
	ReleaseID  string                `json:"releaseId"`
	OwnerEpoch string                `json:"-"`
	Revision   int64                 `json:"revision"`
	State      string                `json:"state"`
	IntentID   string                `json:"intentId"`
	Intent     ReleaseActionIntent   `json:"-"`
	Receipt    *ReleaseActionReceipt `json:"-"`
}

func (s *Service) CreateRelease(ctx context.Context, run, node, owner string, plan ReleasePlan) (ReleaseRecord, error) {
	out := ReleaseRecord{}
	if !validID(run) || !cleanText(node, 128) || !validID(owner) || plan.Version != 2 || len(plan.Resources) == 0 || len(plan.Resources) > MaxReleaseResources || plan.TimeoutSeconds < 30 || plan.TimeoutSeconds > 1800 || plan.ObserveSeconds < 0 || plan.ObserveSeconds > 600 || plan.ObserveSeconds >= plan.TimeoutSeconds {
		return out, ErrInvalid
	}
	switch plan.Mode {
	case "image", "manifest", "helm", "progressive", "rollback", "coredns":
	default:
		return out, ErrInvalid
	}
	if plan.MutationVersion < 0 || plan.MutationVersion > 1 {
		return out, ErrInvalid
	}
	for _, batch := range plan.Batches {
		for _, resource := range batch {
			if !validReleaseMutation(resource) || plan.MutationVersion == 0 && resource.Mutation != nil {
				return out, ErrInvalid
			}
			if resource.Mutation != nil && resource.Mutation.Strategy == releasePriorCreateMerge && !releasePriorCreateStepValid(plan, resource) {
				return out, ErrInvalid
			}
		}
	}
	if plan.Helm != nil {
		for _, resource := range plan.Helm.Rendered {
			if !validReleaseMutation(resource) || plan.MutationVersion == 0 && resource.Mutation != nil {
				return out, ErrInvalid
			}
		}
	}
	if !validReleaseDaemonProof(plan) {
		return out, ErrInvalid
	}
	if !validCoreDNSFinalization(plan) {
		return out, ErrInvalid
	}
	seen := map[string]bool{}
	for _, r := range plan.Resources {
		if r.Mutation != nil && r.Mutation.Strategy == releasePriorCreateMerge && !releasePriorCreateStepValid(plan, r) {
			return out, ErrInvalid
		}
		if r.Mutation != nil && r.Mutation.Strategy == releaseHelmOwnedPostMerge && plan.Helm == nil {
			return out, ErrInvalid
		}
		if !validReleaseMutation(r) || plan.MutationVersion == 0 && r.Mutation != nil {
			return out, ErrInvalid
		}
		b, e := json.Marshal(r.Object)
		if e != nil || len(b) > MaxReleaseObjectBytes || r.Digest != releaseByteDigest(b) || seen[r.ID.Key()] {
			return out, ErrInvalid
		}
		seen[r.ID.Key()] = true
	}
	e := s.write(ctx, func(ctx context.Context, q dbtx) error {
		for _, resource := range plan.Resources {
			if e := s.validateHelmMutationProof(ctx, q, plan, resource); e != nil {
				return e
			}
		}
		if plan.RollbackOf != "" {
			if e := s.validateRollbackSource(ctx, q, plan); e != nil {
				return e
			}
		}
		if e := s.validateReleaseAuthorization(ctx, q, plan.Authorization); e != nil {
			return e
		}
		var count int
		if e := q.QueryRowContext(ctx, `SELECT COUNT(*) FROM k8s_releases WHERE run_id=? AND node_key=?`, run, node).Scan(&count); e != nil {
			return e
		}
		if count > 0 {
			return ErrConflict
		}
		id := "krel_" + uuid.NewString()
		body, e := json.Marshal(plan)
		if e != nil || len(body) > 48<<20 {
			return ErrInvalid
		}
		defer clear(body)
		cipher, e := s.seal(releasePlanPurpose, id, 1, body)
		if e != nil {
			return e
		}
		now := s.now().UnixMilli()
		readerOwner, e := releaseReaderOwner(ctx, q, run)
		if e != nil {
			return e
		}
		out = ReleaseRecord{ReaderOwnerEpoch: readerOwner, RollbackOf: plan.RollbackOf, ID: id, RunID: run, NodeKey: node, OwnerEpoch: owner, Revision: 1, State: "prepared", Cleanup: CleanupVerified, PlanID: releaseByteDigest(cipher), CreatedAt: now, UpdatedAt: now, Plan: plan}
		_, e = q.ExecContext(ctx, `INSERT INTO k8s_releases(id,run_id,node_key,project_id,connection_id,credential_id,mode,owner_epoch,revision,state,cleanup_state,plan_cipher,plan_id,created_at,updated_at,rollback_of,reader_owner_epoch) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, id, run, node, plan.Authorization.Request.ProjectID, plan.Authorization.Request.ConnectionID, plan.Authorization.Request.CredentialID, plan.Mode, owner, 1, out.State, out.Cleanup, cipher, out.PlanID, now, now, plan.RollbackOf, readerOwner)
		return e
	})
	return out, e
}
func (s *Service) release(ctx context.Context, q dbtx, id string) (ReleaseRecord, error) {
	var out ReleaseRecord
	var cipher []byte
	e := q.QueryRowContext(ctx, `SELECT id,run_id,node_key,owner_epoch,revision,state,cleanup_state,owner_fenced,plan_cipher,plan_id,approved_id,batch_cursor,reason_code,created_at,updated_at,rollback_of,reader_owner_epoch FROM k8s_releases WHERE id=?`, id).Scan(&out.ID, &out.RunID, &out.NodeKey, &out.OwnerEpoch, &out.Revision, &out.State, &out.Cleanup, &out.OwnerFenced, &cipher, &out.PlanID, &out.ApprovedID, &out.Cursor, &out.ReasonCode, &out.CreatedAt, &out.UpdatedAt, &out.RollbackOf, &out.ReaderOwnerEpoch)
	if e != nil {
		return out, rowError(e)
	}
	if releaseByteDigest(cipher) != out.PlanID {
		return out, ErrDecrypt
	}
	b, e := s.open(releasePlanPurpose, id, 1, cipher)
	if e != nil {
		return out, e
	}
	defer clear(b)
	if json.Unmarshal(b, &out.Plan) != nil || out.Plan.Version != 2 || out.Plan.RollbackOf != out.RollbackOf {
		return out, ErrDecrypt
	}
	return out, nil
}
func (s *Service) GetRelease(ctx context.Context, id string) (ReleaseRecord, error) {
	return s.release(ctx, configstate.Queryer(ctx, s.db), id)
}
func (s *Service) ReleaseForNode(ctx context.Context, run, node string) (ReleaseRecord, error) {
	var id string
	e := configstate.Queryer(ctx, s.db).QueryRowContext(ctx, `SELECT id FROM k8s_releases WHERE run_id=? AND node_key=?`, run, node).Scan(&id)
	if e != nil {
		return ReleaseRecord{}, rowError(e)
	}
	return s.GetRelease(ctx, id)
}
func (s *Service) mutateRelease(ctx context.Context, id, owner string, expected int64, fn func(context.Context, dbtx, *ReleaseRecord) error) (ReleaseRecord, error) {
	var out ReleaseRecord
	if !validRevision(expected) {
		return out, ErrInvalid
	}
	e := s.write(ctx, func(ctx context.Context, q dbtx) error {
		r, e := s.release(ctx, q, id)
		if e != nil {
			return e
		}
		if r.Revision != expected {
			return ErrConflict
		}
		if r.OwnerEpoch != owner || r.OwnerFenced {
			return ErrOwner
		}
		if e = fn(ctx, q, &r); e != nil {
			return e
		}
		r.Revision++
		r.UpdatedAt = s.now().UnixMilli()
		e = requireOne(q.ExecContext(ctx, `UPDATE k8s_releases SET revision=?,state=?,cleanup_state=?,owner_fenced=?,approved_id=?,batch_cursor=?,reason_code=?,updated_at=? WHERE id=? AND revision=?`, r.Revision, r.State, r.Cleanup, r.OwnerFenced, r.ApprovedID, r.Cursor, r.ReasonCode, r.UpdatedAt, id, expected))
		out = r
		return e
	})
	return out, e
}
func (s *Service) ApproveRelease(ctx context.Context, id, owner string, expected int64, planID string) (ReleaseRecord, error) {
	return s.mutateRelease(ctx, id, owner, expected, func(ctx context.Context, q dbtx, r *ReleaseRecord) error {
		if r.PlanID != planID || r.State != "prepared" && r.State != "awaiting_approval" {
			return ErrConflict
		}
		if e := s.validateReleaseAuthorization(ctx, q, r.Plan.Authorization); e != nil {
			return e
		}
		r.ApprovedID = planID
		r.State = "running"
		return nil
	})
}
func (s *Service) ListReleases(ctx context.Context, run string) ([]ReleaseRecord, error) {
	q := configstate.Queryer(ctx, s.db)
	rows, e := q.QueryContext(ctx, `SELECT id FROM k8s_releases WHERE run_id=? ORDER BY created_at,id`+store.Limit(store.DialectOf(s.db), "500"), run)
	if e != nil {
		return nil, e
	}
	var ids []string
	for rows.Next() {
		var id string
		if e = rows.Scan(&id); e != nil {
			rows.Close()
			return nil, e
		}
		ids = append(ids, id)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return nil, e
	}
	out := []ReleaseRecord{}
	for _, id := range ids {
		r, e := s.GetRelease(ctx, id)
		if e != nil {
			return nil, e
		}
		out = append(out, r)
	}
	return out, nil
}
func releaseGateKey(cluster string, id ReleaseResourceID) string {
	return fingerprint([]string{cluster, id.Group, id.Resource, id.Namespace, id.Name})
}
func (s *Service) takeReleaseGate(ctx context.Context, q dbtx, key, kind, id, owner string) error {
	var actualKind, actualID, epoch string
	e := q.QueryRowContext(ctx, `SELECT owner_kind,owner_id,owner_epoch FROM k8s_resource_gates WHERE resource_key=?`, key).Scan(&actualKind, &actualID, &epoch)
	if e == nil {
		if actualKind == kind && actualID == id && epoch == owner {
			return nil
		}
		return ErrHeld
	}
	if !errors.Is(e, sql.ErrNoRows) {
		return e
	}
	_, e = q.ExecContext(ctx, `INSERT INTO k8s_resource_gates(resource_key,owner_kind,owner_id,owner_epoch,created_at) VALUES(?,?,?,?,?)`, key, kind, id, owner, s.now().UnixMilli())
	return e
}

// bridgeV1Gates runs under the same epoch/write lock as all acquisitions. It
// decrypts immutable legacy intents; no cluster reads or TTL stealing occur.
func (s *Service) bridgeV1Gates(ctx context.Context, q dbtx) error {
	rows, e := q.QueryContext(ctx, `SELECT operation_id FROM k8s_workload_holds ORDER BY operation_id`)
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
	for _, id := range ids {
		o, e := s.operation(ctx, q, id)
		if e != nil {
			return e
		}
		t := o.Intent.Authorization.Target
		resource := ReleaseResourceID{Group: "apps", Resource: "deployments", Namespace: t.Namespace, Name: t.Deployment}
		if e = s.takeReleaseGate(ctx, q, releaseGateKey(o.Intent.Authorization.ClusterKey, resource), "v1", o.ID, o.OwnerEpoch); e != nil {
			return e
		}
	}
	return nil
}
func (s *Service) BridgeReleaseGates(ctx context.Context) error { return s.write(ctx, s.bridgeV1Gates) }
func (s *Service) AcquireReleaseGates(ctx context.Context, id, owner string) error {
	return s.write(ctx, func(ctx context.Context, q dbtx) error {
		if e := s.bridgeV1Gates(ctx, q); e != nil {
			return e
		}
		r, e := s.release(ctx, q, id)
		if e != nil {
			return e
		}
		if r.OwnerEpoch != owner || r.OwnerFenced {
			return ErrOwner
		}
		if e = s.validateReleaseAuthorization(ctx, q, r.Plan.Authorization); e != nil {
			return e
		}
		if e = s.transferRollbackGates(ctx, q, r); e != nil {
			return e
		}
		keys := []string{}
		for _, resource := range r.Plan.Authorization.Request.Resources {
			keys = append(keys, releaseGateKey(r.Plan.Authorization.ClusterKey, resource.ID))
		}
		sort.Strings(keys)
		for _, key := range keys {
			if e = s.takeReleaseGate(ctx, q, key, "v2", id, owner); e != nil {
				return e
			}
		}
		return nil
	})
}
func (s *Service) ReleaseGates(ctx context.Context, id, owner string) error {
	return s.write(ctx, func(ctx context.Context, q dbtx) error {
		r, e := s.release(ctx, q, id)
		if e != nil {
			return e
		}
		if r.OwnerEpoch != owner || r.Cleanup != CleanupVerified || !r.OwnerFenced || r.State == "paused" || r.State == "outcome_unknown" || r.State == "running" {
			return ErrHeld
		}
		var count int
		if e = q.QueryRowContext(ctx, `SELECT COUNT(*) FROM k8s_release_actions WHERE release_id=? AND state IN ('outcome_unknown','submitted')`, id).Scan(&count); e != nil {
			return e
		}
		if count != 0 {
			return ErrHeld
		}
		_, e = q.ExecContext(ctx, `DELETE FROM k8s_resource_gates WHERE owner_kind='v2' AND owner_id=? AND owner_epoch=?`, id, owner)
		return e
	})
}

func (s *Service) action(ctx context.Context, q dbtx, id string) (ReleaseAction, error) {
	var a ReleaseAction
	var intent, receipt []byte
	e := q.QueryRowContext(ctx, `SELECT id,release_id,owner_epoch,revision,state,intent_cipher,intent_id,receipt_cipher FROM k8s_release_actions WHERE id=?`, id).Scan(&a.ID, &a.ReleaseID, &a.OwnerEpoch, &a.Revision, &a.State, &intent, &a.IntentID, &receipt)
	if e != nil {
		return a, rowError(e)
	}
	if releaseByteDigest(intent) != a.IntentID {
		return a, ErrDecrypt
	}
	b, e := s.open(releaseActionPurpose, id, 1, intent)
	if e != nil {
		return a, e
	}
	defer clear(b)
	if json.Unmarshal(b, &a.Intent) != nil {
		return a, ErrDecrypt
	}
	if len(receipt) > 0 {
		b, e := s.open(releaseActionReceiptPurpose, id, a.Revision, receipt)
		if e != nil {
			return a, e
		}
		defer clear(b)
		if json.Unmarshal(b, &a.Receipt) != nil {
			return a, ErrDecrypt
		}
	}
	return a, nil
}
func (s *Service) GetReleaseAction(ctx context.Context, id string) (ReleaseAction, error) {
	return s.action(ctx, configstate.Queryer(ctx, s.db), id)
}
func (s *Service) PrepareReleaseAction(ctx context.Context, id, owner string, expected int64, in ReleaseActionIntent) (ReleaseAction, error) {
	out := ReleaseAction{}
	if in.ContentType != "" && (in.MutationVersion != 1 || in.Verb != "patch") {
		return out, ErrDenied
	}
	if in.Version != 2 || !cleanText(in.StepKey, 128) || len(in.Body) > MaxReleaseObjectBytes {
		return out, ErrInvalid
	}
	e := s.write(ctx, func(ctx context.Context, q dbtx) error {
		r, e := s.release(ctx, q, id)
		if e != nil {
			return e
		}
		if in.MutationVersion != r.Plan.MutationVersion {
			return ErrDenied
		}
		if r.Revision != expected || r.PlanID != in.ParentID || in.AuthorizationFingerprint != r.Plan.Authorization.Fingerprint {
			return ErrConflict
		}
		if r.OwnerEpoch != owner || r.OwnerFenced || r.State != "running" {
			return ErrOwner
		}
		if (r.Plan.Authorization.RequireApproval || r.Plan.ForceApproval) && r.ApprovedID != r.PlanID {
			return ErrDenied
		}
		if e = s.validateReleaseAuthorization(ctx, q, r.Plan.Authorization); e != nil {
			return e
		}
		if e = s.VerifyReleaseWriterLease(ctx, q, id, owner); e != nil {
			return e
		}
		var gateKind, gateOwner, epoch string
		e = q.QueryRowContext(ctx, `SELECT owner_kind,owner_id,owner_epoch FROM k8s_resource_gates WHERE resource_key=?`, releaseGateKey(r.Plan.Authorization.ClusterKey, releaseActionGateID(r.Plan, in.Resource))).Scan(&gateKind, &gateOwner, &epoch)
		if e != nil || gateKind != "v2" || gateOwner != id || epoch != owner {
			return ErrHeld
		}
		var unsettled int
		if e = q.QueryRowContext(ctx, `SELECT COUNT(*) FROM k8s_release_actions WHERE release_id=? AND state='outcome_unknown'`, id).Scan(&unsettled); e != nil {
			return e
		}
		if unsettled > 0 {
			return ErrOutcomeUnknown
		}
		allowed := false
		for _, scope := range r.Plan.Authorization.Request.Resources {
			if scope.ID == releaseActionGateID(r.Plan, in.Resource) && releaseStringSetContains(scope.Actions, []string{in.Verb}) {
				allowed = true
				break
			}
		}
		if !allowed {
			return ErrDenied
		}
		if in.Verb != "create" && in.Verb != "patch" && in.Verb != "update" && in.Verb != "delete" {
			return ErrInvalid
		}
		if in.Verb != "create" && (in.ExpectedUID == "" || in.ExpectedRV == "") {
			return ErrInvalid
		}
		// The controller supplies an exact planned object, with only current CAS
		// metadata added. Dynamic tool output never acquires arbitrary write rights.
		actionID := "kact_" + uuid.NewString()
		if in.Verb == "delete" {
			declared := false
			for _, target := range r.Plan.Deletes {
				declared = declared || target == in.Resource
			}
			if !declared {
				return ErrDenied
			}
			var options struct {
				Kind          string `json:"kind"`
				Preconditions struct {
					UID string `json:"uid"`
					RV  string `json:"resourceVersion"`
				} `json:"preconditions"`
			}
			if json.Unmarshal(in.Body, &options) != nil || options.Kind != "DeleteOptions" || options.Preconditions.UID != in.ExpectedUID || options.Preconditions.RV != in.ExpectedRV {
				return ErrInvalid
			}
		}
		if in.Verb != "delete" {
			var obj map[string]any
			if json.Unmarshal(in.Body, &obj) != nil {
				return ErrInvalid
			}
			meta := releaseMap(obj["metadata"])
			if releaseString(meta["name"]) != in.Resource.Name || releaseString(meta["namespace"]) != in.Resource.Namespace || obj["kind"] != in.Resource.Kind {
				return ErrInvalid
			}
			if in.Verb != "create" && (meta["uid"] != in.ExpectedUID || meta["resourceVersion"] != in.ExpectedRV) {
				return ErrConflict
			}
			delete(meta, "uid")
			delete(meta, "resourceVersion")
			planned := false
			resources := append([]ReleaseResource(nil), r.Plan.Resources...)
			if in.StepKey == releaseCoreDNSFinalizeStep {
				if r.Plan.CoreDNS == nil || r.Plan.CoreDNS.Finalize == nil || in.Verb != "patch" {
					return ErrDenied
				}
				if e := s.validateCoreDNSFinalizeIntent(ctx, r, in); e != nil {
					return e
				}
				resources = []ReleaseResource{*r.Plan.CoreDNS.Finalize}
			}

			if r.Plan.Helm != nil {
				resources = append(resources, r.Plan.Helm.Rendered...)
			}
			if r.Plan.Mode == "progressive" && r.Cursor < len(r.Plan.Batches) {
				resources = r.Plan.Batches[r.Cursor]
			}
			for _, resource := range resources {
				if resource.ID == in.Resource && ReleaseObjectMatches(releaseMutationObject(resource), obj) && ReleaseObjectMatches(obj, releaseMutationObject(resource)) {
					if !releaseResourceAdministrativeAllowed(r.Plan, resource) {
						return ErrDenied
					}
					expectedContentType := ""
					if resource.Mutation != nil {
						expectedContentType = resource.Mutation.ContentType
					}
					if in.ContentType != expectedContentType {
						return ErrDenied
					}
					if e := s.validateHelmMutationProof(ctx, q, r.Plan, resource); e != nil {
						return e
					}
					if resource.Mutation != nil && resource.Mutation.Strategy == releasePriorCreateMerge {
						if e := s.validatePriorCreateIntent(ctx, q, r, resource, in.ExpectedUID); e != nil {
							return e
						}
					}
					planned = true
					break
				}
			}
			if !planned && !releaseHelmStorageAllowed(r.Plan, in.Resource, obj) {
				return ErrDenied
			}
			annotations := releaseMap(meta["annotations"])
			if annotations == nil {
				annotations = map[string]any{}
				meta["annotations"] = annotations
			}
			annotations[ReleaseActionAnnotation] = actionID
			if in.Verb != "create" {
				meta["uid"] = in.ExpectedUID
				meta["resourceVersion"] = in.ExpectedRV
			}
			in.Body, _ = json.Marshal(obj)
		}
		var count int
		if e = q.QueryRowContext(ctx, `SELECT COUNT(*) FROM k8s_release_actions WHERE release_id=? AND step_key=?`, id, in.StepKey).Scan(&count); e != nil {
			return e
		}
		if count > 0 {
			return ErrConflict
		}

		b, _ := json.Marshal(in)
		defer clear(b)
		cipher, e := s.seal(releaseActionPurpose, actionID, 1, b)
		if e != nil {
			return e
		}
		out = ReleaseAction{ID: actionID, ReleaseID: id, OwnerEpoch: owner, Revision: 1, State: "outcome_unknown", IntentID: releaseByteDigest(cipher), Intent: in}
		now := s.now().UnixMilli()
		_, e = q.ExecContext(ctx, `INSERT INTO k8s_release_actions(id,release_id,step_key,resource_key,owner_epoch,revision,state,intent_cipher,intent_id,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?)`, actionID, id, in.StepKey, releaseGateKey(r.Plan.Authorization.ClusterKey, releaseActionGateID(r.Plan, in.Resource)), owner, 1, out.State, cipher, out.IntentID, now, now)
		return e
	})
	return out, e
}
func (s *Service) RecordReleaseAction(ctx context.Context, id, owner string, expected int64, state string, receipt ReleaseActionReceipt) (ReleaseAction, error) {
	var out ReleaseAction
	if state != "submitted" && state != "verified" && state != "rejected" && state != "outcome_unknown" {
		return out, ErrInvalid
	}
	e := s.write(ctx, func(ctx context.Context, q dbtx) error {
		a, e := s.action(ctx, q, id)
		if e != nil {
			return e
		}
		r, e := s.release(ctx, q, a.ReleaseID)
		if e != nil {
			return e
		}
		if a.Revision != expected {
			return ErrConflict
		}
		if a.OwnerEpoch != owner || r.OwnerEpoch != owner || r.OwnerFenced {
			return ErrOwner
		}
		if a.State == "verified" || a.State == "rejected" {
			return ErrConflict
		}
		if receipt.Resource != a.Intent.Resource {
			return ErrInvalid
		}
		if state == "verified" || state == "submitted" {
			if receipt.UID == "" || receipt.ResourceVersion == "" || a.Intent.ExpectedUID != "" && receipt.UID != a.Intent.ExpectedUID {
				return ErrConflict
			}
			if a.Intent.Verb != "delete" {
				var want map[string]any
				if json.Unmarshal(a.Intent.Body, &want) != nil {
					return ErrInvalid
				}
				delete(releaseMap(want["metadata"]), "resourceVersion")
				if !releaseApprovedActionFieldsMatch(a.Intent, want, receipt.Object) || !releaseIntentExpectedObserved(r.Plan, a.Intent, receipt.Object) {
					return ErrConflict
				}
			}
		}
		a.Revision++
		a.State = state
		receipt.ObservedAt = s.now().UnixMilli()
		a.Receipt = &receipt
		b, _ := json.Marshal(receipt)
		defer clear(b)
		cipher, e := s.seal(releaseActionReceiptPurpose, id, a.Revision, b)
		if e != nil {
			return e
		}
		e = requireOne(q.ExecContext(ctx, `UPDATE k8s_release_actions SET revision=?,state=?,receipt_cipher=?,updated_at=? WHERE id=? AND revision=?`, a.Revision, state, cipher, s.now().UnixMilli(), id, expected))
		out = a
		return e
	})
	return out, e
}
func (s *Service) ListReleaseActions(ctx context.Context, id string) ([]ReleaseAction, error) {
	q := configstate.Queryer(ctx, s.db)
	rows, e := q.QueryContext(ctx, `SELECT id FROM k8s_release_actions WHERE release_id=? ORDER BY created_at,id`, id)
	if e != nil {
		return nil, e
	}
	var ids []string
	for rows.Next() {
		var id string
		if e = rows.Scan(&id); e != nil {
			rows.Close()
			return nil, e
		}
		ids = append(ids, id)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return nil, e
	}
	out := []ReleaseAction{}
	for _, id := range ids {
		a, e := s.action(ctx, q, id)
		if e != nil {
			return nil, e
		}
		out = append(out, a)
	}
	return out, nil
}

// UpdateReleaseActionHealth changes observation evidence only. A known accepted
// write remains known even when rollout/cleanup later fails; pipeline readiness
// is independently decided from these encrypted health receipts.
func (s *Service) UpdateReleaseActionHealth(ctx context.Context, id, owner string, expected int64, receipt ReleaseActionReceipt) (ReleaseAction, error) {
	var out ReleaseAction
	e := s.write(ctx, func(ctx context.Context, q dbtx) error {
		a, e := s.action(ctx, q, id)
		if e != nil {
			return e
		}
		r, e := s.release(ctx, q, a.ReleaseID)
		if e != nil {
			return e
		}
		if a.Revision != expected || a.State != "verified" {
			return ErrConflict
		}
		if r.OwnerEpoch != owner || r.OwnerFenced {
			return ErrOwner
		}
		if receipt.Resource != a.Intent.Resource || receipt.UID == "" || a.Receipt == nil || receipt.UID != a.Receipt.UID || receipt.ResourceVersion == "" {
			return ErrConflict
		}
		meta := releaseMap(receipt.Object["metadata"])
		if releaseMap(meta["annotations"])[ReleaseActionAnnotation] != a.ID {
			return ErrConflict
		}
		var want map[string]any
		if json.Unmarshal(a.Intent.Body, &want) != nil {
			return ErrInvalid
		}
		delete(releaseMap(want["metadata"]), "resourceVersion")
		if !releaseApprovedActionFieldsMatch(a.Intent, want, receipt.Object) || !releaseIntentExpectedObserved(r.Plan, a.Intent, receipt.Object) {
			return ErrConflict
		}
		a.Revision++
		receipt.ObservedAt = s.now().UnixMilli()
		a.Receipt = &receipt
		b, _ := json.Marshal(receipt)
		defer clear(b)
		cipher, e := s.seal(releaseActionReceiptPurpose, id, a.Revision, b)
		if e != nil {
			return e
		}
		e = requireOne(q.ExecContext(ctx, `UPDATE k8s_release_actions SET revision=?,receipt_cipher=?,updated_at=? WHERE id=? AND revision=?`, a.Revision, cipher, s.now().UnixMilli(), id, expected))
		out = a
		return e
	})
	return out, e
}

// ValidateReleaseActionPermit checks the live owner and writer after the helper
// has queued its exact request, immediately before the one-use permit is sent.
func (s *Service) ValidateReleaseActionPermit(ctx context.Context, releaseID, actionID, owner string) error {
	return s.write(ctx, func(ctx context.Context, q dbtx) error {
		r, e := s.release(ctx, q, releaseID)
		if e != nil {
			return e
		}
		if r.OwnerEpoch != owner || r.OwnerFenced || r.State != "running" {
			return ErrOwner
		}
		a, e := s.action(ctx, q, actionID)
		if e != nil {
			return e
		}
		if a.ReleaseID != releaseID || a.OwnerEpoch != owner || a.State != "outcome_unknown" || a.Intent.ParentID != r.PlanID {
			return ErrOwner
		}
		if (r.Plan.Authorization.RequireApproval || r.Plan.ForceApproval) && r.ApprovedID != r.PlanID {
			return ErrDenied
		}
		if e = s.validateReleaseAuthorization(ctx, q, r.Plan.Authorization); e != nil {
			return e
		}
		return s.VerifyReleaseWriterLease(ctx, q, releaseID, owner)
	})
}
func releaseContainerNames(resource ReleaseResource) []string {
	spec := releaseMap(resource.Object["spec"])
	switch resource.ID.Kind {
	case "Deployment", "StatefulSet", "DaemonSet", "Job":
		spec = releaseMap(releaseMap(spec["template"])["spec"])
	case "CronJob":
		spec = releaseMap(releaseMap(releaseMap(releaseMap(spec["jobTemplate"])["spec"])["template"])["spec"])
	case "Pod":
	default:
		return nil
	}
	out := []string{}
	for _, key := range []string{"containers", "initContainers"} {
		for _, v := range releaseItems(spec[key]) {
			if name := releaseString(releaseMap(v)["name"]); name != "" {
				out = append(out, name)
			}
		}
	}
	sort.Strings(out)
	return out
}

func (r ReleaseRecord) Successful() bool {
	return (r.State == "ready" || r.State == "rolled_back") && r.Cleanup == CleanupVerified && r.OwnerFenced
}
