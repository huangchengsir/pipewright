package opschat

import (
	"context"
	"database/sql"
	"encoding/json"
	"sort"
	"strconv"
	"time"
)

func (s *Service) requestPlan(ctx context.Context, r runRecord, continuation bool) (Plan, []callRecord, error) {
	var plan Plan
	if s.model == nil || s.model.Info() != r.Body.Provider {
		return plan, nil, ErrConsent
	}
	messages, err := s.contextMessages(ctx, r.View.SessionID)
	if err != nil {
		return plan, nil, err
	}
	facts, err := s.containerObservations(ctx, r.View.SessionID, r.Body.Targets)
	if err != nil {
		return plan, nil, err
	}
	aliases := make([]string, len(r.Body.Targets))
	for i := range aliases {
		aliases[i] = "target_" + strconv.Itoa(i)
	}
	req := PlanRequest{Question: s.scrub(r.Body.Question, 8<<10), Messages: messages, Targets: aliases, Tools: Tools(), MaxResponseBytes: 32 << 10, Provider: r.Body.Provider, ContainerFacts: facts, Continuation: continuation}
	currentUser := -1
	for i, message := range req.Messages {
		if message.Role == "user" {
			currentUser = i
		}
	}
	for {
		encoded, err := json.Marshal(req)
		if err != nil {
			return plan, nil, ErrInvalid
		}
		if len(encoded) <= 32<<10 {
			break
		}
		if len(req.Messages) <= 1 || currentUser == 0 {
			return plan, nil, ErrQuota
		}
		req.Messages = req.Messages[1:]
		currentUser--
	}
	// Persist the one-shot boundary before contacting the provider. Recovery never replans.
	err = s.persist(r.View.SessionID, func(c context.Context, tx *sql.Tx) error {
		current, e := s.run(c, tx, r.View.SessionID, r.View.ID)
		if e != nil {
			return e
		}
		if current.View.CancelRequested || terminal(current.View.Status) || ctx.Err() != nil {
			return ErrConflict
		}
		if continuation {
			if !current.Body.NeedsDiscovery || current.Body.ContinuationStarted || current.Body.ModelCalls != 1 {
				return ErrConflict
			}
			current.Body.ContinuationStarted = true
			current.View.Status = Planning
		} else if current.Body.ModelCalls != 0 {
			return ErrConflict
		}
		current.Body.ModelCalls++
		if e = s.saveRun(c, tx, current); e != nil {
			return e
		}
		if continuation {
			return s.append(c, tx, Entry{SessionID: r.View.SessionID, RunID: r.View.ID, Kind: "run_status", Status: Planning}, false, false)
		}
		return nil
	})
	if err != nil {
		return plan, nil, err
	}
	if err = ctx.Err(); err != nil {
		return plan, nil, err
	}
	modelCtx, stop := context.WithTimeout(ctx, 60*time.Second)
	defer stop()
	reply := s.reply(modelCtx, r.View.SessionID, r.View.ID, "assistant", req.MaxResponseBytes)
	if streaming, ok := s.model.(StreamModel); ok {
		plan, err = streaming.PlanStream(modelCtx, req, reply.chunk)
	} else {
		plan, err = s.model.Plan(modelCtx, req)
	}
	if err == nil {
		err = s.registerSecrets()
	}
	var candidates []callRecord
	if err == nil {
		r.Body.ContinuationStarted = continuation
		candidates, err = s.planCalls(r, plan)
	}
	if err == nil && continuation {
		var previous []callRecord
		previous, err = s.calls(ctx, s.db, r.View.SessionID, r.View.ID)
		if err == nil && len(previous)+len(candidates) > resourceLimits.Calls {
			err = ErrQuota
		}
	}
	if err == nil && reply.seq != 0 {
		err = reply.finish(s.planText(plan), Succeeded, len(plan.Actions) == 0)
	}
	if err != nil && reply.seq != 0 {
		_ = reply.finish(reply.text, Interrupted, false)
	}
	if reply.seq != 0 {
		plan.Text, plan.ManualAdvice = "", ""
	}
	return plan, candidates, err
}

func (s *Service) publishContinuation(ctx context.Context, r runRecord, plan Plan, candidates []callRecord) error {
	readOnly := true
	for _, c := range candidates {
		readOnly = readOnly && !mutation(c.Body.View.ToolID)
	}
	if readOnly {
		order := map[string]int{}
		for i, snap := range r.Body.Targets {
			order[snap.Server.ID] = i
		}
		sort.SliceStable(candidates, func(i, j int) bool {
			return order[candidates[i].Body.View.ServerID] < order[candidates[j].Body.View.ServerID]
		})
	}
	return s.persist(r.View.SessionID, func(c context.Context, tx *sql.Tx) error {
		current, err := s.run(c, tx, r.View.SessionID, r.View.ID)
		if err != nil {
			return err
		}
		if ctx.Err() != nil || current.View.CancelRequested || terminal(current.View.Status) || !current.Body.ContinuationStarted || current.Body.ModelCalls != 2 || plan.NeedsDiscovery {
			return ErrConflict
		}
		previous, err := s.calls(c, tx, r.View.SessionID, r.View.ID)
		if err != nil {
			return err
		}
		if len(previous)+len(candidates) > resourceLimits.Calls {
			return ErrQuota
		}
		for _, call := range previous {
			if call.Body.View.ToolID != "docker_containers" || call.Body.View.Status != Succeeded {
				return ErrConflict
			}
		}
		text := s.planText(plan)
		if text != "" {
			if err = s.append(c, tx, Entry{SessionID: r.View.SessionID, RunID: r.View.ID, Kind: "assistant", Text: text}, true, len(plan.Actions) == 0); err != nil {
				return err
			}
		}
		for i := range candidates {
			candidate := &candidates[i]
			candidate.Ordinal = len(previous) + i
			body, err := s.seal(candidate.Body)
			if err != nil {
				return err
			}
			v := candidate.Body.View
			if _, err = tx.ExecContext(c, `INSERT INTO ops_chat_calls(id,session_id,run_id,server_id,tool_id,status,args_hash,target_hash,body,created_at,ordinal) VALUES(?,?,?,?,?,?,?,?,?,?,?)`, v.ID, v.SessionID, v.RunID, v.ServerID, v.ToolID, v.Status, v.ArgsHash, v.TargetHash, body, candidate.Created, candidate.Ordinal); err != nil {
				return ErrStorage
			}
			if err = s.append(c, tx, Entry{SessionID: v.SessionID, RunID: v.RunID, CallID: v.ID, Kind: "tool_call"}, true, false); err != nil {
				return err
			}
		}
		current.Body.NeedsDiscovery = false
		current.View.Status = Running
		if err = s.saveRun(c, tx, current); err != nil {
			return err
		}
		return s.append(c, tx, Entry{SessionID: r.View.SessionID, RunID: r.View.ID, Kind: "run_status", Status: Running}, false, false)
	})
}

func (s *Service) planText(plan Plan) string {
	text := plan.Text
	if plan.ManualAdvice != "" {
		text += "\n" + plan.ManualAdvice
	}
	return s.scrub(text, 32<<10)
}
