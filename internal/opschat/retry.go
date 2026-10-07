package opschat

import (
	"context"
	"database/sql"
	"encoding/json"
	"sort"
)

func (s *Service) call(ctx context.Context, q querier, id, cid string) (callRecord, error) {
	var c callRecord
	var blob []byte
	var status string
	err := q.QueryRowContext(ctx, "SELECT body,status,created_at,ordinal FROM ops_chat_calls WHERE session_id=? AND id=?", id, cid).Scan(&blob, &status, &c.Created, &c.Ordinal)
	if err != nil {
		return c, databaseError(err)
	}
	if err = s.open(blob, &c.Body); err != nil {
		return c, err
	}
	c.Body.View.Status = status
	return c, nil
}

// Call retrieves historical results without changing records or visiting SSH/model.
func (s *Service) Call(ctx context.Context, id, cid string) (*Call, error) {
	if err := s.registerSecrets(); err != nil {
		return nil, err
	}
	c, err := s.call(ctx, s.db, id, cid)
	if err != nil {
		return nil, err
	}
	view := s.publicCall(c.Body.View)
	return &view, nil
}
func retryable(c callRecord) error {
	switch c.Body.View.Status {
	case Failed, PartialFailed:
		return nil
	case Interrupted:
		if !c.Body.Dispatched {
			return nil
		}
		return ErrVerifyFirst
	case Unknown:
		return ErrVerifyFirst
	default:
		return ErrConflict
	}
}
func (s *Service) RetryFailed(ctx context.Context, id string, in RetryInput) (*Run, error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	if !validID(in.ClientRequestID) || len(in.CallIDs) < 1 || len(in.CallIDs) > 24 {
		return nil, ErrInvalid
	}
	hash := digest(struct {
		Operation string
		Input     RetryInput
	}{"retry_failed", in})
	if old, e := s.duplicate(ctx, id, in.ClientRequestID, hash); old != nil || e != nil {
		return old, e
	}
	if s.executor == nil {
		return nil, ErrUnavailable
	}
	sr, err := s.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if sr.Revision != in.Revision || sr.ActiveRunID != "" {
		return nil, ErrConflict
	}
	selected := map[string]bool{}
	for _, sid := range sr.ServerIDs {
		selected[sid] = true
	}
	body := runBody{RetryCallIDs: append([]string{}, in.CallIDs...), RetryActions: []Action{}}
	indexes := map[string]int{}
	seen := map[string]bool{}
	// Validate every source before capturing any connection or accepting a new task.
	oldCalls := []callRecord{}
	runTimes := map[string]int64{}
	for _, cid := range in.CallIDs {
		if !validID(cid) || seen[cid] {
			return nil, ErrInvalid
		}
		seen[cid] = true
		c, e := s.call(ctx, s.db, id, cid)
		if e != nil {
			return nil, e
		}
		if e = retryable(c); e != nil {
			return nil, e
		}
		if !selected[c.Body.View.ServerID] {
			return nil, ErrConflict
		}
		args, e := json.Marshal(c.Body.View.Args)
		if e != nil {
			return nil, ErrInvalid
		}
		if _, e = parseArgs(c.Body.View.ToolID, args); e != nil {
			return nil, e
		}
		oldCalls = append(oldCalls, c)
		if _, ok := runTimes[c.Body.View.RunID]; !ok {
			r, e := s.run(ctx, s.db, id, c.Body.View.RunID)
			if e != nil {
				return nil, e
			}
			runTimes[c.Body.View.RunID] = r.View.CreatedAt.UnixMilli()
		}
	}
	sort.SliceStable(oldCalls, func(i, j int) bool {
		a, b := oldCalls[i], oldCalls[j]
		if a.Body.View.RunID == b.Body.View.RunID {
			return a.Ordinal < b.Ordinal
		}
		if runTimes[a.Body.View.RunID] != runTimes[b.Body.View.RunID] {
			return runTimes[a.Body.View.RunID] < runTimes[b.Body.View.RunID]
		}
		return a.Body.View.RunID < b.Body.View.RunID
	})
	body.RetryCallIDs = nil
	for _, c := range oldCalls {
		body.RetryCallIDs = append(body.RetryCallIDs, c.Body.View.ID)
		sid := c.Body.View.ServerID
		index, ok := indexes[sid]
		if !ok {
			snap, e := s.executor.Capture(ctx, sid)
			if e != nil || snap.Server.ID != sid || snap.Fingerprint() != c.Body.View.TargetHash {
				return nil, ErrConflict
			}
			index = len(body.Targets)
			indexes[sid] = index
			body.Targets = append(body.Targets, snap)
		} else if body.Targets[index].Fingerprint() != c.Body.View.TargetHash {
			return nil, ErrConflict
		}
		args, _ := json.Marshal(c.Body.View.Args)
		body.RetryActions = append(body.RetryActions, Action{ToolID: c.Body.View.ToolID, Args: args, TargetIndexes: []int{index}})
	}
	if len(body.Targets) > 8 {
		return nil, ErrInvalid
	}
	if err = s.registerSecrets(); err != nil {
		return nil, err
	}
	return s.enqueue(ctx, id, in.ClientRequestID, hash, body, in.Revision)
}
func (s *Service) validateRetryTx(ctx context.Context, tx *sql.Tx, id string, body runBody, sr sessionRecord) error {
	if len(body.RetryActions) == 0 {
		return nil
	}
	if len(body.RetryCallIDs) != len(body.RetryActions) {
		return ErrInvalid
	}
	selected := map[string]bool{}
	for _, sid := range sr.Body.ServerIDs {
		selected[sid] = true
	}
	for i, cid := range body.RetryCallIDs {
		c, e := s.call(ctx, tx, id, cid)
		if e != nil {
			return e
		}
		if e = retryable(c); e != nil {
			return e
		}
		a := body.RetryActions[i]
		if len(a.TargetIndexes) != 1 {
			return ErrInvalid
		}
		index := a.TargetIndexes[0]
		if index < 0 || index >= len(body.Targets) {
			return ErrInvalid
		}
		snap := body.Targets[index]
		args, _ := json.Marshal(c.Body.View.Args)
		if !selected[c.Body.View.ServerID] || snap.Server.ID != c.Body.View.ServerID || snap.Fingerprint() != c.Body.View.TargetHash || a.ToolID != c.Body.View.ToolID || digest(a.Args) != digest(json.RawMessage(args)) {
			return ErrConflict
		}
	}
	return nil
}
