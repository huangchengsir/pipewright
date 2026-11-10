package kubernetes

import (
	"context"
	"encoding/json"
	"github.com/huangchengsir/pipewright/internal/configstate"
)

// ReleaseConfigOptions reads configured metadata only. It neither opens a
// protected credential nor contacts Kubernetes, Git, registries or executors.
func (s *Service) ReleaseConfigOptions(ctx context.Context, project string) ([]map[string]any, error) {
	if !validID(project) {
		return nil, ErrInvalid
	}
	q := configstate.Queryer(ctx, s.db)
	rows, e := q.QueryContext(ctx, `SELECT g.grant_json,c.connection_json,COALESCE(p.enabled,0) FROM k8s_release_grants g JOIN k8s_connections c ON c.id=g.connection_id LEFT JOIN k8s_release_profiles p ON p.connection_id=c.id WHERE g.project_id=? ORDER BY g.id LIMIT 4097`, project)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []map[string]any{}
	count := 0
	for rows.Next() {
		var raw, conn string
		var enabled bool
		if rows.Scan(&raw, &conn, &enabled) != nil {
			return nil, ErrInvalid
		}
		var g ReleaseGrant
		var c Connection
		if json.Unmarshal([]byte(raw), &g) != nil || json.Unmarshal([]byte(conn), &c) != nil || g.ProjectID != project {
			return nil, ErrInvalid
		}
		for _, a := range g.Resources {
			count++
			if count > 4096 {
				return nil, ErrInvalid
			}
			out = append(out, map[string]any{"kind": "resourceGrant", "grantId": g.ID, "connectionId": g.ConnectionID, "credentialId": c.CredentialID, "context": c.Context, "environment": g.Environment, "executor": g.Executor, "enabled": g.Enabled && c.Enabled && enabled, "resource": a.ID, "actions": a.Actions, "containers": a.Containers, "fieldCount": len(a.Fields), "fieldPathsWithheld": len(a.Fields) > 0, "requireApproval": g.RequireApproval, "daemonSetRevisionReadGranted": releaseDaemonID(a.ID) && releaseStringSetContains(a.Actions, []string{releaseDaemonRevisionAction})})
		}
	}
	return out, rows.Err()
}
