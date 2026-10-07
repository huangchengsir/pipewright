package opschat

import (
	"context"
	"database/sql"
	"encoding/json"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/huangchengsir/pipewright/internal/audit"
	"github.com/huangchengsir/pipewright/internal/target"
)

const leaseDuration = 15 * time.Second

func (s *Service) Start(root context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.started || s.closing {
		return ErrConflict
	}
	waitCtx, cancel := context.WithTimeout(root, leaseDuration+500*time.Millisecond)
	defer cancel()
	var seen leaseObservation
	for {
		err := s.acquireLease(waitCtx, &seen)
		if err == nil {
			break
		}
		if root.Err() != nil {
			return root.Err()
		}
		if err != ErrLease || seen.renewed || waitCtx.Err() != nil {
			return err
		}
		select {
		case <-waitCtx.Done():
			if root.Err() != nil {
				return root.Err()
			}
			return ErrLease
		case <-time.After(100 * time.Millisecond):
		}
	}
	s.root, s.stop = context.WithCancel(root)
	s.started = true
	s.leaseDone = make(chan struct{})
	go s.leaseLoop()
	return nil
}

type leaseObservation struct {
	owner   string
	until   int64
	renewed bool
}

func (s *Service) acquireLease(root context.Context, seen *leaseObservation) error {
	return s.transaction(root, false, func(tx *sql.Tx) error {
		if _, e := tx.ExecContext(root, "UPDATE ops_chat_state SET lease_until=lease_until WHERE id=1"); e != nil {
			return ErrStorage
		}
		var owner string
		var until int64
		if tx.QueryRowContext(root, "SELECT lease_owner,lease_until FROM ops_chat_state WHERE id=1").Scan(&owner, &until) != nil {
			return ErrStorage
		}
		if owner != "" && until > time.Now().UnixMilli() {
			if seen.owner != "" && (seen.owner != owner || until > seen.until) {
				seen.renewed = true
			}
			seen.owner, seen.until = owner, until
			return ErrLease
		}
		// Validate every encrypted record before modifying recovery state.
		for _, table := range []string{"ops_chat_sessions", "ops_chat_entries", "ops_chat_runs", "ops_chat_calls", "ops_chat_approvals"} {
			rows, e := tx.QueryContext(root, "SELECT body FROM "+table)
			if e != nil {
				return ErrStorage
			}
			for rows.Next() {
				var b []byte
				if rows.Scan(&b) != nil {
					rows.Close()
					return ErrStorage
				}
				var plain any
				if e = s.open(b, &plain); e != nil {
					rows.Close()
					return e
				}
			}
			e = rows.Err()
			rows.Close()
			if e != nil {
				return ErrStorage
			}
		}
		rows, e := tx.QueryContext(root, `SELECT session_id,id FROM ops_chat_runs WHERE status IN ('queued','planning','running')`)
		if e != nil {
			return ErrStorage
		}
		ids := [][2]string{}
		for rows.Next() {
			var pair [2]string
			if rows.Scan(&pair[0], &pair[1]) != nil {
				rows.Close()
				return ErrStorage
			}
			ids = append(ids, pair)
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			return ErrStorage
		}
		for _, pair := range ids {
			r, e := s.run(root, tx, pair[0], pair[1])
			if e != nil {
				return e
			}
			calls, e := s.calls(root, tx, pair[0], pair[1])
			if e != nil {
				return e
			}
			status := Interrupted
			for _, c := range calls {
				if c.Body.View.Status == Unknown {
					status = Unknown
				}
				if c.Body.View.Status == Running && c.Body.Dispatched {
					c.Body.View.Status = Unknown
					status = Unknown
				} else if !terminal(c.Body.View.Status) {
					c.Body.View.Status = Interrupted
				}
				if e = s.saveCall(root, tx, c); e != nil {
					return e
				}
			}
			if e = s.finishTx(root, tx, r, status); e != nil {
				return e
			}
		}
		_, e = tx.ExecContext(root, "UPDATE ops_chat_state SET lease_owner=?,lease_until=? WHERE id=1", s.instance, time.Now().Add(leaseDuration).UnixMilli())
		return databaseError(e)
	})
}

// beginCall commits the dispatch intent before even a read-only resolver reaches SSH.
func (s *Service) beginCall(ctx context.Context, c *callRecord) error {
	if ctx.Err() != nil {
		return ErrConflict
	}
	v := &c.Body.View
	return s.persist(v.SessionID, func(cc context.Context, tx *sql.Tx) error {
		r, e := s.run(cc, tx, v.SessionID, v.RunID)
		if e != nil {
			return e
		}
		if r.View.CancelRequested || terminal(r.View.Status) {
			return ErrConflict
		}
		var status string
		if tx.QueryRowContext(cc, "SELECT status FROM ops_chat_calls WHERE id=? AND session_id=? AND run_id=?", v.ID, v.SessionID, v.RunID).Scan(&status) != nil {
			return ErrStorage
		}
		if status != Queued {
			return ErrConflict
		}
		v.Status = Running
		c.Body.Dispatched = false
		if e = s.saveCall(cc, tx, *c); e != nil {
			return e
		}
		return s.append(cc, tx, Entry{SessionID: v.SessionID, RunID: v.RunID, CallID: v.ID, Kind: "call_status", Status: Running}, false, false)
	})
}

func (s *Service) leaseLoop() {
	defer close(s.leaseDone)
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-s.root.Done():
			return
		case <-ticker.C:
			ctx, cancel := context.WithTimeout(s.root, 5*time.Second)
			err := s.transaction(ctx, true, func(tx *sql.Tx) error {
				_, e := tx.ExecContext(ctx, "UPDATE ops_chat_state SET lease_until=? WHERE id=1 AND lease_owner=?", time.Now().Add(leaseDuration).UnixMilli(), s.instance)
				return databaseError(e)
			})
			cancel()
			if err != nil {
				s.mu.Lock()
				s.lost = true
				s.stop()
				s.mu.Unlock()
				return
			}
		}
	}
}
func (s *Service) Close(ctx context.Context) error {
	s.mu.Lock()
	s.closing = true
	if s.stop != nil {
		s.stop()
	}
	jobs := []*job{}
	for _, j := range s.jobs {
		j.cancel()
		jobs = append(jobs, j)
	}
	for id, subs := range s.wake {
		for ch := range subs {
			close(ch)
		}
		delete(s.wake, id)
	}
	leaseDone := s.leaseDone
	s.mu.Unlock()
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	for _, j := range jobs {
		select {
		case <-j.done:
		case <-ctx.Done():
			return ErrConflict
		}
	}
	if leaseDone != nil {
		select {
		case <-leaseDone:
		case <-ctx.Done():
			return ErrConflict
		}
	}
	_, err := s.db.ExecContext(ctx, "UPDATE ops_chat_state SET lease_owner='',lease_until=0 WHERE id=1 AND lease_owner=?", s.instance)
	return databaseError(err)
}
func (s *Service) launch(id, run string) {
	s.mu.Lock()
	if !s.started || s.closing || s.lost {
		s.mu.Unlock()
		return
	}
	if existing := s.jobs[run]; existing != nil {
		existing.retry = true
		s.mu.Unlock()
		return
	}
	ctx, cancel := context.WithCancel(s.root)
	j := &job{session: id, cancel: cancel, done: make(chan struct{})}
	s.jobs[run] = j
	s.mu.Unlock()
	go func() {
		defer cancel()
		for {
			s.work(ctx, id, run)
			s.mu.Lock()
			if j.retry && ctx.Err() == nil && !s.closing && !s.lost {
				j.retry = false
				s.mu.Unlock()
				continue
			}
			delete(s.jobs, run)
			close(j.done)
			s.mu.Unlock()
			return
		}
	}()
}
func (s *Service) persist(id string, fn func(context.Context, *sql.Tx) error) error {
	return s.persistAttempts(id, fn, 3)
}

func (s *Service) persistAttempts(id string, fn func(context.Context, *sql.Tx) error, attempts int) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var err error
	for attempt := 0; attempt < attempts; attempt++ {
		err = s.transaction(ctx, true, func(tx *sql.Tx) error { return fn(ctx, tx) })
		if err != ErrStorage || ctx.Err() != nil || attempt == attempts-1 {
			break
		}
		select {
		case <-ctx.Done():
		case <-time.After(100 * time.Millisecond):
		}
		if ctx.Err() != nil {
			break
		}
	}
	if err == ErrLease {
		s.mu.Lock()
		s.lost = true
		if s.stop != nil {
			s.stop()
		}
		s.mu.Unlock()
	}
	if err == nil {
		s.notify(id)
	}
	return err
}
func (s *Service) failRun(id, run, status string) {
	if err := s.convergeRun(id, run, status, false); err != nil {
		s.fenceWorker()
	}
}

func (s *Service) fenceWorker() {
	s.mu.Lock()
	s.lost = true
	if s.stop != nil {
		s.stop()
	}
	s.mu.Unlock()
}

// Only reconcile local facts. Never retry model, SSH, or consumed mutations.
func (s *Service) convergeRun(id, run, status string, onlyWorking bool) error {
	return s.persistAttempts(id, func(ctx context.Context, tx *sql.Tx) error {
		r, e := s.run(ctx, tx, id, run)
		if e != nil {
			return e
		}
		if terminal(r.View.Status) {
			return nil
		}
		// A confirmation may have queued a fresh phase while this worker exits.
		if onlyWorking && r.View.Status != Planning && r.View.Status != Running {
			return nil
		}
		calls, e := s.calls(ctx, tx, id, run)
		if e != nil {
			return e
		}
		successes := 0
		uncertain := false
		for _, c := range calls {
			if c.Body.View.Status == Unknown {
				uncertain = true
			}
			if c.Body.View.Status == Running && c.Body.Dispatched {
				c.Body.View.Status = Unknown
				uncertain = true
			}
			if c.Body.View.Status == Succeeded {
				successes++
			}
			if !terminal(c.Body.View.Status) {
				c.Body.View.Status = Interrupted
			}
			if e = s.saveCall(ctx, tx, c); e != nil {
				return e
			}
		}
		if uncertain {
			status = Unknown
		} else if successes > 0 && len(calls) > successes {
			status = PartialFailed
		}
		return s.finishTx(ctx, tx, r, status)
	}, 50)
}
func (s *Service) work(parent context.Context, id, run string) {
	defer func() {
		if err := s.convergeRun(id, run, Failed, true); err != nil {
			s.fenceWorker()
		}
	}()
	r, err := s.run(parent, s.db, id, run)
	if err != nil {
		s.failRun(id, run, Interrupted)
		return
	}
	if r.View.Status != Queued {
		return
	}
	remaining := 180*time.Second - time.Duration(r.Body.ActiveMillis)*time.Millisecond
	if remaining <= 0 {
		s.failRun(id, run, Interrupted)
		return
	}
	ctx, cancel := context.WithTimeout(parent, remaining)
	defer cancel()
	began := time.Now()
	defer func() {
		if ctx.Err() != nil {
			s.failRun(id, run, Interrupted)
		}
	}()
	err = s.persist(id, func(c context.Context, tx *sql.Tx) error {
		current, e := s.run(c, tx, id, run)
		if e != nil {
			return e
		}
		if current.View.Status != Queued || current.View.CancelRequested {
			return ErrConflict
		}
		current.View.Status = Planning
		if e = s.saveRun(c, tx, current); e != nil {
			return e
		}
		return s.append(c, tx, Entry{SessionID: id, RunID: run, Kind: "run_status", Status: Planning}, false, false)
	})
	if err != nil {
		s.failRun(id, run, Interrupted)
		return
	}
	if err = s.registerSecrets(); err != nil {
		s.failRun(id, run, Failed)
		return
	}
	if r.Body.Analysis != nil {
		s.analyzeWork(ctx, id, run, r)
		return
	}
	calls, err := s.calls(ctx, s.db, id, run)
	if err != nil {
		s.failRun(id, run, Failed)
		return
	}
	if len(calls) == 0 {
		plan := Plan{}
		if len(r.Body.RetryActions) > 0 {
			plan.Actions = r.Body.RetryActions
		} else if r.Body.Turn.ToolID != "" {
			indexes := []int{}
			for i := range r.Body.Targets {
				indexes = append(indexes, i)
			}
			plan.Actions = []Action{{ToolID: r.Body.Turn.ToolID, Args: r.Body.Turn.Args, TargetIndexes: indexes}}
		} else {
			messages, e := s.contextMessages(ctx, id)
			if e != nil {
				s.failRun(id, run, Failed)
				return
			}
			aliases := []string{}
			for i := range r.Body.Targets {
				aliases = append(aliases, "target_"+strconv.Itoa(i))
			}
			req := PlanRequest{Messages: messages, Targets: aliases, Tools: Tools(), MaxResponseBytes: 32 << 10}
			for {
				encoded, _ := json.Marshal(req)
				if len(encoded) <= 32<<10 {
					break
				}
				if len(req.Messages) <= 1 {
					s.failRun(id, run, Failed)
					return
				}
				req.Messages = req.Messages[1:]
			}
			if s.model == nil {
				s.failRun(id, run, Failed)
				return
			}
			r.Body.ModelCalls++
			if r.Body.ModelCalls > 2 {
				s.failRun(id, run, Failed)
				return
			}
			e = s.persist(id, func(c context.Context, tx *sql.Tx) error {
				current, e := s.run(c, tx, id, run)
				if e != nil {
					return e
				}
				current.Body.ModelCalls = r.Body.ModelCalls
				return s.saveRun(c, tx, current)
			})
			if e != nil {
				return
			}
			modelCtx, stop := context.WithTimeout(ctx, 60*time.Second)
			plan, e = s.model.Plan(modelCtx, req)
			stop()
			if e != nil || s.registerSecrets() != nil {
				s.failRun(id, run, Failed)
				return
			}
			raw, e := json.Marshal(plan)
			if e != nil || len(raw) > 32<<10 {
				s.failRun(id, run, Failed)
				return
			}
		}
		// Validate the whole plan before any call is executed.
		candidates := []callRecord{}
		for _, a := range plan.Actions {
			args, e := parseArgs(a.ToolID, a.Args)
			if e != nil || s.secretArgs(args) || len(a.TargetIndexes) == 0 {
				s.failRun(id, run, Failed)
				return
			}
			seen := map[int]bool{}
			for _, index := range a.TargetIndexes {
				if index < 0 || index >= len(r.Body.Targets) || seen[index] {
					s.failRun(id, run, Failed)
					return
				}
				seen[index] = true
				snap := r.Body.Targets[index]
				object := args.Container
				if object == "" {
					object = args.Unit
				}
				view := Call{ID: uuid.NewString(), RunID: run, SessionID: id, ServerID: snap.Server.ID, ServerName: s.scrub(snap.Server.Name, 1024), ToolID: a.ToolID, Args: args, Object: object, Status: Queued, ArgsHash: digest(args), TargetHash: snap.Fingerprint()}
				candidates = append(candidates, callRecord{Body: callBody{View: view, Snapshot: snap}, Created: time.Now().UnixMilli(), Ordinal: len(candidates)})
				if len(candidates) > 24 {
					s.failRun(id, run, Failed)
					return
				}
			}
		}
		text := s.scrub(plan.Text, 16<<10)
		advice := s.scrub(plan.ManualAdvice, 8<<10)
		if advice != "" {
			text += "\n" + advice
		}
		err = s.persist(id, func(c context.Context, tx *sql.Tx) error {
			current, e := s.run(c, tx, id, run)
			if e != nil {
				return e
			}
			if current.View.CancelRequested {
				return ErrConflict
			}
			if text != "" {
				if e = s.append(c, tx, Entry{SessionID: id, RunID: run, Kind: "assistant", Text: text}, true, true); e != nil {
					return e
				}
			}
			for _, candidate := range candidates {
				b, e := s.seal(candidate.Body)
				if e != nil {
					return e
				}
				v := candidate.Body.View
				_, e = tx.ExecContext(c, `INSERT INTO ops_chat_calls(id,session_id,run_id,server_id,tool_id,status,args_hash,target_hash,body,created_at,ordinal) VALUES(?,?,?,?,?,?,?,?,?,?,?)`, v.ID, id, run, v.ServerID, v.ToolID, v.Status, v.ArgsHash, v.TargetHash, b, candidate.Created, candidate.Ordinal)
				if e != nil {
					return ErrStorage
				}
				if e = s.append(c, tx, Entry{SessionID: id, RunID: run, CallID: v.ID, Kind: "tool_call"}, true, false); e != nil {
					return e
				}
			}
			current.View.Status = Running
			if e = s.saveRun(c, tx, current); e != nil {
				return e
			}
			return s.append(c, tx, Entry{SessionID: id, RunID: run, Kind: "run_status", Status: Running}, false, false)
		})
		if err != nil {
			s.failRun(id, run, Failed)
			return
		}
		calls = candidates
	}
	// Calls in a run are serial; separate sessions can use the four global SSH slots.
	barrier := false
	for _, call := range calls {
		if ctx.Err() != nil {
			break
		}
		if call.Body.View.Status == AwaitingConfirmation {
			barrier = true
			continue
		}
		if terminal(call.Body.View.Status) {
			continue
		}
		if mutation(call.Body.View.ToolID) {
			a, e := s.approval(ctx, s.db, id, run)
			approved := e == nil && a.Status == "consumed" && a.Instance == s.instance && a.View.ExpiresAt.After(time.Now())
			if !approved {
				barrier = true
				if e = s.prepareMutation(ctx, call); e != nil {
					s.failRun(id, run, Failed)
					return
				}
				continue
			}
		}
		// Only mutation object resolution may cross the confirmation barrier.
		if barrier {
			continue
		}
		if err = s.executeCall(ctx, call); err != nil {
			s.failRun(id, run, Failed)
			return
		}
	}
	if ctx.Err() != nil {
		return
	}
	_ = s.persist(id, func(c context.Context, tx *sql.Tx) error {
		current, e := s.run(c, tx, id, run)
		if e != nil {
			return e
		}
		if terminal(current.View.Status) {
			return nil
		}
		current.Body.ActiveMillis += time.Since(began).Milliseconds()
		cs, e := s.calls(c, tx, id, run)
		if e != nil {
			return e
		}
		pending := false
		for _, call := range cs {
			pending = pending || call.Body.View.Status == AwaitingConfirmation
		}
		successes, failures, unknowns := 0, 0, 0
		for _, call := range cs {
			if call.Body.View.Status == Running {
				call.Body.View.Status = Interrupted
				if call.Body.Dispatched {
					call.Body.View.Status = Unknown
				}
				if e = s.saveCall(c, tx, call); e != nil {
					return e
				}
			}
			if call.Body.View.Status == Queued && !pending {
				call.Body.View.Status = Interrupted
				if e = s.saveCall(c, tx, call); e != nil {
					return e
				}
			}
			switch call.Body.View.Status {
			case AwaitingConfirmation:
				pending = true
			case Succeeded:
				successes++
			case PartialFailed:
				successes++
				failures++
			case Unknown:
				unknowns++
			default:
				failures++
			}
		}
		if pending {
			if e = s.issueApproval(c, tx, id, run, cs); e != nil {
				return e
			}
			current.View.Status = AwaitingConfirmation
			if e = s.saveRun(c, tx, current); e != nil {
				return e
			}
			return s.append(c, tx, Entry{SessionID: id, RunID: run, Kind: "confirmation_ready", Status: AwaitingConfirmation}, false, false)
		}
		status := Succeeded
		if unknowns > 0 {
			status = Unknown
		} else if failures > 0 {
			status = Failed
			if successes > 0 {
				status = PartialFailed
			}
		}
		return s.finishTx(c, tx, current, status)
	})
}
func (s *Service) analyzeWork(ctx context.Context, id, run string, r runRecord) {
	preview, err := s.AnalysisPreview(ctx, id, r.Body.Analysis.CallIDs)
	if err != nil || preview.Hash != r.Body.Analysis.Hash || preview.Provider != r.Body.Analysis.Provider {
		s.failRun(id, run, Failed)
		return
	}
	req := AnalysisRequest{Provider: preview.Provider, Items: preview.Items, MaxResponseBytes: 16 << 10}
	raw, _ := json.Marshal(req)
	if len(raw) > 32<<10 {
		s.failRun(id, run, Failed)
		return
	}
	err = s.persist(id, func(c context.Context, tx *sql.Tx) error {
		current, e := s.run(c, tx, id, run)
		if e != nil {
			return e
		}
		if current.View.CancelRequested || current.Body.ModelCalls >= 2 {
			return ErrConflict
		}
		current.Body.ModelCalls++
		return s.saveRun(c, tx, current)
	})
	if err != nil {
		return
	}
	modelCtx, stop := context.WithTimeout(ctx, 60*time.Second)
	text, err := s.model.Analyze(modelCtx, req)
	stop()
	if err != nil || len(text) > 16<<10 || s.registerSecrets() != nil {
		s.failRun(id, run, Failed)
		return
	}
	_ = s.persist(id, func(c context.Context, tx *sql.Tx) error {
		current, e := s.run(c, tx, id, run)
		if e != nil {
			return e
		}
		if current.View.CancelRequested || ctx.Err() != nil {
			return s.finishTx(c, tx, current, Interrupted)
		}
		// Derived analysis is excluded from every future planning context.
		if e = s.append(c, tx, Entry{SessionID: id, RunID: run, Kind: "analysis", Text: s.scrub(text, 16<<10)}, true, false); e != nil {
			return e
		}
		return s.finishTx(c, tx, current, Succeeded)
	})
}
func (s *Service) takeSlot(ctx context.Context, server string) (func(), error) {
	s.mu.Lock()
	slot := s.serverSlots[server]
	if slot == nil {
		slot = make(chan struct{}, 1)
		s.serverSlots[server] = slot
	}
	s.mu.Unlock()
	select {
	case slot <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	select {
	case s.slots <- struct{}{}:
	case <-ctx.Done():
		<-slot
		return nil, ctx.Err()
	}
	return func() { <-s.slots; <-slot }, nil
}

type readBudget struct {
	bytes     int
	lines     int
	truncated bool
}

func newReadBudget() *readBudget { return &readBudget{bytes: 64 << 10, lines: 1000} }
func remainingReadBudget(c callRecord) *readBudget {
	return &readBudget{bytes: max(0, (64<<10)-c.Body.ReadBytes), lines: max(0, 1000-c.Body.ReadLines), truncated: c.Body.ReadTruncated}
}
func (s *Service) bounded(ctx context.Context, c *callRecord, argv []string, b *readBudget) (*target.LimitedResult, error) {
	release, err := s.takeSlot(ctx, c.Body.Snapshot.Server.ID)
	if err != nil {
		return nil, err
	}
	defer release()
	return s.boundedInSlot(ctx, c, argv, b)
}

// The caller owns both slots across mutation and verification; never acquire recursively.
func (s *Service) boundedInSlot(ctx context.Context, c *callRecord, argv []string, b *readBudget) (*target.LimitedResult, error) {
	snap := c.Body.Snapshot
	if b.bytes < 1 || b.lines < 1 {
		b.truncated = true
		c.Body.ReadTruncated = true
		return nil, ErrQuota
	}
	if s.executor == nil {
		return nil, ErrUnavailable
	}
	if err := s.registerSecrets(); err != nil {
		return nil, err
	}
	if s.secretArgs(c.Body.View.Args) {
		return nil, ErrInvalid
	}
	// Revalidate the lease after waiting for slots, before each SSH dispatch.
	limitBytes := b.bytes
	err := s.persist(c.Body.View.SessionID, func(cc context.Context, tx *sql.Tx) error {
		r, e := s.run(cc, tx, c.Body.View.SessionID, c.Body.View.RunID)
		if e != nil {
			return e
		}
		if r.View.CancelRequested || ctx.Err() != nil || terminal(r.View.Status) {
			return ErrConflict
		}
		limitBytes = min(limitBytes, max(0, (512<<10)-r.Body.ReadBytes))
		if limitBytes < 1 {
			return ErrQuota
		}
		stored, e := s.call(cc, tx, c.Body.View.SessionID, c.Body.View.ID)
		if e != nil {
			return e
		}
		if stored.Body.View.Status != Running {
			return ErrConflict
		}
		// Only the action dispatch needs approval; read-only effect verification can
		// finish after expiry. This check is after both SSH slot waits.
		if mutation(c.Body.View.ToolID) && mutationCommand(argv) {
			if e = s.consumedApproval(cc, tx, c.Body.View); e != nil {
				return e
			}
			if bound(stored.Body.View) != bound(c.Body.View) {
				return ErrApproval
			}
		}
		checkpoint := *c
		checkpoint.Body.Dispatched = true
		checkpoint.Body.View.Status = Running
		return s.saveCall(cc, tx, checkpoint)
	})
	if err != nil {
		if err == ErrQuota {
			b.truncated = true
			c.Body.ReadTruncated = true
		}
		return nil, err
	}
	c.Body.Dispatched = true
	if ctx.Err() != nil {
		return nil, ErrConflict
	}
	callCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	res, err := s.executor.ExecLimited(callCtx, snap, argv, target.ExecutionLimits{Bytes: limitBytes, Lines: b.lines})
	// Credentials may rotate while SSH is in flight. No returned stream may be
	// projected or persisted until the latest local secret set is available.
	if secretErr := s.registerSecrets(); secretErr != nil {
		return nil, secretErr
	}
	if res != nil {
		total := len(res.Stdout) + len(res.Stderr)
		lines := 0
		for _, text := range []string{res.Stdout, res.Stderr} {
			lines += strings.Count(text, "\n")
			if text != "" && !strings.HasSuffix(text, "\n") {
				lines++
			}
		}
		if total > limitBytes || lines > b.lines {
			c.Body.ReadTruncated = true
			return nil, ErrInvalid
		}
		b.bytes -= total
		b.lines -= lines
		b.truncated = b.truncated || res.Truncated
		c.Body.ReadBytes += total
		c.Body.ReadLines += lines
		c.Body.ReadTruncated = b.truncated
		// Mask while the original bounded streams are still intact, before any
		// projection/line clipping or persistence callback can consume a result.
		if res.Truncated {
			res.Stdout = s.masker.ScrubTruncated(res.Stdout)
			res.Stderr = s.masker.ScrubTruncated(res.Stderr)
		} else {
			res.Stdout = s.masker.Scrub(res.Stdout)
			res.Stderr = s.masker.Scrub(res.Stderr)
		}
		checkpointErr := s.persist(c.Body.View.SessionID, func(cc context.Context, tx *sql.Tx) error {
			r, e := s.run(cc, tx, c.Body.View.SessionID, c.Body.View.RunID)
			if e != nil {
				return e
			}
			r.Body.ReadBytes += total
			if e = s.saveRun(cc, tx, r); e != nil {
				return e
			}
			checkpoint := *c
			checkpoint.Body.View.Status = Running
			return s.saveCall(cc, tx, checkpoint)
		})
		if checkpointErr != nil {
			return res, checkpointErr
		}
	}
	if err != nil {
		return res, ErrUnavailable
	}
	if res == nil {
		return nil, ErrUnavailable
	}
	return res, nil
}
func (s *Service) resolveContainer(ctx context.Context, c *callRecord, b *readBudget) (string, error) {
	res, err := s.bounded(ctx, c, []string{"docker", "inspect", "--type", "container", "--format", "{{.Id}}", c.Body.View.Args.Container}, b)
	if err != nil || res == nil || res.ExitCode != 0 || res.Truncated {
		return "", ErrInvalid
	}
	id := strings.TrimSpace(res.Stdout)
	if !immutableContainer.MatchString(id) {
		return "", ErrInvalid
	}
	return id, nil
}
func (s *Service) prepareMutation(ctx context.Context, c callRecord) error {
	callCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	ctx = callCtx
	b := remainingReadBudget(c)
	v := &c.Body.View
	if err := s.beginCall(ctx, &c); err != nil {
		return err
	}
	if v.Args.Container != "" {
		id, err := s.resolveContainer(ctx, &c, b)
		if err != nil {
			v.Status = Failed
			v.Error = "container resolution failed"
			return s.recordResult(c)
		}
		v.Args.Container = id
		v.Object = id
		v.ArgsHash = digest(v.Args)
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	v.Status = AwaitingConfirmation
	return s.persist(v.SessionID, func(cc context.Context, tx *sql.Tx) error {
		r, e := s.run(cc, tx, v.SessionID, v.RunID)
		if e != nil {
			return e
		}
		if r.View.CancelRequested {
			return ErrConflict
		}
		if e = s.saveCall(cc, tx, c); e != nil {
			return e
		}
		return s.append(cc, tx, Entry{SessionID: v.SessionID, RunID: v.RunID, CallID: v.ID, Kind: "call_status", Status: v.Status}, false, false)
	})
}
func (s *Service) recordResult(c callRecord) error {
	v := &c.Body.View
	v.Truncated = v.Truncated || c.Body.ReadTruncated
	return s.persist(v.SessionID, func(ctx context.Context, tx *sql.Tx) error {
		// Charge result text exactly once. Events contain a call reference, never another copy.
		stored, e := s.call(ctx, tx, v.SessionID, v.ID)
		if e != nil {
			return e
		}
		if terminal(stored.Body.View.Status) {
			return ErrConflict
		}
		r, e := s.run(ctx, tx, v.SessionID, v.RunID)
		if e != nil {
			return e
		}
		// Recover a failed read-count checkpoint without charging a persisted one twice.
		r.Body.ReadBytes += max(0, c.Body.ReadBytes-stored.Body.ReadBytes)
		remaining := max(0, (512<<10)-r.Body.OutputBytes)
		v.Error = clip(v.Error, min(1024, remaining))
		if v.Resources != nil && resultBytes(*v) > remaining {
			v.Resources = nil
			v.Truncated = true
			v.Status = Failed
			v.Error = clip("resource output budget exhausted", remaining)
		}
		if len(v.Output)+len(v.Error) > remaining {
			v.Output = clip(v.Output, max(0, remaining-len(v.Error)))
			v.Truncated = true
		}
		r.Body.OutputBytes += resultBytes(*v)
		if e = s.saveRun(ctx, tx, r); e != nil {
			return e
		}
		if e = s.saveCall(ctx, tx, c); e != nil {
			return e
		}
		if _, e = tx.ExecContext(ctx, "UPDATE ops_chat_sessions SET body_bytes=body_bytes+? WHERE id=?", resultBytes(*v), v.SessionID); e != nil {
			return ErrStorage
		}
		return s.append(ctx, tx, Entry{SessionID: v.SessionID, RunID: v.RunID, CallID: v.ID, Kind: "call_status", Status: v.Status}, false, false)
	})
}
func (s *Service) executeCall(ctx context.Context, c callRecord) error {
	v := &c.Body.View
	b := remainingReadBudget(c)
	callCtx, stop := context.WithTimeout(ctx, 30*time.Second)
	defer stop()
	if err := s.beginCall(callCtx, &c); err != nil {
		return err
	}
	r, budgetErr := s.run(callCtx, s.db, v.SessionID, v.RunID)
	if budgetErr != nil {
		return budgetErr
	}
	b.bytes = min(b.bytes, (512<<10)-r.Body.OutputBytes-1024)
	if b.bytes < 1 {
		v.Status = Failed
		v.Truncated = true
		v.Error = "run output budget exhausted"
		return s.recordResult(c)
	}
	if v.Args.Container != "" && !mutation(v.ToolID) {
		id, err := s.resolveContainer(callCtx, &c, b)
		if err != nil {
			v.Status = Failed
			v.Error = "container resolution failed"
			return s.recordResult(c)
		}
		v.Args.Container = id
		v.Object = id
		v.ArgsHash = digest(v.Args)
	}
	err := error(nil)
	if mutation(v.ToolID) {
		if s.audit == nil {
			v.Status = Failed
			v.Error = "local audit unavailable"
			return s.recordResult(c)
		}
		// Outside DB transactions; the injected recorder only writes the local append-only log.
		err = s.audit.Record(callCtx, audit.Entry{Actor: "admin", Action: "ops_chat_intent", TargetType: "ops_chat_call", TargetID: v.ID,
			Detail: map[string]any{"sessionId": v.SessionID, "runId": v.RunID, "callId": v.ID, "serverId": v.ServerID, "toolId": v.ToolID, "argsHash": v.ArgsHash, "targetHash": v.TargetHash, "status": "intent"}})
		if err != nil {
			v.Status = Failed
			v.Error = "local audit failed"
			return s.recordResult(c)
		}
		// Audit can block; recheck cancellation, lease and approval immediately before dispatch.
		err = s.persist(v.SessionID, func(cc context.Context, tx *sql.Tx) error {
			r, e := s.run(cc, tx, v.SessionID, v.RunID)
			if e != nil {
				return e
			}
			if r.View.CancelRequested || callCtx.Err() != nil {
				return ErrConflict
			}
			return s.consumedApproval(cc, tx, *v)
		})
		if err != nil {
			v.Status = Interrupted
			v.Error = "dispatch blocked"
			return s.recordResult(c)
		}
	}
	// Capture again outside the transaction. Never fall back to the old unbounded Exec.
	current, e := s.executor.Capture(callCtx, v.ServerID)
	if e != nil || current.Fingerprint() != v.TargetHash {
		v.Status = Failed
		v.Error = "target configuration changed"
		return s.recordResult(c)
	}
	v.Status = Succeeded
	exec := s.bounded
	if mutation(v.ToolID) {
		release, slotErr := s.takeSlot(callCtx, v.ServerID)
		if slotErr != nil {
			v.Status = Interrupted
			v.Error = "dispatch blocked"
			return s.recordResult(c)
		}
		defer release()
		exec = s.boundedInSlot
	}
	if v.ToolID == "host_resources" {
		v.Resources = &HostResources{Unavailable: []string{}}
	}
	for _, argv := range commands(v.ToolID, v.Args) {
		res, e := exec(callCtx, &c, argv, b)
		if e != nil {
			if v.Resources != nil && callCtx.Err() == nil && e != ErrLease && e != ErrStorage && e != ErrConflict {
				v.Resources.collect(resourcePart(argv), "", false)
				continue
			}
			v.Status = Failed
			if mutation(v.ToolID) && c.Body.Dispatched {
				v.Status = Unknown
			} else if mutation(v.ToolID) {
				v.Status = Interrupted
			}
			v.Error = "bounded execution failed"
			break
		}
		now := time.Now().UTC()
		v.CollectedAt = &now
		exit := res.ExitCode
		v.ExitCode = &exit
		stdout := res.Stdout
		if v.ToolID == "host_processes" {
			lines := strings.Split(stdout, "\n")
			if len(lines) > 31 {
				stdout = strings.Join(lines[:31], "\n")
				b.truncated = true
			}
		}
		if v.ToolID == "host_resources" {
			v.Resources.collect(resourcePart(argv), s.scrub(stdout, 64<<10), exit == 0 && !res.Truncated)
			continue
		}
		v.Output += s.scrub(stdout, b.bytes+len(stdout))
		if res.Stderr != "" {
			v.Output += "\n" + s.scrub(res.Stderr, 64<<10)
		}
		v.Truncated = b.truncated
		if exit != 0 {
			v.Status = Failed
			if mutation(v.ToolID) {
				v.Status = Unknown
			}
			v.Error = "remote command exited nonzero"
			break
		}
	}
	if v.Resources != nil {
		if len(v.Resources.Unavailable) == 3 {
			v.Status = Failed
			v.Error = "resource metrics unavailable"
		} else if len(v.Resources.Unavailable) > 0 {
			v.Status = PartialFailed
			v.Error = "some resource metrics unavailable"
		}
		encoded, _ := json.Marshal(v.Resources)
		if len(encoded) > 64<<10 {
			v.Resources = nil
			v.Status = Failed
			v.Error = "resource projection exceeded output budget"
			v.Truncated = true
		}
	}
	if mutation(v.ToolID) && c.Body.Dispatched && v.ExitCode != nil && callCtx.Err() == nil {
		if s.verifyMutation(callCtx, &c, b) {
			v.Status = Succeeded
			v.Error = ""
		} else {
			v.Status = Unknown
			v.Error = "remote effect not verified"
		}
	}
	v.Output = s.scrub(v.Output, 64<<10)
	v.Truncated = v.Truncated || b.truncated || c.Body.ReadTruncated || len(v.Output) >= 64<<10
	if callCtx.Err() != nil && mutation(v.ToolID) && c.Body.Dispatched {
		v.Status = Unknown
	}
	if v.CollectedAt == nil {
		now := time.Now().UTC()
		v.CollectedAt = &now
	}
	return s.recordResult(c)
}
func (s *Service) verifyMutation(ctx context.Context, c *callRecord, b *readBudget) bool {
	v := c.Body.View
	if v.ToolID == "docker_action" {
		res, err := s.boundedInSlot(ctx, c, []string{"docker", "inspect", "--type", "container", "--format", "{{json .State}}", v.Object}, b)
		if err != nil || res.ExitCode != 0 || res.Truncated {
			return false
		}
		var state struct {
			Running *bool
			Paused  *bool
		}
		if json.Unmarshal([]byte(res.Stdout), &state) != nil || state.Running == nil || state.Paused == nil {
			return false
		}
		if v.Args.Action == "restart" && (v.ExitCode == nil || *v.ExitCode != 0) {
			return false
		}
		switch v.Args.Action {
		case "start", "restart":
			return *state.Running && !*state.Paused
		case "stop":
			return !*state.Running
		case "pause":
			return *state.Paused
		case "unpause":
			return !*state.Paused
		}
	}
	res, err := s.boundedInSlot(ctx, c, []string{"systemctl", "show", v.Object, "--property=ActiveState", "--value"}, b)
	if err != nil || res.ExitCode != 0 || res.Truncated {
		return false
	}
	state := strings.TrimSpace(res.Stdout)
	if v.Args.Action == "restart" && (v.ExitCode == nil || *v.ExitCode != 0) {
		return false
	}
	if v.Args.Action == "stop" {
		return state == "inactive"
	}
	return state == "active"
}
