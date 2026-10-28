package runretry

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"github.com/google/uuid"
	"github.com/huangchengsir/pipewright/internal/configstate"
	"github.com/huangchengsir/pipewright/internal/pipeline"
	"github.com/huangchengsir/pipewright/internal/store"
	"sort"
	"strings"
	"time"
)

func New(db *sql.DB, sealer Sealer, opts ...Option) *Service {
	s := &Service{db: db, sealer: sealer, owner: uuid.NewString(), maxPayload: 2 << 20, maxFileBytes: 2 << 30}
	for _, o := range opts {
		o(s)
	}
	return s
}

type envelope struct {
	Version             int `json:"version"`
	Purpose, Root, Node string
	Data                json.RawMessage
}

func (s *Service) seal(purpose, root, node string, v any) ([]byte, error) {
	if s.sealer == nil {
		return nil, ErrUnavailable
	}
	b, e := json.Marshal(v)
	if e != nil || len(b) > s.maxPayload {
		return nil, ErrInvalid
	}
	env, e := json.Marshal(envelope{SchemaVersion, purpose, root, node, b})
	clear(b)
	if e != nil {
		return nil, ErrInvalid
	}
	defer clear(env)
	cipher, e := s.sealer.SealSecret(env)
	if e != nil {
		return nil, ErrUnavailable
	}
	return cipher, nil
}
func (s *Service) open(cipher []byte, purpose, root, node string, v any) error {
	if s.sealer == nil || len(cipher) > s.maxPayload+4096 {
		return ErrUnavailable
	}
	b, e := s.sealer.OpenSecret(cipher)
	if e != nil {
		return ErrUnavailable
	}
	defer clear(b)
	var env envelope
	if json.Unmarshal(b, &env) != nil || env.Version != SchemaVersion || env.Purpose != purpose || env.Root != root || env.Node != node {
		return ErrUnavailable
	}
	if json.Unmarshal(env.Data, v) != nil {
		return ErrUnavailable
	}
	return nil
}
func (s *Service) q(ctx context.Context) configstate.DBTX { return configstate.Queryer(ctx, s.db) }

type lineage struct {
	root, project, head, active, fingerprint string
	revision                                 int64
	snapshot                                 Snapshot
	graph                                    Graph
	sourceConflict                           bool
}

func (s *Service) load(ctx context.Context, runID string) (lineage, error) {
	l := lineage{}
	q := s.q(ctx)
	e := q.QueryRowContext(ctx, "SELECT root_id FROM run_retry_attempts WHERE run_id=?", runID).Scan(&l.root)
	if errors.Is(e, sql.ErrNoRows) {
		return l, ErrUnavailable
	}
	if e != nil {
		return l, e
	}
	var active sql.NullString
	var cipher []byte
	e = q.QueryRowContext(ctx, `SELECT l.project_id,l.head_attempt_id,l.revision,l.active_attempt_id,m.fingerprint,m.ciphertext FROM run_retry_lineages l JOIN run_retry_manifests m ON m.root_id=l.root_id WHERE l.root_id=?`, l.root).Scan(&l.project, &l.head, &l.revision, &active, &l.fingerprint, &cipher)
	if e != nil {
		return l, e
	}
	l.active = active.String
	if e = s.open(cipher, "manifest", l.root, "", &l.snapshot); e != nil {
		return l, e
	}
	if e = q.QueryRowContext(ctx, "SELECT commit_sha,conflicted FROM run_retry_sources WHERE root_id=?", l.root).Scan(&l.snapshot.CommitSHA, &l.sourceConflict); e != nil && !errors.Is(e, sql.ErrNoRows) {
		return l, e
	}
	if l.snapshot.Compiled == nil || l.snapshot.Compiled.Version != SchemaVersion {
		return l, ErrUnavailable
	}
	stages, err := l.snapshot.ExecutionStages()
	if err != nil {
		return l, err
	}
	l.graph = Graph{Version: l.snapshot.Compiled.Version, Nodes: l.snapshot.Compiled.Nodes, Edges: l.snapshot.Compiled.Edges, Stages: stages}
	_, e = topological(l.graph)
	return l, e
}
func (s *Service) lock(ctx context.Context, q configstate.DBTX, l lineage) error {
	if e := configstate.LockEpochs(ctx, q, s.db, configstate.EpochPolicy, configstate.EpochSecrets, configstate.EpochExecutor); e != nil {
		return e
	}
	if _, e := configstate.LockProject(ctx, q, s.db, l.project); e != nil {
		return e
	}
	stmt := "SELECT revision FROM run_retry_lineages WHERE root_id=?"
	if store.DialectOf(s.db) == store.MySQL {
		stmt += " FOR UPDATE"
	}
	var rev int64
	return q.QueryRowContext(ctx, stmt, l.root).Scan(&rev)
}
func (s *Service) SaveManifest(ctx context.Context, runID string, snap Snapshot) error {
	if runID == "" || snap.ProjectID == "" {
		return ErrInvalid
	}
	if snap.SchemaVersion == 0 {
		snap.SchemaVersion = SchemaVersion
	}
	if snap.SchemaVersion != SchemaVersion {
		return ErrInvalid
	}
	if snap.CommitSHA != "" && !fullSHA(snap.CommitSHA) {
		return ErrInvalid
	}
	g, e := Compile(snap.Spec)
	if e != nil {
		return e
	}
	// Normalize the internal graph before idempotent capture comparison. The
	// ciphertext retains exact expanded configuration, identities and barriers;
	// future compiler changes never reinterpret this attempt.
	parents := map[string]string{}
	for _, stage := range g.Stages {
		if stage.MatrixParentID != "" {
			parents[stage.ID] = stage.MatrixParentID
		}
	}
	compiled := &FrozenGraph{Version: g.Version, Nodes: g.Nodes, Edges: g.Edges, Stages: g.Stages, Dependencies: pipeline.StageDependencies(g.Stages), MatrixParents: parents}
	if snap.Compiled != nil && digest(snap.Compiled) != digest(compiled) {
		return ErrInvalid
	}
	snap.Compiled = compiled
	cipher, e := s.seal("manifest", runID, "", snap)
	if e != nil {
		return e
	}
	return configstate.WithWriteTxContext(ctx, s.db, func(tx context.Context, q configstate.DBTX) error {
		if e := configstate.LockEpochs(tx, q, s.db, configstate.EpochPolicy, configstate.EpochSecrets, configstate.EpochExecutor); e != nil {
			return e
		}
		if _, e := configstate.LockProject(tx, q, s.db, snap.ProjectID); e != nil {
			return e
		}
		var project, created string
		if e := q.QueryRowContext(tx, "SELECT project_id,created_at FROM pipeline_runs WHERE id=?", runID).Scan(&project, &created); e != nil {
			return ErrNotFound
		}
		if project != snap.ProjectID {
			return ErrConflict
		}
		var old []byte
		e := q.QueryRowContext(tx, "SELECT ciphertext FROM run_retry_manifests WHERE root_id=?", runID).Scan(&old)
		if e == nil {
			var previous Snapshot
			if s.open(old, "manifest", runID, "", &previous) != nil {
				return ErrUnavailable
			}
			if digest(previous) != digest(snap) {
				return ErrConflict
			}
			return nil
		}
		if !errors.Is(e, sql.ErrNoRows) {
			return e
		}
		now := time.Now().UnixMilli()
		if _, e = q.ExecContext(tx, "INSERT INTO run_retry_manifests(root_id,project_id,schema_version,fingerprint,ciphertext,created_at) VALUES(?,?,?,?,?,?)", runID, snap.ProjectID, SchemaVersion, uuid.NewString(), cipher, now); e != nil {
			return e
		}
		if _, e = q.ExecContext(tx, "INSERT INTO run_retry_lineages(root_id,project_id,head_attempt_id,revision,updated_at) VALUES(?,?,?,1,?)", runID, snap.ProjectID, runID, now); e != nil {
			return e
		}
		createdAt, _ := time.Parse(time.RFC3339Nano, created)
		createdMS := int64(0)
		if !createdAt.IsZero() {
			createdMS = createdAt.UnixMilli()
		}
		if _, e = q.ExecContext(tx, "INSERT INTO run_retry_attempts(run_id,root_id,parent_id,revision,execution_kind,status,created_at,initiator) VALUES(?,?,?,1,'original','running',?,?)", runID, runID, "", createdMS, snap.Trigger.Actor); e != nil {
			return e
		}
		for _, n := range g.Nodes {
			if _, e = q.ExecContext(tx, `INSERT INTO run_retry_nodes(attempt_id,node_key,status,origin,source_attempt_id,source_node_key,updated_at) VALUES(?,?,'pending','executed',?,?,?)`, runID, n.Key, runID, n.Key, now); e != nil {
				return e
			}
		}
		if snap.CommitSHA != "" {
			_, e = q.ExecContext(tx, "INSERT INTO run_retry_sources(root_id,commit_sha,first_attempt_id,bound_at) VALUES(?,?,?,?)", runID, snap.CommitSHA, runID, now)
		}
		return e
	})
}
func fullSHA(s string) bool {
	if len(s) != 40 {
		return false
	}
	for _, c := range s {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}
func terminal(status string) bool {
	return status == "success" || status == "failed" || status == "partial_failed" || status == "rolled_back"
}
func (s *Service) quiescent(ctx context.Context, q configstate.DBTX, runID string) error {
	var status string
	if e := q.QueryRowContext(ctx, "SELECT status FROM pipeline_runs WHERE id=?", runID).Scan(&status); e != nil {
		return ErrUnavailable
	}
	if !terminal(status) {
		return ErrBlocked
	}
	var count int
	if e := q.QueryRowContext(ctx, "SELECT COUNT(*) FROM project_activity WHERE run_id=? AND released_at IS NULL", runID).Scan(&count); e != nil {
		return e
	}
	if count != 0 {
		return ErrBlocked
	}
	return nil
}

type nodeRecord struct {
	status, origin, source, sourceKey, stepID, owner, reason string
	ordinal                                                  int
	complete, reusable, drained                              bool
	until                                                    sql.NullInt64
	cipher                                                   []byte
}

func (s *Service) records(ctx context.Context, attempt string) (map[string]nodeRecord, error) {
	rows, e := s.q(ctx).QueryContext(ctx, "SELECT node_key,status,origin,source_attempt_id,source_node_key,complete,reusable,valid_until,ciphertext,step_id,step_ordinal,owner_epoch,reason_code,resources_drained FROM run_retry_nodes WHERE attempt_id=?", attempt)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := map[string]nodeRecord{}
	for rows.Next() {
		var k string
		var r nodeRecord
		var complete, reusable int
		if e = rows.Scan(&k, &r.status, &r.origin, &r.source, &r.sourceKey, &complete, &reusable, &r.until, &r.cipher, &r.stepID, &r.ordinal, &r.owner, &r.reason, &r.drained); e != nil {
			return nil, e
		}
		r.complete = complete == 1
		r.reusable = reusable == 1
		out[k] = r
	}
	return out, rows.Err()
}
func (s *Service) outcome(ctx context.Context, l lineage, n NodeRef, r nodeRecord) (Outcome, error) {
	if r.source == "" || n.Key == "" || r.sourceKey != n.Key {
		return Outcome{}, ErrUnavailable
	}
	if r.source != l.head || len(r.cipher) == 0 {
		e := s.q(ctx).QueryRowContext(ctx, `SELECT status,complete,reusable,valid_until,ciphertext FROM run_retry_nodes WHERE attempt_id=? AND node_key=?`, r.source, r.sourceKey).Scan(&r.status, &r.complete, &r.reusable, &r.until, &r.cipher)
		if e != nil {
			return Outcome{}, ErrUnavailable
		}
	}
	var o Outcome
	if e := s.open(r.cipher, "checkpoint", l.root, r.source+":"+r.sourceKey, &o); e != nil {
		return o, e
	}
	if o.Status != r.status || o.Complete != r.complete || o.Reusable != r.reusable {
		return o, ErrUnavailable
	}
	return o, nil
}

// Source projection uses the expected frozen identity, not a mutable sibling
// pointer. Ciphertext consumers enforce the same identity before decrypting.
func (s *Service) sourceStep(ctx context.Context, l lineage, n NodeRef, r nodeRecord) (string, int, error) {
	if r.source == "" || n.Key == "" || r.sourceKey != n.Key {
		return "", -1, ErrUnavailable
	}
	var id string
	var ordinal int
	err := s.q(ctx).QueryRowContext(ctx, `SELECT n.step_id,n.step_ordinal FROM run_retry_nodes n JOIN run_retry_attempts a ON a.run_id=n.attempt_id WHERE a.root_id=? AND n.attempt_id=? AND n.node_key=?`, l.root, r.source, n.Key).Scan(&id, &ordinal)
	if errors.Is(err, sql.ErrNoRows) {
		err = ErrUnavailable
	}
	return id, ordinal, err
}
func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
func cleanTargets(ids []string) ([]string, error) {
	if len(ids) == 0 || len(ids) > 8192 {
		return nil, ErrInvalid
	}
	out := append([]string(nil), ids...)
	sort.Strings(out)
	for i, k := range out {
		if len(k) != 64 || i > 0 && out[i-1] == k {
			return nil, ErrInvalid
		}
	}
	return out, nil
}
func validIdentity(s string) bool {
	if len(s) < 1 || len(s) > 128 {
		return false
	}
	for _, c := range s {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || strings.ContainsRune("-_.:", c)) {
			return false
		}
	}
	return true
}
