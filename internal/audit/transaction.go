package audit

import (
	"context"
	"encoding/json"
	"github.com/google/uuid"
	"github.com/huangchengsir/pipewright/internal/configstate"
	"github.com/huangchengsir/pipewright/internal/mask"
	"github.com/huangchengsir/pipewright/internal/tenancy"
	"time"
)

// AppendLocalTx is a strict transaction participant. It never calls remote
// sinks, and a local failure aborts the caller's configuration transaction.
func AppendLocalTx(ctx context.Context, q configstate.DBTX, m *mask.Masker, e Entry) error {
	if m == nil {
		return configstate.ErrInvalid
	}
	detail, err := json.Marshal(m.ScrubMap(e.Detail))
	if err != nil {
		return err
	}
	id := uuid.NewString()
	actor, err := tenancy.AppendAuditOwnership(ctx, q, id, e.TargetType, e.TargetID)
	if err != nil {
		return err
	}
	if actor != "" {
		e.Actor = actor
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, err = q.ExecContext(ctx, `INSERT INTO audit_log(id,timestamp,actor,action,target_type,target_id,detail_json,ip,created_at) VALUES(?,?,?,?,?,?,?,?,?)`, id, now, e.Actor, e.Action, e.TargetType, e.TargetID, string(detail), nil, now)
	return err
}
