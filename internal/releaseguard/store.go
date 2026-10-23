package releaseguard

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"

	"github.com/google/uuid"
	"github.com/huangchengsir/pipewright/internal/target"
)

const (
	maxEvidenceSize  = 64 << 10
	maxEvidenceBytes = 2 << 20
	// Ordinary evidence leaves room for one complete, real failure sample.
	regularEvidenceBytes = maxEvidenceBytes - maxEvidenceSize
	maxLogEvidenceBytes  = 1536 << 10
)

func digestJSON(v any) string {
	b, _ := json.Marshal(v)
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

func (s *Service) projectExists(ctx context.Context, id string) error {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM projects WHERE id=?`, id).Scan(&n)
	if err != nil {
		return err
	}
	if n != 1 {
		return ErrNotFound
	}
	return nil
}

func (s *Service) plan(ctx context.Context, project, id string) (*Plan, error) {
	var body string
	err := s.db.QueryRowContext(ctx, `SELECT plan_json FROM release_guard_plans WHERE id=? AND project_id=?`, id, project).Scan(&body)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	p := &Plan{}
	if json.Unmarshal([]byte(body), p) != nil {
		return nil, ErrUnavailable
	}
	return p, nil
}

func (s *Service) load(ctx context.Context, project, id string) (*document, error) {
	var body string
	var revision int64
	err := s.db.QueryRowContext(ctx, `SELECT record_json,revision FROM release_guards WHERE id=? AND project_id=?`, id, project).Scan(&body, &revision)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	d := &document{}
	if json.Unmarshal([]byte(body), d) != nil {
		return nil, ErrUnavailable
	}
	d.Private.Revision = revision
	return d, nil
}

// save atomically persists snapshot, evidence and replay cursor. CAS fences
// late workers and concurrent cancellation; raw private state never leaves DB.
func (s *Service) save(ctx context.Context, d *document, kind string, e *Evidence) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	d.Report.EventsCursor++
	now := s.now().UTC()
	if e != nil {
		if e.ID == "" {
			e.ID = uuid.NewString()
		}
		e.Sequence = d.Report.EventsCursor
		b, _ := json.Marshal(e)
		if len(b) > maxEvidenceSize {
			return ErrInvalid
		}
		nextBytes := d.Private.EvidenceBytes + len(b)
		failure := e.Kind == "runtime" && d.Report.VerificationResult != nil && *d.Report.VerificationResult == "failed"
		if nextBytes > maxEvidenceBytes || (nextBytes > regularEvidenceBytes && !failure) {
			return ErrCapacity
		}
		d.Private.EvidenceBytes = nextBytes
		d.Report.EvidenceSetHash = digestJSON([]string{d.Report.EvidenceSetHash, string(b)})
		if d.Report.Investigation.Status == "completed" {
			d.Report.Investigation.Status = "stale"
			d.Report.Investigation.Limitations = appendUnique(d.Report.Investigation.Limitations, "investigation_input_changed")
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO release_guard_evidence(guard_id,sequence,evidence_json) VALUES(?,?,?)`, d.Report.ID, e.Sequence, string(b))
		if err != nil {
			return err
		}
	}
	event := TimelineItem{ID: uuid.NewString(), At: now, Kind: kind, Summary: kind, EvidenceIDs: []string{}}
	if e != nil {
		event.TargetID = e.TargetID
		event.EvidenceIDs = []string{e.ID}
	}
	b, _ := json.Marshal(event)
	_, err = tx.ExecContext(ctx, `INSERT INTO release_guard_events(guard_id,sequence,event_json) VALUES(?,?,?)`, d.Report.ID, d.Report.EventsCursor, string(b))
	if err != nil {
		return err
	}
	body, _ := json.Marshal(d)
	res, err := tx.ExecContext(ctx, `UPDATE release_guards SET status=?,attempt_id=?,record_json=?,revision=revision+1 WHERE id=? AND project_id=? AND revision=?`, d.Report.ExecutionStatus, d.Report.AttemptID, string(body), d.Report.ID, d.Report.ProjectID, d.Private.Revision)
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
	if err = tx.Commit(); err != nil {
		return err
	}
	d.Private.Revision++
	return nil
}

type EvidencePage struct {
	Items      []Evidence `json:"items"`
	NextCursor int64      `json:"nextCursor"`
	HasMore    bool       `json:"hasMore"`
}

func (s *Service) Evidence(ctx context.Context, project, id string, after int64, limit int) (EvidencePage, error) {
	page := EvidencePage{Items: []Evidence{}, NextCursor: after}
	if after < 0 || limit < 0 || limit > 100 {
		return page, ErrInvalid
	}
	if limit == 0 {
		limit = 50
	}
	r, err := s.Snapshot(ctx, project, id)
	if err != nil {
		return page, err
	}
	if r.EvidenceExpired {
		return page, ErrUnavailable
	}
	rows, err := s.db.QueryContext(ctx, `SELECT evidence_json FROM release_guard_evidence WHERE guard_id=? AND sequence>? ORDER BY sequence LIMIT ?`, id, after, limit+1)
	if err != nil {
		return page, err
	}
	defer rows.Close()
	for rows.Next() {
		var b string
		var e Evidence
		if err = rows.Scan(&b); err != nil {
			return page, err
		}
		if json.Unmarshal([]byte(b), &e) != nil {
			return page, ErrUnavailable
		}
		if len(page.Items) == limit {
			page.HasMore = true
			break
		}
		page.Items = append(page.Items, e)
		page.NextCursor = e.Sequence
	}
	return page, rows.Err()
}

type EventPage struct {
	Items      []TimelineItem `json:"items"`
	NextCursor int64          `json:"nextCursor"`
	Reset      bool           `json:"reset"`
}

func (s *Service) Events(ctx context.Context, project, id string, after int64) (EventPage, error) {
	p := EventPage{Items: []TimelineItem{}, NextCursor: after}
	if after < 0 {
		return p, ErrInvalid
	}
	r, e := s.Snapshot(ctx, project, id)
	if e != nil {
		return p, e
	}
	if r.EvidenceExpired {
		p.Reset = true
		p.NextCursor = r.EventsCursor
		return p, nil
	}
	rows, e := s.db.QueryContext(ctx, `SELECT sequence,event_json FROM release_guard_events WHERE guard_id=? AND sequence>? ORDER BY sequence LIMIT 100`, id, after)
	if e != nil {
		return p, e
	}
	defer rows.Close()
	for rows.Next() {
		var b string
		var item TimelineItem
		var n int64
		if e = rows.Scan(&n, &b); e != nil {
			return p, e
		}
		if json.Unmarshal([]byte(b), &item) != nil {
			return p, ErrUnavailable
		}
		p.Items = append(p.Items, item)
		p.NextCursor = n
	}
	return p, rows.Err()
}

func (s *Service) recover(ctx context.Context) error {
	rows, err := s.db.QueryContext(ctx, `SELECT project_id,id FROM release_guards WHERE status NOT IN ('completed','stopped','interrupted','blocked','tool_error','expired')`)
	if err != nil {
		return err
	}
	type key struct{ project, id string }
	keys := []key{}
	for rows.Next() {
		var k key
		if err = rows.Scan(&k.project, &k.id); err != nil {
			rows.Close()
			return err
		}
		keys = append(keys, k)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, k := range keys {
		d, e := s.load(ctx, k.project, k.id)
		if e != nil {
			return e
		}
		d.Report.ExecutionStatus = "interrupted"
		d.Report.Reason = "service_restarted"
		now := s.now().UTC()
		d.Report.FinishedAt = &now
		d.Private.Bindings = map[string]target.ConnectionSnapshot{}
		aggregate(&d.Report, true)
		if e = s.save(ctx, d, "interrupted", nil); e != nil {
			return e
		}
	}
	_, err = s.db.ExecContext(ctx, `UPDATE release_guard_analyses SET status='unknown' WHERE status='sending'`)
	return err
}

func (s *Service) Purge(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now().UnixMilli()
	rows, scanErr := s.db.QueryContext(ctx, `SELECT project_id,id FROM release_guards WHERE evidence_expires_at<=? AND status IN ('completed','stopped','interrupted','blocked','tool_error','expired')`, now)
	if scanErr != nil {
		return scanErr
	}
	type expiredKey struct{ project, id string }
	keys := []expiredKey{}
	for rows.Next() {
		var k expiredKey
		if scanErr = rows.Scan(&k.project, &k.id); scanErr != nil {
			rows.Close()
			return scanErr
		}
		keys = append(keys, k)
	}
	scanErr = rows.Err()
	rows.Close()
	if scanErr != nil {
		return scanErr
	}
	for _, k := range keys {
		d, e := s.load(ctx, k.project, k.id)
		if e != nil {
			return e
		}
		if !d.Report.EvidenceExpired {
			expireReport(&d.Report)
			d.Private = privateState{Revision: d.Private.Revision}
			if e = s.save(ctx, d, "evidence_expired", nil); e != nil {
				return e
			}
		}
	}
	// Active observations are never purged. Project deletion is handled by the
	// explicit coordinator; this also cleans records left by direct DB deletion.
	_, err := s.db.ExecContext(ctx, `DELETE FROM release_guard_evidence WHERE guard_id IN (SELECT id FROM release_guards WHERE evidence_expires_at<=? AND status IN ('completed','stopped','interrupted','blocked','tool_error','expired'))`, now)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `DELETE FROM release_guard_events WHERE guard_id IN (SELECT id FROM release_guards WHERE evidence_expires_at<=? AND status IN ('completed','stopped','interrupted','blocked','tool_error','expired'))`, now)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `DELETE FROM release_guard_analyses WHERE guard_id IN (SELECT id FROM release_guards WHERE evidence_expires_at<=?)`, now)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `DELETE FROM release_guards WHERE report_expires_at<=? AND status IN ('completed','stopped','interrupted','blocked','tool_error','expired')`, now)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `DELETE FROM release_guard_plans WHERE expires_at<=?`, now)
	return err
}

func (s *Service) Snapshot(ctx context.Context, project, id string) (*Report, error) {
	if err := s.projectExists(ctx, project); err != nil {
		return nil, err
	}
	d, err := s.load(ctx, project, id)
	if err != nil {
		return nil, err
	}
	d.Report.EvidenceExpired = !s.now().Before(d.Report.EvidenceExpiresAt)
	if d.Report.EvidenceExpired {
		expireReport(&d.Report)
	}
	return &d.Report, nil
}

func newEvidenceID() string { return uuid.NewString() }

func expireReport(r *Report) {
	r.EvidenceExpired = true
	r.EvidenceSetHash = ""
	r.Timeline = []TimelineItem{}
	r.Investigation = Investigation{BaselineStatus: "expired", Candidates: []Candidate{}, SourceComparisons: []SourceComparison{}, Baselines: []TargetBaseline{}, Limitations: []string{"evidence_expired_not_replayable"}}
	for i := range r.Targets {
		r.Targets[i].Actual = nil
	}
}

func (s *Service) planRevisionCurrent(ctx context.Context, p Plan) bool {
	var revision int64
	err := s.db.QueryRowContext(ctx, `SELECT revision FROM project_config_versions WHERE project_id=? AND deleted_at IS NULL`, p.ProjectID).Scan(&revision)
	return err == nil && revision == p.ConfigRevision
}
