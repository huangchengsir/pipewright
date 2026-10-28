package runretry

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/huangchengsir/pipewright/internal/configstate"
	"io"
	"path"
	"sort"
	"strings"
	"sync"
	"time"
)

type execution struct {
	svc       *Service
	runID     string
	l         lineage
	decisions map[string]Decision
	nodes     map[string]NodeRef
	mu        sync.RWMutex
}

func (e *execution) IsRecovery() bool { return e.runID != e.l.root }
func (e *execution) Result(ctx context.Context, n NodeRef) (Outcome, error) {
	if !e.valid(n) {
		return Outcome{}, ErrInvalid
	}
	records, err := e.svc.records(ctx, e.runID)
	if err != nil {
		return Outcome{}, err
	}
	return e.svc.outcome(ctx, e.l, n, records[n.Key])
}
func (e *execution) Previous(ctx context.Context, n NodeRef) (Outcome, string, error) {
	if !e.valid(n) || !e.IsRecovery() {
		return Outcome{}, "", ErrUnavailable
	}
	return e.previousBusinessOutcome(ctx, n)
}
func IsRecovery(e Execution) bool {
	v, ok := e.(interface{ IsRecovery() bool })
	return ok && v.IsRecovery()
}

// Rebuilding is derived from immutable parent evidence and the persisted child
// decisions, so a restart does not lose the user's explicit reconstruction.
func (e *execution) Rebuilding(ctx context.Context, stage string) (bool, error) {
	if !e.IsRecovery() {
		return false, nil
	}
	var parent string
	if err := e.svc.q(ctx).QueryRowContext(ctx, "SELECT parent_id FROM run_retry_attempts WHERE run_id=?", e.runID).Scan(&parent); err != nil {
		return false, err
	}
	records, err := e.svc.records(ctx, parent)
	if err != nil {
		return false, err
	}
	for _, n := range e.l.graph.Nodes {
		if n.ExecutionStageID != stage || n.JobID == "" || e.Decision(n).Action != "execute" {
			continue
		}
		r := records[n.Key]
		if r.status != "success" {
			continue
		}
		o, err := e.svc.outcome(ctx, e.l, n, r)
		if err == nil && o.WorkspaceIncomplete {
			return true, nil
		}
	}
	return false, nil
}

// IsRetry and HasManifest distinguish a missing historical capture from a known
// record whose ciphertext cannot be opened. Neither decrypts any secret.
func (s *Service) IsRetry(ctx context.Context, id string) (bool, error) {
	var n int
	err := s.q(ctx).QueryRowContext(ctx, "SELECT COUNT(*) FROM run_retry_attempts WHERE run_id=? AND execution_kind='recovery'", id).Scan(&n)
	return n == 1, err
}
func (s *Service) HasManifest(ctx context.Context, id string) (bool, error) {
	var n int
	err := s.q(ctx).QueryRowContext(ctx, "SELECT COUNT(*) FROM run_retry_attempts a JOIN run_retry_manifests m ON m.root_id=a.root_id WHERE a.run_id=?", id).Scan(&n)
	return n == 1, err
}
func (s *Service) Prepare(ctx context.Context, runID string) (context.Context, error) {
	l, e := s.load(ctx, runID)
	if errors.Is(e, ErrUnavailable) {
		var known int
		if err := s.q(ctx).QueryRowContext(ctx, "SELECT COUNT(*) FROM run_retry_attempts WHERE run_id=?", runID).Scan(&known); err != nil {
			return ctx, err
		}
		if known > 0 {
			return ctx, ErrUnavailable
		}
	}
	if errors.Is(e, ErrUnavailable) && s.provider != nil {
		snap, err := s.provider(ctx, runID)
		if err != nil {
			return ctx, err
		}
		if err = s.SaveManifest(ctx, runID, snap); err != nil {
			return ctx, err
		}
		l, e = s.load(ctx, runID)
	}
	if errors.Is(e, ErrUnavailable) {
		return ctx, nil
	}
	if e != nil {
		return ctx, e
	}
	if l.sourceConflict {
		return ctx, ErrConflict
	}
	rs, e := s.records(ctx, runID)
	if e != nil {
		return ctx, e
	}
	if runID != l.root {
		e = configstate.WithWriteTxContext(ctx, s.db, func(tx context.Context, q configstate.DBTX) error {
			var state, owner string
			if e := q.QueryRowContext(tx, "SELECT state,owner_epoch FROM run_retry_dispatch WHERE attempt_id=?", runID).Scan(&state, &owner); e != nil {
				return e
			}
			if owner != s.owner || state == "outcome_unknown" {
				return ErrUnknown
			}
			if state != "dispatching" && state != "accepted" {
				return ErrConflict
			}
			if _, e := q.ExecContext(tx, "UPDATE run_retry_dispatch SET state='running',updated_at=? WHERE attempt_id=? AND owner_epoch=?", time.Now().UnixMilli(), runID, s.owner); e != nil {
				return e
			}
			_, e := q.ExecContext(tx, "UPDATE run_retry_requests SET state='running' WHERE attempt_id=?", runID)
			return e
		})
		if e != nil {
			return ctx, e
		}
	}
	ex := &execution{svc: s, runID: runID, l: l, decisions: map[string]Decision{}, nodes: map[string]NodeRef{}}
	for _, n := range l.graph.Nodes {
		r := rs[n.Key]
		action := r.origin
		if action == "executed" {
			action = "execute"
			if n.ControlKey != "" {
				action = "control"
			}
		}
		ex.decisions[n.Key] = Decision{Action: action, Status: r.status, SourceAttemptID: r.source, SourceNodeKey: r.sourceKey}
		ex.nodes[n.Key] = n
	}
	if ex.IsRecovery() && s.preflight != nil {
		plan := &Plan{}
		for _, n := range l.graph.Nodes {
			plan.Nodes = append(plan.Nodes, PlannedNode{Node: n, Decision: ex.decisions[n.Key]})
		}
		reasons, err := s.preflight(ctx, l.snapshot, plan)
		if err != nil {
			return ctx, err
		}
		if len(reasons) > 0 {
			return ctx, ErrUnavailable
		}
	}
	return WithExecution(ctx, ex), nil
}
func (e *execution) Snapshot() Snapshot {
	e.mu.RLock()
	defer e.mu.RUnlock()
	b, _ := json.Marshal(e.l.snapshot)
	var out Snapshot
	_ = json.Unmarshal(b, &out)
	return out
}
func (e *execution) Node(stage, job string) (NodeRef, bool) {
	for _, n := range e.l.graph.Nodes {
		if n.ExecutionStageID == stage && n.ExecutionJobID == job && n.ControlKey == "" {
			return n, true
		}
	}
	return NodeRef{}, false
}

// ControlNode has its own namespace so a canvas job literally named "init"
// cannot be confused with the stage initialization control identity.
func (e *execution) ControlNode(stage, key string) (NodeRef, bool) {
	for _, n := range e.l.graph.Nodes {
		if n.ExecutionStageID == stage && n.ControlKey == key && key != "" {
			return n, true
		}
	}
	return NodeRef{}, false
}
func (e *execution) valid(n NodeRef) bool {
	expected, ok := e.nodes[n.Key]
	return ok && digest(expected) == digest(n)
}
func (e *execution) Decision(n NodeRef) Decision {
	if !e.valid(n) {
		return Decision{Action: "blocked", Reasons: []Reason{{Code: "node_invalid"}}}
	}
	e.mu.RLock()
	defer e.mu.RUnlock()
	d := e.decisions[n.Key]
	d.Reasons = append([]Reason(nil), d.Reasons...)
	return d
}
func (e *execution) Begin(ctx context.Context, n NodeRef) error {
	if !e.valid(n) {
		return ErrInvalid
	}
	d := e.Decision(n)
	if d.Action != "execute" && d.Action != "control" {
		return ErrBlocked
	}
	r, err := e.svc.q(ctx).ExecContext(ctx, "UPDATE run_retry_nodes SET status='running',owner_epoch=?,updated_at=? WHERE attempt_id=? AND node_key=? AND status='pending' AND origin='executed'", e.svc.owner, time.Now().UnixMilli(), e.runID, n.Key)
	if err != nil {
		return err
	}
	count, err := r.RowsAffected()
	if err != nil {
		return err
	}
	if count != 1 {
		return ErrConflict
	}
	return nil
}
func (s *Service) validateInputs(in Inputs) error {
	if len(in.Files) > 512 || len(in.Env) > 4096 || len(in.Images) > 512 {
		return ErrInvalid
	}
	var total int64
	for _, f := range in.Files {
		if len(f.Key) != 64 || f.Key != f.SHA256 || !lowerHex(f.Key) || f.Size < 0 || f.Size > s.maxFileBytes || !safePath(f.Path) {
			return ErrInvalid
		}
		total += f.Size
		if total > 8<<30 {
			return ErrInvalid
		}
	}
	for k, v := range in.Images {
		if k == "" || v.Digest == "" && (v.ImageID == "" || v.RuntimeID == "") {
			return ErrInvalid
		}
		for _, id := range []string{v.Digest, v.ImageID} {
			if id != "" && (!strings.HasPrefix(id, "sha256:") || len(id) != 71 || !lowerHex(id[7:])) {
				return ErrInvalid
			}
		}
	}
	return nil
}
func lowerHex(s string) bool {
	for _, c := range s {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}
func safePath(p string) bool {
	return p != "" && p != "." && path.Clean(p) == p && !path.IsAbs(p) && !strings.ContainsAny(p, "\\:\x00") && p != ".." && !strings.HasPrefix(p, "../")
}

type contextReader struct {
	ctx context.Context
	r   io.Reader
}

func (r contextReader) Read(p []byte) (int, error) {
	if e := r.ctx.Err(); e != nil {
		return 0, e
	}
	return r.r.Read(p)
}
func (s *Service) verifyFiles(ctx context.Context, files []FileRef) error {
	if len(files) > 0 && s.blobs == nil {
		return ErrUnavailable
	}
	for _, f := range files {
		r, err := s.blobs.Open(f.Key)
		if err != nil {
			return ErrUnavailable
		}
		h := sha256.New()
		n, err := io.Copy(h, io.LimitReader(contextReader{ctx, r}, f.Size+1))
		closeErr := r.Close()
		if err != nil || closeErr != nil || n != f.Size || hex.EncodeToString(h.Sum(nil)) != f.SHA256 {
			return ErrUnavailable
		}
	}
	return nil
}
func (e *execution) Inputs(ctx context.Context, n NodeRef) (Inputs, error) {
	if !e.valid(n) {
		return Inputs{}, ErrInvalid
	}
	out := Inputs{Env: map[string]string{}, Images: map[string]ImageRef{}}
	records, err := e.svc.records(ctx, e.runID)
	if err != nil {
		return out, err
	}
	fileSeen := map[string]string{}
	load := func(key string) (Outcome, error) {
		r := records[key]
		if r.status != "success" {
			return Outcome{}, ErrBlocked
		}
		ref := e.nodes[key]
		o, err := e.svc.outcome(ctx, e.l, ref, r)
		if err != nil || !o.Complete || (!o.Reusable && r.source != e.runID) || o.ExecutionMode != "real" || o.ExternalOutcome == "unknown" {
			return Outcome{}, ErrUnavailable
		}
		if o.ValidUntil != nil && !o.ValidUntil.After(time.Now()) {
			return Outcome{}, ErrUnavailable
		}
		return o, nil
	}
	// Env flows only across direct declared needs in declaration order.
	for _, edge := range canonicalEdges(e.l.graph.Edges, n.Key, "env") {
		o, err := load(edge.From)
		if err != nil {
			return out, err
		}
		for k, v := range o.Outputs.Env {
			out.Env[k] = v
		}
	}
	// File/image producers may be transitive (build -> test -> push/deploy).
	producer, index, prefer, explicit, selectorErr := e.selectedProducer(n)
	if selectorErr != nil {
		return out, selectorErr
	}
	if explicit {
		o, err := load(producer.Key)
		if err != nil {
			return out, err
		}
		matches := 0
		for _, file := range o.Outputs.Files {
			if declarationMatches(file.Metadata, index) && (prefer == "" || prefer == file.ArtifactType) {
				out.Files = append(out.Files, file)
				matches++
			}
		}
		for key, image := range o.Outputs.Images {
			if declarationMatches(image.Metadata, index) && (prefer == "" || prefer == "image") {
				out.Images[key] = image
				matches++
			}
		}
		if matches != 1 {
			return out, ErrUnavailable
		}
		if err = e.svc.validateInputs(out); err != nil {
			return out, err
		}
		if err = e.svc.verifyFiles(ctx, out.Files); err != nil {
			return out, err
		}
		return out, nil
	}
	for _, kind := range []string{"file", "image"} {
		visited := map[string]bool{}
		var visit func(string) error
		visit = func(key string) error {
			for _, edge := range canonicalEdges(e.l.graph.Edges, key, kind) {
				if visited[edge.From] {
					continue
				}
				visited[edge.From] = true
				// A failed control predecessor that produced no applicable data must
				// not leak into unrelated consumers. Required data still fails closed.
				if err := visit(edge.From); err != nil {
					return err
				}
				o, err := load(edge.From)
				if err != nil {
					return err
				}
				if kind == "file" {
					for _, f := range o.Outputs.Files {
						if old, ok := fileSeen[f.Path]; ok {
							if old != f.Key {
								return ErrUnavailable
							}
							continue
						}
						fileSeen[f.Path] = f.Key
						out.Files = append(out.Files, f)
					}
				} else {
					for k, v := range o.Outputs.Images {
						if old, ok := out.Images[k]; ok && digest(old) != digest(v) {
							return ErrUnavailable
						}
						out.Images[k] = v
					}
				}
			}
			return nil
		}
		if err := visit(n.Key); err != nil {
			return out, err
		}
	}
	if err = e.svc.validateInputs(out); err != nil {
		return out, err
	}
	if err = e.svc.verifyFiles(ctx, out.Files); err != nil {
		return out, err
	}
	return out, nil
}
func (e *execution) Finish(ctx context.Context, n NodeRef, o Outcome) error {
	if !e.valid(n) {
		return ErrInvalid
	}
	decision := e.Decision(n)
	if decision.Action != "execute" && decision.Action != "control" {
		return ErrBlocked
	}
	if o.Status != "success" && o.Status != "failed" && o.Status != "skipped" && o.Status != "outcome_unknown" {
		return ErrInvalid
	}
	if o.ExternalOutcome == "unknown" {
		o.Status = "outcome_unknown"
		o.Complete = false
		o.Reusable = false
	}
	if o.Status != "success" || o.ExecutionMode != "real" {
		o.Reusable = false
	}
	if o.DeploymentNotDispatched && !validDeploymentNoDispatch(n, o) {
		return ErrInvalid
	}
	for i := range o.Outputs.Files {
		o.Outputs.Files[i].ProducerAttemptID = e.runID
		o.Outputs.Files[i].ProducerNodeKey = n.Key
	}
	for k, v := range o.Outputs.Images {
		v.ProducerAttemptID = e.runID
		v.ProducerNodeKey = n.Key
		o.Outputs.Images[k] = v
	}
	var checkpointErr error
	if err := e.svc.validateInputs(o.Outputs); err != nil {
		checkpointErr = ErrCheckpoint
		o.Complete = false
		o.Reusable = false
		o.Outputs = Inputs{}
	}
	cipher, err := e.svc.seal("checkpoint", e.l.root, e.runID+":"+n.Key, o)
	if err != nil {
		checkpointErr = ErrCheckpoint
		o.Complete = false
		o.Reusable = false
		cipher = nil
	}
	err = configstate.WithWriteTxContext(ctx, e.svc.db, func(tx context.Context, q configstate.DBTX) error {
		if err := e.svc.lock(tx, q, e.l); err != nil {
			return err
		}
		if o.DeploymentNotDispatched {
			if err := e.svc.noOwnedNodeEffects(tx, e.runID, n.Key); err != nil {
				return err
			}
		}
		var oldStatus string
		var old []byte
		if err := q.QueryRowContext(tx, "SELECT status,ciphertext FROM run_retry_nodes WHERE attempt_id=? AND node_key=?", e.runID, n.Key).Scan(&oldStatus, &old); err != nil {
			return err
		}
		if oldStatus != "pending" && oldStatus != "running" {
			var previous Outcome
			if e.svc.open(old, "checkpoint", e.l.root, e.runID+":"+n.Key, &previous) == nil && digest(previous) == digest(o) {
				return nil
			}
			return ErrConflict
		}
		var used int64
		if err := q.QueryRowContext(tx, "SELECT checkpoint_bytes FROM run_retry_lineages WHERE root_id=?", e.l.root).Scan(&used); err != nil {
			return err
		}
		if used-int64(len(old))+int64(len(cipher)) > 31<<20 {
			checkpointErr = ErrCheckpoint
			o.ReasonCode = "checkpoint_budget_exceeded"
			o.Complete = false
			o.Reusable = false
			cipher = nil
		}
		reason := o.ReasonCode
		if checkpointErr != nil && reason == "" {
			reason = "checkpoint_unavailable"
		}
		drained := n.ControlKey == "cleanup" && o.Status == "success" && o.ExternalOutcome == "verified"
		if _, err := q.ExecContext(tx, "UPDATE run_retry_lineages SET checkpoint_bytes=? WHERE root_id=?", used-int64(len(old))+int64(len(cipher)), e.l.root); err != nil {
			return err
		}
		var until any
		if o.ValidUntil != nil {
			until = o.ValidUntil.UnixMilli()
		}
		if _, err := q.ExecContext(tx, `UPDATE run_retry_nodes SET status=?,complete=?,reusable=?,valid_until=?,ciphertext=?,reason_code=?,resources_drained=?,updated_at=? WHERE attempt_id=? AND node_key=? AND origin='executed'`, o.Status, boolInt(o.Complete), boolInt(o.Reusable), until, cipher, reason, boolInt(drained), time.Now().UnixMilli(), e.runID, n.Key); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		if errors.Is(err, ErrConflict) || errors.Is(err, ErrInvalid) || errors.Is(err, ErrBlocked) || errors.Is(err, ErrUnknown) {
			return err
		}
		return fmt.Errorf("%w: checkpoint write", ErrCheckpoint)
	}
	e.mu.Lock()
	d := e.decisions[n.Key]
	d.Status = o.Status
	e.decisions[n.Key] = d
	e.mu.Unlock()
	return checkpointErr
}
func (e *execution) Control(ctx context.Context, n NodeRef, o Outcome) error {
	if n.ControlKey == "" {
		return ErrInvalid
	}
	return e.Finish(ctx, n, o)
}
func (e *execution) BindCommit(ctx context.Context, sha string) error {
	if !fullSHA(sha) {
		return ErrInvalid
	}
	conflict := false
	err := configstate.WithWriteTxContext(ctx, e.svc.db, func(tx context.Context, q configstate.DBTX) error {
		if err := e.svc.lock(tx, q, e.l); err != nil {
			return err
		}
		var previous string
		err := q.QueryRowContext(tx, "SELECT commit_sha,conflicted FROM run_retry_sources WHERE root_id=?", e.l.root).Scan(&previous, &conflict)
		if err == nil {
			if previous != sha || conflict {
				conflict = true
				_, err = q.ExecContext(tx, "UPDATE run_retry_sources SET conflicted=1 WHERE root_id=?", e.l.root)
				return err
			}
			return nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		records, err := e.svc.records(tx, e.runID)
		if err != nil {
			return err
		}
		for _, n := range e.l.graph.Nodes {
			r := records[n.Key]
			if n.JobID != "" && r.origin == "reuse" && r.source != e.runID {
				return ErrBlocked
			}
		}
		_, err = q.ExecContext(tx, "INSERT INTO run_retry_sources(root_id,commit_sha,first_attempt_id,bound_at) VALUES(?,?,?,?)", e.l.root, sha, e.runID, time.Now().UnixMilli())
		return err
	})
	if err == nil && conflict {
		return ErrConflict
	}
	if err == nil {
		e.mu.Lock()
		e.l.snapshot.CommitSHA = sha
		e.mu.Unlock()
	}
	return err
}

// BindStep connects a stable node to a real current-attempt step. No name matching.
func (s *Service) BindStep(ctx context.Context, attempt, nodeKey, stepID string, ordinal int) error {
	if stepID == "" || ordinal < 0 {
		return ErrInvalid
	}
	var exists int
	if err := s.q(ctx).QueryRowContext(ctx, "SELECT COUNT(*) FROM run_steps WHERE id=? AND run_id=? AND ordinal=?", stepID, attempt, ordinal).Scan(&exists); err != nil {
		return err
	}
	if exists != 1 {
		return ErrInvalid
	}
	r, e := s.q(ctx).ExecContext(ctx, "UPDATE run_retry_nodes SET step_id=?,step_ordinal=? WHERE attempt_id=? AND node_key=? AND (step_id='' OR (step_id=? AND step_ordinal=?))", stepID, ordinal, attempt, nodeKey, stepID, ordinal)
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
	return nil
}

// SortedKeys is useful to adapters restoring deterministic image maps.
func SortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
