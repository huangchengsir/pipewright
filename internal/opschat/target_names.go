package opschat

import (
	"context"
	"strconv"
)

type entryTargetKey struct {
	run, kind string
}

func validTargetAlias(alias string) bool {
	return len(alias) == 8 && alias[:7] == "target_" &&
		(alias[7] >= '0' && alias[7] <= '7' || alias[7] >= 'A' && alias[7] <= 'H')
}

func (s *Service) entryTargetNames(ctx context.Context, q querier, session string, key entryTargetKey) (map[string]string, error) {
	r, err := s.run(ctx, q, session, key.run)
	if err == ErrNotFound {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	names := map[string]string{}
	if key.kind == "assistant" {
		if len(r.Body.Targets) > 8 {
			return nil, nil
		}
		for i, snap := range r.Body.Targets {
			if snap.Server.ID != "" && snap.Server.Name != "" {
				names["target_"+strconv.Itoa(i)] = s.scrub(snap.Server.Name, 1024)
			}
		}
		return names, nil
	}
	if key.kind != "analysis" {
		return nil, nil
	}
	calls := []Call{}
	if r.Body.Analysis != nil && !r.Body.AnalysisStarted {
		ids := r.Body.Analysis.CallIDs
		if len(ids) > 24 {
			return nil, nil
		}
		seen := map[string]bool{}
		for _, id := range ids {
			if !validID(id) || seen[id] {
				return nil, nil
			}
			seen[id] = true
			c, err := s.call(ctx, q, session, id)
			if err == ErrNotFound {
				// An unknown server would shift subsequent aliases; never guess its position.
				return nil, nil
			}
			if err != nil {
				return nil, err
			}
			calls = append(calls, c.Body.View)
		}
	} else {
		rows, err := q.QueryContext(ctx, "SELECT body FROM ops_chat_calls WHERE session_id=? AND run_id=? ORDER BY ordinal LIMIT 25", session, key.run)
		if err != nil {
			return nil, ErrStorage
		}
		defer rows.Close()
		for rows.Next() {
			if len(calls) == 24 {
				return nil, nil
			}
			var raw []byte
			var c callBody
			if rows.Scan(&raw) != nil {
				return nil, ErrStorage
			}
			if err = s.open(raw, &c); err != nil {
				return nil, err
			}
			calls = append(calls, c.View)
		}
		if rows.Err() != nil {
			return nil, ErrStorage
		}
	}
	aliases := map[string]string{}
	for _, call := range calls {
		if call.ServerID == "" {
			return nil, nil
		}
		if _, seen := aliases[call.ServerID]; seen {
			continue
		}
		if len(aliases) == 8 {
			return nil, nil
		}
		alias := "target_" + string(rune('A'+len(aliases)))
		aliases[call.ServerID] = alias
		if call.ServerName != "" {
			names[alias] = s.scrub(call.ServerName, 1024)
		}
	}
	return names, nil
}
