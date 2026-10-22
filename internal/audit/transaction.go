package audit

import (
	"context"
	"encoding/json"
	"github.com/google/uuid"
	"github.com/huangchengsir/pipewright/internal/configstate"
	"github.com/huangchengsir/pipewright/internal/mask"
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
	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, err = q.ExecContext(ctx, `INSERT INTO audit_log(id,timestamp,actor,action,target_type,target_id,detail_json,ip,created_at) VALUES(?,?,?,?,?,?,?,?,?)`, uuid.NewString(), now, e.Actor, e.Action, e.TargetType, e.TargetID, string(detail), nil, now)
	return err
}
