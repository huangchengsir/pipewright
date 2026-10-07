package opschat

import (
	"context"
	"database/sql"
	"encoding/json"
	"github.com/google/uuid"
	"time"
)

func bound(c Call) BoundCall {
	return BoundCall{CallID: c.ID, ServerID: c.ServerID, ToolID: c.ToolID, Object: c.Object, ArgsHash: c.ArgsHash, TargetHash: c.TargetHash}
}

func mutationCommand(argv []string) bool {
	if len(argv) != 3 || (argv[0] != "docker" && argv[0] != "systemctl") {
		return false
	}
	switch argv[1] {
	case "start", "stop", "restart", "pause", "unpause":
		return true
	}
	return false
}

func (s *Service) consumedApproval(ctx context.Context, tx *sql.Tx, call Call) error {
	a, err := s.approval(ctx, tx, call.SessionID, call.RunID)
	if err != nil {
		return err
	}
	if a.Status != "consumed" || a.Instance != s.instance || !a.View.ExpiresAt.After(time.Now()) || call.ArgsHash != digest(call.Args) {
		return ErrApproval
	}
	for _, binding := range a.Body.Calls {
		if binding == bound(call) {
			return nil
		}
	}
	return ErrApproval
}
func (s *Service) issueApproval(ctx context.Context, tx *sql.Tx, id, run string, calls []callRecord) error {
	body := approvalBody{Nonce: uuid.NewString(), Calls: []BoundCall{}}
	for _, c := range calls {
		if c.Body.View.Status == AwaitingConfirmation {
			body.Calls = append(body.Calls, bound(c.Body.View))
		}
	}
	if len(body.Calls) == 0 {
		return ErrApproval
	}
	b, err := s.seal(body)
	if err != nil {
		return err
	}
	var existing string
	err = tx.QueryRowContext(ctx, "SELECT id FROM ops_chat_approvals WHERE run_id=?", run).Scan(&existing)
	if err == sql.ErrNoRows {
		_, err = tx.ExecContext(ctx, `INSERT INTO ops_chat_approvals(id,session_id,run_id,status,instance_id,expires_at,body) VALUES(?,?,?,'pending',?,?,?)`, uuid.NewString(), id, run, s.instance, time.Now().Add(15*time.Minute).UnixMilli(), b)
	} else if err == nil {
		_, err = tx.ExecContext(ctx, "UPDATE ops_chat_approvals SET status='pending',instance_id=?,expires_at=?,body=? WHERE id=?", s.instance, time.Now().Add(15*time.Minute).UnixMilli(), b, existing)
	}
	return databaseError(err)
}
func (s *Service) ReissueConfirmation(ctx context.Context, id, run string) (*Confirmation, error) {
	if err := s.registerSecrets(); err != nil {
		return nil, err
	}
	if s.executor == nil {
		return nil, ErrUnavailable
	}
	if err := s.ready(); err != nil {
		return nil, err
	}
	calls, err := s.calls(ctx, s.db, id, run)
	if err != nil {
		return nil, err
	}
	for _, c := range calls {
		if c.Body.View.Status == AwaitingConfirmation {
			snap, e := s.executor.Capture(ctx, c.Body.View.ServerID)
			if e != nil || snap.Fingerprint() != c.Body.View.TargetHash {
				return nil, ErrApproval
			}
		}
	}
	err = s.transaction(ctx, true, func(tx *sql.Tx) error {
		r, e := s.run(ctx, tx, id, run)
		if e != nil {
			return e
		}
		if r.View.Status != AwaitingConfirmation || r.View.CancelRequested {
			return ErrApproval
		}
		current, e := s.calls(ctx, tx, id, run)
		if e != nil {
			return e
		}
		if digest(current) != digest(calls) {
			return ErrApproval
		}
		if e = s.issueApproval(ctx, tx, id, run, current); e != nil {
			return e
		}
		return s.append(ctx, tx, Entry{SessionID: id, RunID: run, Kind: "confirmation_ready", Status: AwaitingConfirmation}, false, false)
	})
	if err != nil {
		return nil, err
	}
	s.notify(id)
	a, err := s.approval(ctx, s.db, id, run)
	view := s.publicConfirmation(a.View)
	return &view, err
}
func (s *Service) Confirm(ctx context.Context, id string, in ConfirmInput) (*Run, error) {
	if err := s.registerSecrets(); err != nil {
		return nil, err
	}
	if s.executor == nil {
		return nil, ErrUnavailable
	}
	if err := s.ready(); err != nil {
		return nil, err
	}
	if len(in.Calls) < 1 || len(in.Calls) > 24 || !validID(in.Nonce) {
		return nil, ErrApproval
	}
	a, err := s.approval(ctx, s.db, id, in.RunID)
	if err != nil {
		return nil, err
	}
	if !a.View.Valid || a.Body.Nonce != in.Nonce || digest(s.publicConfirmation(a.View).Calls) != digest(in.Calls) {
		return nil, ErrApproval
	}
	// Capture is local and must run outside the SQLite transaction.
	calls, err := s.calls(ctx, s.db, id, in.RunID)
	if err != nil {
		return nil, err
	}
	for _, c := range calls {
		if c.Body.View.Status == AwaitingConfirmation {
			snap, e := s.executor.Capture(ctx, c.Body.View.ServerID)
			if e != nil || snap.Fingerprint() != c.Body.View.TargetHash {
				return nil, ErrApproval
			}
		}
	}
	err = s.transaction(ctx, true, func(tx *sql.Tx) error {
		r, e := s.run(ctx, tx, id, in.RunID)
		if e != nil {
			return e
		}
		if r.View.Status != AwaitingConfirmation || r.View.CancelRequested {
			return ErrApproval
		}
		current, e := s.approval(ctx, tx, id, in.RunID)
		if e != nil {
			return e
		}
		if !current.View.Valid || current.Body.Nonce != in.Nonce || digest(s.publicConfirmation(current.View).Calls) != digest(in.Calls) {
			return ErrApproval
		}
		cs, e := s.calls(ctx, tx, id, in.RunID)
		if e != nil {
			return e
		}
		if digest(cs) != digest(calls) {
			return ErrApproval
		}
		result, e := tx.ExecContext(ctx, "UPDATE ops_chat_approvals SET status='consumed' WHERE id=? AND status='pending' AND instance_id=?", current.ID, s.instance)
		if e != nil {
			return ErrStorage
		}
		n, e := result.RowsAffected()
		if e != nil || n != 1 {
			return ErrApproval
		}
		for _, c := range cs {
			if c.Body.View.Status == AwaitingConfirmation {
				c.Body.View.Status = Queued
				if e = s.saveCall(ctx, tx, c); e != nil {
					return e
				}
			}
		}
		r.View.Status = Queued
		if e = s.saveRun(ctx, tx, r); e != nil {
			return e
		}
		return s.append(ctx, tx, Entry{SessionID: id, RunID: in.RunID, Kind: "run_status", Status: Queued}, false, false)
	})
	if err != nil {
		return nil, err
	}
	s.notify(id)
	s.launch(id, in.RunID)
	r, err := s.run(ctx, s.db, id, in.RunID)
	if err != nil {
		return nil, err
	}
	return &r.View, nil
}
func validProvider(p ProviderInfo) bool {
	return p.Provider != "" && p.Model != "" && len(p.Provider) <= 256 && len(p.Model) <= 256 && len(p.ConfigHash) == 64
}
func (s *Service) AnalysisPreview(ctx context.Context, id string, ids []string) (*AnalysisPreview, error) {
	if err := s.registerSecrets(); err != nil {
		return nil, err
	}
	if s.model == nil {
		return nil, ErrUnavailable
	}
	p := s.model.Info()
	if !validProvider(p) {
		return nil, ErrUnavailable
	}
	out := &AnalysisPreview{CallIDs: append([]string{}, ids...), Items: []AnalysisItem{}, Provider: p}
	if len(ids) == 0 || len(ids) > 24 {
		return nil, ErrInvalid
	}
	seen := map[string]bool{}
	size := 0
	aliases := map[string]string{}
	err := s.transaction(ctx, false, func(tx *sql.Tx) error {
		if _, e := s.session(ctx, tx, id); e != nil {
			return e
		}
		for _, cid := range ids {
			if !validID(cid) || seen[cid] {
				return ErrInvalid
			}
			seen[cid] = true
			var b []byte
			var status string
			if e := tx.QueryRowContext(ctx, "SELECT body,status FROM ops_chat_calls WHERE id=? AND session_id=?", cid, id).Scan(&b, &status); e != nil {
				return databaseError(e)
			}
			var c callBody
			if e := s.open(b, &c); e != nil {
				return e
			}
			if !terminal(status) || c.View.CollectedAt == nil {
				return ErrConflict
			}
			alias, ok := aliases[c.View.ServerID]
			if !ok {
				alias = "target_" + string(rune('A'+len(aliases)))
				aliases[c.View.ServerID] = alias
			}
			item := AnalysisItem{CallID: cid, TargetAlias: alias, ToolID: c.View.ToolID, Output: c.View.Output, Error: c.View.Error, Status: status}
			if c.View.Resources != nil {
				encoded, _ := json.Marshal(c.View.Resources)
				item.Output = string(encoded)
			}
			item.Output = s.scrub(item.Output, 64<<10)
			item.Error = s.scrub(item.Error, 1024)
			encoded, _ := json.Marshal(item)
			size += len(encoded)
			if size > 30<<10 {
				return ErrQuota
			}
			out.Items = append(out.Items, item)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	out.Hash = digest(struct {
		SessionID string
		CallIDs   []string
		Items     []AnalysisItem
		Provider  ProviderInfo
	}{id, out.CallIDs, out.Items, out.Provider})
	return out, nil
}
func (s *Service) Analyze(ctx context.Context, id string, in AnalysisInput) (*Run, error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	if !in.Consent || !validID(in.ClientRequestID) {
		return nil, ErrConsent
	}
	hash := digest(in)
	if old, err := s.duplicate(ctx, id, in.ClientRequestID, hash); old != nil || err != nil {
		return old, err
	}
	if err := s.registerSecrets(); err != nil {
		return nil, err
	}
	preview, err := s.AnalysisPreview(ctx, id, in.CallIDs)
	if err != nil {
		return nil, err
	}
	if preview.Hash != in.PreviewHash || preview.Provider != in.Provider {
		return nil, ErrConsent
	}
	preview.Items = nil // Only references and permission persist; result text has a single owner.
	return s.enqueue(ctx, id, in.ClientRequestID, hash, runBody{Analysis: preview}, in.Revision)
}
