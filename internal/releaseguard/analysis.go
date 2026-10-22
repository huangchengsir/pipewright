package releaseguard

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"time"

	"github.com/google/uuid"
)

// ExplanationModel has no tool, exec, deployment or configuration interface.
type ExplanationModel interface {
	Info(context.Context) (ModelInfo, error)
	Explain(context.Context, ModelInfo, string) (string, error)
}

var ErrNotDispatched = errors.New("releaseguard: model request not dispatched")

type ModelInfo struct {
	Name        string `json:"name"`
	Model       string `json:"model"`
	Destination string `json:"destination"`
	ConfigHash  string `json:"configHash"`
}
type AnalysisInput struct {
	IncludeRuntime bool `json:"includeRuntime"`
	IncludeLogs    bool `json:"includeLogs"`
}
type SendAnalysisInput struct {
	PayloadHash        string `json:"payloadHash"`
	ProviderConfigHash string `json:"providerConfigHash"`
	Confirmed          bool   `json:"confirmed"`
}
type Explanation struct {
	Summary    string      `json:"summary"`
	Candidates []Candidate `json:"candidates"`
}
type Analysis struct {
	ID              string        `json:"id"`
	GuardID         string        `json:"guardId"`
	Status          string        `json:"status"`
	Provider        ModelInfo     `json:"provider"`
	Input           AnalysisInput `json:"input"`
	Payload         string        `json:"payload"`
	PayloadHash     string        `json:"payloadHash"`
	PlanHash        string        `json:"planHash"`
	EvidenceSetHash string        `json:"evidenceSetHash"`
	EvidenceIDs     []string      `json:"evidenceIds"`
	CreatedAt       time.Time     `json:"createdAt"`
	ExpiresAt       time.Time     `json:"expiresAt"`
	Revoked         bool          `json:"revoked"`
	Stale           bool          `json:"stale"`
	Reason          string        `json:"reason"`
	Limitations     []string      `json:"limitations"`
	Result          *Explanation  `json:"result,omitempty"`
}

func (s *Service) loadAnalysis(ctx context.Context, guard, id string) (*Analysis, error) {
	var body, status string
	err := s.db.QueryRowContext(ctx, `SELECT analysis_json,status FROM release_guard_analyses WHERE id=? AND guard_id=?`, id, guard).Scan(&body, &status)
	if err != nil {
		return nil, ErrNotFound
	}
	var a Analysis
	if json.Unmarshal([]byte(body), &a) != nil {
		return nil, ErrUnavailable
	}
	a.Status = status
	return &a, nil
}

func (s *Service) saveAnalysis(ctx context.Context, a *Analysis) error {
	body, _ := json.Marshal(a)
	_, err := s.db.ExecContext(ctx, `UPDATE release_guard_analyses SET status=?,analysis_json=? WHERE id=? AND guard_id=?`, a.Status, string(body), a.ID, a.GuardID)
	return err
}

func (s *Service) Analyses(ctx context.Context, project, guard string) ([]Analysis, error) {
	r, err := s.Snapshot(ctx, project, guard)
	if err != nil {
		return nil, err
	}
	if r.EvidenceExpired {
		return []Analysis{}, nil
	}
	rows, err := s.db.QueryContext(ctx, `SELECT analysis_json,status FROM release_guard_analyses WHERE guard_id=? ORDER BY created_at DESC LIMIT 20`, guard)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Analysis{}
	for rows.Next() {
		var b, status string
		var a Analysis
		if err = rows.Scan(&b, &status); err != nil {
			return nil, err
		}
		if json.Unmarshal([]byte(b), &a) != nil {
			return nil, ErrUnavailable
		}
		a.Status = status
		a.Stale = a.EvidenceSetHash != r.EvidenceSetHash || a.PlanHash != r.Plan.Hash
		out = append(out, a)
	}
	return out, rows.Err()
}

func (s *Service) PreviewAnalysis(ctx context.Context, project, guard string, in AnalysisInput) (*Analysis, error) {
	if !in.IncludeRuntime && !in.IncludeLogs {
		return nil, ErrInvalid
	}
	if s.model == nil {
		return nil, ErrUnavailable
	}
	r, err := s.Snapshot(ctx, project, guard)
	if err != nil {
		return nil, err
	}
	if r.EvidenceExpired {
		return nil, ErrUnavailable
	}
	provider, err := s.model.Info(ctx)
	if err != nil || provider.ConfigHash == "" || provider.Destination == "" {
		return nil, ErrUnavailable
	}
	rows, err := s.db.QueryContext(ctx, `SELECT evidence_json FROM release_guard_evidence WHERE guard_id=? AND sequence<=? ORDER BY sequence DESC LIMIT 200`, guard, r.EventsCursor)
	if err != nil {
		return nil, err
	}
	items := []map[string]any{}
	ids := []string{}
	seen := map[string]bool{}
	for rows.Next() {
		var body string
		var e Evidence
		if err = rows.Scan(&body); err != nil {
			rows.Close()
			return nil, err
		}
		if json.Unmarshal([]byte(body), &e) != nil {
			rows.Close()
			return nil, ErrUnavailable
		}
		if len(items) >= 15 {
			break
		}
		if e.Kind == "runtime" || e.Kind == "baseline" {
			if !in.IncludeRuntime || seen[e.Kind+e.TargetID] {
				continue
			}
			seen[e.Kind+e.TargetID] = true
			raw, _ := json.Marshal(e.Value)
			var v RuntimeSnapshot
			if json.Unmarshal(raw, &v) != nil {
				continue
			}
			refs := []string{}
			for _, ref := range v.RepoDigests {
				if len(refs) < 4 {
					refs = append(refs, ref)
				}
			}
			items = append(items, map[string]any{"evidenceId": e.ID, "target": e.TargetID, "kind": e.Kind, "at": e.CollectedAt, "outcome": v.Outcome, "containerId": v.ContainerID, "imageConfigId": v.ConfigID, "repoDigests": refs, "running": v.Running, "oomKilled": v.OOMKilled, "exitCode": v.ExitCode, "restartCount": v.RestartCount, "health": v.Health, "reason": v.Reason, "truncated": e.Truncated || len(v.RepoDigests) > 4})
		} else if e.Kind == "logs" {
			if !in.IncludeLogs || seen["logs"+e.TargetID] {
				continue
			}
			seen["logs"+e.TargetID] = true
			raw, _ := json.Marshal(e.Value)
			var value struct {
				Text  string    `json:"text"`
				From  time.Time `json:"from"`
				Until time.Time `json:"until"`
				Gap   bool      `json:"gap"`
			}
			if json.Unmarshal(raw, &value) != nil {
				continue
			}
			text := s.scrub(value.Text)
			clipped := len(text) > 2048
			if clipped {
				text = s.scrub(text[:2048])
			}
			items = append(items, map[string]any{"evidenceId": e.ID, "target": e.TargetID, "kind": "logs", "from": value.From, "until": value.Until, "text": text, "gap": value.Gap, "truncated": e.Truncated || clipped})
		} else {
			continue
		}
		ids = append(ids, e.ID)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	if len(items) == 0 {
		return nil, ErrUnavailable
	}
	payloadBytes, _ := json.Marshal(map[string]any{"scope": "frozen_release_guard_evidence", "items": items, "ruleResult": r.VerificationResult, "limitations": []string{"untrusted_evidence", "sampling_only", "causation_unproven", "host_identity_unverified"}})
	payload := s.scrub(string(payloadBytes))
	if len(payload) > 24<<10 {
		return nil, ErrCapacity
	}
	a := &Analysis{ID: uuid.NewString(), GuardID: guard, Status: "preview", Provider: provider, Input: in, Payload: payload, PayloadHash: digestJSON(payload), PlanHash: r.Plan.Hash, EvidenceSetHash: r.EvidenceSetHash, EvidenceIDs: ids, CreatedAt: s.now().UTC(), ExpiresAt: s.now().UTC().Add(10 * time.Minute), Limitations: []string{"one_snapshot_one_request", "already_sent_data_cannot_be_recalled", "no_tools_or_actions", "unknown_cost_is_not_zero"}}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || s.deleting[project] {
		return nil, ErrUnavailable
	}
	list, err := s.Analyses(ctx, project, guard)
	if err != nil {
		return nil, err
	}
	if len(list) >= 20 {
		return nil, ErrCapacity
	}
	for _, old := range list {
		if old.Status == "sending" || old.Status == "unknown" {
			return nil, ErrConflict
		}
		if old.PayloadHash == a.PayloadHash && old.Provider == a.Provider && !old.Revoked && (old.Status == "completed" || (old.Status == "preview" && s.now().Before(old.ExpiresAt))) {
			return &old, nil
		}
	}
	body, _ := json.Marshal(a)
	_, err = s.db.ExecContext(ctx, `INSERT INTO release_guard_analyses(id,guard_id,status,created_at,analysis_json) VALUES(?,?,?,?,?)`, a.ID, guard, a.Status, a.CreatedAt.UnixMilli(), string(body))
	if err != nil {
		return nil, err
	}
	return a, nil
}

func (s *Service) SendAnalysis(ctx context.Context, project, guard, id string, in SendAnalysisInput) (*Analysis, error) {
	s.mu.Lock()
	r, err := s.Snapshot(ctx, project, guard)
	if err != nil {
		s.mu.Unlock()
		return nil, err
	}
	if r.EvidenceExpired || s.closed || s.deleting[project] || s.model == nil {
		s.mu.Unlock()
		return nil, ErrUnavailable
	}
	a, err := s.loadAnalysis(ctx, guard, id)
	if err != nil {
		s.mu.Unlock()
		return nil, err
	}
	if !in.Confirmed || a.PayloadHash != in.PayloadHash || a.Provider.ConfigHash != in.ProviderConfigHash {
		s.mu.Unlock()
		return nil, ErrInvalid
	}
	if a.Status != "preview" {
		s.mu.Unlock()
		return a, nil
	}
	if a.Revoked || !s.now().Before(a.ExpiresAt) || a.PlanHash != r.Plan.Hash || a.EvidenceSetHash != r.EvidenceSetHash {
		s.mu.Unlock()
		return nil, ErrConflict
	}
	// Several category snapshots may have been prepared before the first send.
	// The unresolved-request fence belongs to the report, not just its preview.
	var unresolved int
	if err = s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM release_guard_analyses WHERE guard_id=? AND id<>? AND status IN ('sending','unknown')`, guard, id).Scan(&unresolved); err != nil {
		s.mu.Unlock()
		return nil, ErrUnavailable
	}
	if unresolved > 0 {
		s.mu.Unlock()
		return nil, ErrConflict
	}
	if len(s.analysisCancels) >= 2 {
		s.mu.Unlock()
		return nil, ErrCapacity
	}
	provider, err := s.model.Info(ctx)
	if err != nil || provider != a.Provider {
		s.mu.Unlock()
		return nil, ErrConflict
	}
	a.Status = "sending"
	if err = s.saveAnalysis(ctx, a); err != nil {
		s.mu.Unlock()
		return nil, err
	}
	callCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	s.analysisCancels[id] = cancel
	s.analysisProjects[id] = project
	s.wg.Add(1)
	s.mu.Unlock()
	text, callErr := s.model.Explain(callCtx, provider, a.Payload)
	cancel()
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.analysisCancels, id)
	delete(s.analysisProjects, id)
	s.wg.Done()
	current, loadErr := s.loadAnalysis(context.Background(), guard, id)
	if loadErr != nil {
		return nil, ErrNotFound
	}
	a = current
	a.Stale = false
	if report, e := s.Snapshot(context.Background(), project, guard); e != nil || report.EvidenceExpired || report.EvidenceSetHash != a.EvidenceSetHash || report.Plan.Hash != a.PlanHash {
		a.Stale = true
	}
	if errors.Is(callErr, ErrNotDispatched) {
		a.Status = "failed"
		a.Reason = "model_not_dispatched"
		if a.Revoked {
			a.Status = "revoked"
		}
	} else if callErr != nil || a.Revoked {
		a.Status = "unknown"
		a.Reason = "dispatch_outcome_unknown_no_automatic_retry"
	} else {
		result, e := validateExplanation(s.scrub(text), a.EvidenceIDs)
		if e != nil {
			a.Status = "failed"
			a.Reason = "analysis_schema_or_citation_invalid"
		} else {
			a.Status = "completed"
			a.Result = result
		}
	}
	if err = s.saveAnalysis(context.Background(), a); err != nil {
		return nil, ErrUnavailable
	}
	return a, nil
}

func (s *Service) RevokeAnalysis(ctx context.Context, project, guard, id string) (*Analysis, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, err := s.Snapshot(ctx, project, guard)
	if err != nil {
		return nil, err
	}
	if r.EvidenceExpired {
		return nil, ErrUnavailable
	}
	a, err := s.loadAnalysis(ctx, guard, id)
	if err != nil {
		return nil, err
	}
	a.Revoked = true
	if a.Status == "preview" {
		a.Status = "revoked"
	}
	if cancel := s.analysisCancels[id]; cancel != nil {
		cancel()
	}
	if err = s.saveAnalysis(ctx, a); err != nil {
		return nil, err
	}
	return a, nil
}

func validateExplanation(text string, ids []string) (*Explanation, error) {
	if len(text) > 16<<10 || !uniqueAnalysisJSON(text) {
		return nil, ErrInvalid
	}
	d := json.NewDecoder(bytes.NewBufferString(text))
	d.DisallowUnknownFields()
	var out Explanation
	if err := d.Decode(&out); err != nil {
		return nil, ErrInvalid
	}
	if d.Decode(new(any)) != io.EOF {
		return nil, ErrInvalid
	}
	if strings.TrimSpace(out.Summary) == "" || len(out.Summary) > 2048 || len(out.Candidates) > 8 {
		return nil, ErrInvalid
	}
	valid := map[string]bool{}
	for _, id := range ids {
		valid[id] = true
	}
	for i := range out.Candidates {
		c := out.Candidates[i]
		if c.ID == "" || len(c.ID) > 128 || len(c.Title) == 0 || len(c.Title) > 512 || len(c.SupportingEvidenceIDs) == 0 || len(c.SupportingEvidenceIDs) > 15 || len(c.CounterEvidenceIDs) > 15 || len(c.MissingEvidence) > 8 || len(c.NextReadOnlyCheck) > 512 {
			return nil, ErrInvalid
		}
		for _, id := range append(append([]string{}, c.SupportingEvidenceIDs...), c.CounterEvidenceIDs...) {
			if !valid[id] {
				return nil, ErrInvalid
			}
		}
		for _, missing := range c.MissingEvidence {
			if len(missing) > 512 {
				return nil, ErrInvalid
			}
		}
		if out.Candidates[i].CounterEvidenceIDs == nil {
			out.Candidates[i].CounterEvidenceIDs = []string{}
		}
		if out.Candidates[i].MissingEvidence == nil {
			out.Candidates[i].MissingEvidence = []string{}
		}
	}
	if out.Candidates == nil {
		out.Candidates = []Candidate{}
	}
	return &out, nil
}

func uniqueAnalysisJSON(text string) bool {
	d := json.NewDecoder(strings.NewReader(text))
	d.UseNumber()
	var walk func(int) bool
	walk = func(depth int) bool {
		if depth > 8 {
			return false
		}
		tok, err := d.Token()
		if err != nil {
			return false
		}
		delim, ok := tok.(json.Delim)
		if !ok {
			return true
		}
		switch delim {
		case '{':
			seen := map[string]bool{}
			for d.More() {
				key, err := d.Token()
				name, ok := key.(string)
				if err != nil || !ok {
					return false
				}
				name = strings.ToLower(name)
				if seen[name] {
					return false
				}
				seen[name] = true
				if !walk(depth + 1) {
					return false
				}
			}
			end, err := d.Token()
			return err == nil && end == json.Delim('}')
		case '[':
			for d.More() {
				if !walk(depth + 1) {
					return false
				}
			}
			end, err := d.Token()
			return err == nil && end == json.Delim(']')
		default:
			return false
		}
	}
	if !walk(0) {
		return false
	}
	_, err := d.Token()
	return err == io.EOF
}

func (s *Service) scrub(text string) string {
	text = sensitiveLine.ReplaceAllString(text, "[REDACTED]")
	if s.masker != nil {
		text = s.masker.ScrubTruncated(text)
	}
	return strings.ToValidUTF8(text, "")
}
