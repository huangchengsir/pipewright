package configstate

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strconv"
	"time"

	"github.com/google/uuid"
)

var (
	ErrMaintenanceForbidden = errors.New("configstate: maintenance forbidden")
	ErrEvidenceUnavailable  = errors.New("configstate: reader exit evidence unavailable")
)

func newEvidenceID() string { return uuid.NewString() }

type MaintenanceOptions struct {
	AuthorizeAdmin func(context.Context, DBTX, string) error
}
type MaintenanceService struct {
	db   *sql.DB
	opts MaintenanceOptions
}

func NewMaintenance(db *sql.DB, opts *MaintenanceOptions) *MaintenanceService {
	s := &MaintenanceService{db: db}
	if opts != nil {
		s.opts = *opts
	}
	return s
}

type MaintenanceItem struct {
	ActivityID      string `json:"activityId"`
	ProjectID       string `json:"projectId"`
	RunID           string `json:"runId,omitempty"`
	OwnerEpoch      string `json:"ownerEpoch"`
	Kind            string `json:"kind"`
	Phase           string `json:"phase"`
	ProjectRevision string `json:"projectRevision"`
	CreatedAt       int64  `json:"createdAt"`
	HeartbeatAt     int64  `json:"heartbeatAt"`
	Digest          string `json:"digest"`
	EvidenceID      string `json:"evidenceId,omitempty"`
	Recoverable     bool   `json:"recoverable"`
	Reason          string `json:"reason"`
}
type MaintenanceSnapshot struct {
	ExecutorEpoch int64             `json:"executorEpoch"`
	Items         []MaintenanceItem `json:"items"`
	Truncated     bool              `json:"truncated"`
}
type MaintenanceRecoverRequest struct {
	ActivityID     string `json:"activityId"`
	ExpectedDigest string `json:"expectedDigest"`
	EvidenceID     string `json:"evidenceId"`
}
type MaintenanceRecovery struct {
	ActivityID    string `json:"activityId"`
	State         string `json:"state"`
	ExecutorEpoch int64  `json:"executorEpoch"`
}

func (s *MaintenanceService) authorize(ctx context.Context, q DBTX, adminID string) error {
	if s.db == nil || adminID == "" || s.opts.AuthorizeAdmin == nil || s.opts.AuthorizeAdmin(ctx, q, adminID) != nil {
		return ErrMaintenanceForbidden
	}
	return nil
}

func maintenanceDigest(i MaintenanceItem, epoch int64) string {
	// Evidence IDs are an in-memory prerequisite, never part of persisted state.
	i.Digest = ""
	i.EvidenceID = ""
	i.Recoverable = false
	i.Reason = ""
	b, _ := json.Marshal(struct {
		Item  MaintenanceItem
		Epoch int64
	}{i, epoch})
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}
func maintenanceItem(ctx context.Context, q DBTX, id string) (MaintenanceItem, error) {
	var i MaintenanceItem
	var runID sql.NullString
	err := q.QueryRowContext(ctx, `SELECT activity_id,project_id,run_id,owner_epoch,kind,phase,created_at,heartbeat_at FROM project_activity WHERE activity_id=? AND released_at IS NULL`, id).Scan(&i.ActivityID, &i.ProjectID, &runID, &i.OwnerEpoch, &i.Kind, &i.Phase, &i.CreatedAt, &i.HeartbeatAt)
	if err != nil {
		return i, err
	}
	i.RunID = runID.String
	rev, err := ReadRevision(ctx, q, i.ProjectID)
	if err != nil {
		return i, err
	}
	i.ProjectRevision = "cfg-" + strconv.FormatInt(rev, 10)
	return i, nil
}

func (s *MaintenanceService) List(ctx context.Context, adminID string) (*MaintenanceSnapshot, error) {
	if s.db == nil {
		return nil, ErrMaintenanceForbidden
	}
	proofs := readerEvidenceSnapshot() // Never take the registry lock while holding SQL locks.
	out := &MaintenanceSnapshot{Items: []MaintenanceItem{}}
	err := WithWriteTxContext(ctx, s.db, func(txctx context.Context, q DBTX) error {
		if err := s.authorize(txctx, q, adminID); err != nil {
			return err
		}
		var err error
		out.ExecutorEpoch, err = ReadEpoch(txctx, q, EpochExecutor)
		if err != nil {
			return err
		}
		rows, err := q.QueryContext(txctx, `SELECT activity_id FROM project_activity WHERE released_at IS NULL ORDER BY created_at,activity_id LIMIT 201`)
		if err != nil {
			return err
		}
		ids := []string{}
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				return err
			}
			ids = append(ids, id)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		if len(ids) > 200 {
			out.Truncated = true
			ids = ids[:200]
		}
		for _, id := range ids {
			i, err := maintenanceItem(txctx, q, id)
			if err != nil {
				return err
			}
			i.Digest = maintenanceDigest(i, out.ExecutorEpoch)
			i.Reason = "evidence_unavailable"
			if p, ok := proofs[id]; ok && i.OwnerEpoch != "legacy-unreconciled" && p.owner == i.OwnerEpoch && !p.active && !p.retired && p.evidence != "" {
				i.EvidenceID = p.evidence
				i.Recoverable = true
				i.Reason = "local_readers_exited"
			}
			out.Items = append(out.Items, i)
		}
		return nil
	})
	return out, err
}

func (s *MaintenanceService) Recover(ctx context.Context, adminID string, in MaintenanceRecoverRequest) (*MaintenanceRecovery, error) {
	if s.db == nil {
		return nil, ErrMaintenanceForbidden
	}
	if in.ActivityID == "" || len(in.ExpectedDigest) != 64 || in.EvidenceID == "" {
		return nil, ErrEvidenceUnavailable
	}
	// This mutex is the local admission fence. Runtime restart invalidates proofs
	// before SQL admission, and cannot pass this point until the owner CAS commits.
	localReaders.Lock()
	defer localReaders.Unlock()
	p := localReaders.items[in.ActivityID]
	if p == nil || p.owner == "legacy-unreconciled" || p.active || p.retired || p.evidence != in.EvidenceID {
		return nil, ErrEvidenceUnavailable
	}
	var out *MaintenanceRecovery
	err := WithWriteTxContext(ctx, s.db, func(txctx context.Context, q DBTX) error {
		if err := LockEpochs(txctx, q, s.db, EpochPolicy, EpochSecrets, EpochExecutor); err != nil {
			return err
		}
		if err := s.authorize(txctx, q, adminID); err != nil {
			return err
		}
		// Find the immutable project identity, then lock its aggregate row.
		var pid string
		if err := q.QueryRowContext(txctx, `SELECT project_id FROM project_activity WHERE activity_id=? AND released_at IS NULL`, in.ActivityID).Scan(&pid); err != nil {
			return ErrConflict
		}
		if _, err := LockProject(txctx, q, s.db, pid); err != nil {
			return err
		}
		i, err := maintenanceItem(txctx, q, in.ActivityID)
		if err != nil {
			return ErrConflict
		}
		epoch, err := ReadEpoch(txctx, q, EpochExecutor)
		if err != nil {
			return err
		}
		if i.OwnerEpoch != p.owner || maintenanceDigest(i, epoch) != in.ExpectedDigest {
			return ErrConflict
		}
		newOwner := uuid.NewString()
		now := time.Now().UnixMilli()
		res, err := q.ExecContext(txctx, `UPDATE project_activity SET owner_epoch=?,phase='released',released_at=?,heartbeat_at=? WHERE activity_id=? AND owner_epoch=? AND released_at IS NULL`, newOwner, now, now, i.ActivityID, i.OwnerEpoch)
		if err != nil {
			return err
		}
		n, err := res.RowsAffected()
		if err != nil {
			return err
		}
		if n != 1 {
			return ErrConflict
		}
		// A worker may have exited after its terminal write failed. Proven local
		// reader exit permits closing only that exact run, without replaying it.
		if i.Kind == "run" && i.RunID != "" {
			if _, err := q.ExecContext(txctx, `UPDATE pipeline_runs SET status='failed',finished_at=? WHERE id=? AND project_id=? AND status IN ('queued','running','waiting_approval')`, time.Now().UTC().Format(time.RFC3339), i.RunID, i.ProjectID); err != nil {
				return err
			}
		}
		if err := BumpEpoch(txctx, q, EpochExecutor); err != nil {
			return err
		}
		detail, _ := json.Marshal(map[string]any{"activityId": i.ActivityID, "oldOwnerEpoch": i.OwnerEpoch, "newOwnerEpoch": newOwner, "beforeDigest": in.ExpectedDigest, "executorEpoch": epoch + 1, "reason": "local_readers_exited"})
		stamp := time.Now().UTC().Format(time.RFC3339Nano)
		if _, err := q.ExecContext(txctx, `INSERT INTO audit_log(id,timestamp,actor,action,target_type,target_id,detail_json,ip,created_at) VALUES(?,?,?,?,?,?,?,?,?)`, uuid.NewString(), stamp, "admin", "config_activity_recover", "project", i.ProjectID, string(detail), "", stamp); err != nil {
			return err
		}
		out = &MaintenanceRecovery{ActivityID: i.ActivityID, State: "released", ExecutorEpoch: epoch + 1}
		return nil
	})
	if err != nil {
		return nil, err
	}
	p.retired = true
	p.evidence = ""
	return out, nil
}
