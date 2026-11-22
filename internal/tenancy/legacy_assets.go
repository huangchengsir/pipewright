package tenancy

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/google/uuid"
	"github.com/huangchengsir/pipewright/internal/configstate"
)

// Only explicit storage roots are backfilled. Child rows retain their existing
// foreign keys and must resolve authorization through that root, not through a
// caller-provided team. Legacy owners remain unknown rather than impersonating
// the administrator performing the upgrade.
var legacyRoots = []struct{ table, kind string }{
	{"workspace_business_projects", "business_project"}, {"workspace_products", "product"},
	{"projects", "project"}, {"credentials", "credential"}, {"servers", "server"},
	{"pipeline_templates", "template"}, {"variable_groups", "variable_group"}, {"custom_nodes", "custom_node"},
	{"k8s_connections", "kubernetes"}, {"k8s_credentials", "kubernetes_credential"},
	{"notification_channels", "notification_channel"}, {"dns_providers", "dns_provider"},
	{"proxy_routes", "proxy_route"}, {"anomaly_rules", "anomaly_rule"},
	{"ops_chat_sessions", "ops_chat"}, {"audit_log", "audit"},
}

func mapLegacyAssets(ctx context.Context, q configstate.DBTX, defaultTeam, bootstrap string) error {
	for _, root := range legacyRoots {
		// Identifiers are fixed literals above, never request values.
		rows, err := q.QueryContext(ctx, `SELECT id FROM `+root.table)
		if err != nil {
			return err
		}
		var ids []string
		for rows.Next() {
			var id string
			if err = rows.Scan(&id); err != nil {
				rows.Close()
				return err
			}
			ids = append(ids, id)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		for _, id := range ids {
			projectID := ""
			if root.kind == "project" {
				projectID = id
			}
			if err = mapLegacyRoot(ctx, q, root.kind, id, defaultTeam, projectID, ""); err != nil {
				return err
			}
			if root.kind == "ops_chat" {
				if _, err = q.ExecContext(ctx, `UPDATE access_ownership SET owner_id=? WHERE resource_kind='ops_chat' AND resource_id=? AND tenant_id=? AND (owner_id IS NULL OR owner_id='')`, bootstrap, id, defaultTeam); err != nil {
					return err
				}
			}
			if root.kind == "template" || root.kind == "variable_group" || root.kind == "custom_node" {
				var team string
				if err = q.QueryRowContext(ctx, `SELECT tenant_id FROM access_ownership WHERE resource_kind=? AND resource_id=?`, root.kind, id).Scan(&team); err != nil {
					return err
				}
				if _, err = q.ExecContext(ctx, "UPDATE "+root.table+" SET name_scope=? WHERE id=?", team, id); err != nil {
					return err
				}
			}
		}
	}
	// The existing environment chain is descriptive, not proof of deployment
	// sensitivity. All imported environments start as other, even named dev/test.
	rows, err := q.QueryContext(ctx, `SELECT project_id,chain_json FROM project_environments`)
	if err != nil {
		return err
	}
	type chain struct{ project, body string }
	var chains []chain
	for rows.Next() {
		var v chain
		if err = rows.Scan(&v.project, &v.body); err != nil {
			rows.Close()
			return err
		}
		chains = append(chains, v)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, v := range chains {
		var items []struct {
			Name string `json:"name"`
		}
		if json.Unmarshal([]byte(v.body), &items) != nil {
			return ErrConflict
		}
		for _, item := range items {
			if _, err = legacyEnvironment(ctx, q, v.project, item.Name); err != nil {
				return err
			}
		}
	}
	rows, err = q.QueryContext(ctx, `SELECT id,project_id,resolved_environment FROM pipeline_runs`)
	if err != nil {
		return err
	}
	type run struct{ id, project, environment string }
	var runs []run
	for rows.Next() {
		var v run
		if err = rows.Scan(&v.id, &v.project, &v.environment); err != nil {
			rows.Close()
			return err
		}
		runs = append(runs, v)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, v := range runs {
		var team string
		if err = q.QueryRowContext(ctx, `SELECT tenant_id FROM access_ownership WHERE resource_kind='project' AND resource_id=?`, v.project).Scan(&team); err != nil {
			return err
		}
		envID := ""
		if v.environment != "" {
			envID, err = legacyEnvironment(ctx, q, v.project, v.environment)
			if err != nil {
				return err
			}
		}
		if err = mapLegacyRoot(ctx, q, "run", v.id, team, v.project, envID); err != nil {
			return err
		}
	}
	rows, err = q.QueryContext(ctx, `SELECT p.id,p.project_id,o.tenant_id FROM preview_envs p JOIN access_ownership o ON o.resource_kind='project' AND o.resource_id=p.project_id`)
	if err != nil {
		return err
	}
	type preview struct{ id, project, team string }
	var previews []preview
	for rows.Next() {
		var v preview
		if err = rows.Scan(&v.id, &v.project, &v.team); err != nil {
			rows.Close()
			return err
		}
		previews = append(previews, v)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, v := range previews {
		if err = mapLegacyRoot(ctx, q, "preview", v.id, v.team, v.project, ""); err != nil {
			return err
		}
	}
	return nil
}
func mapLegacyRoot(ctx context.Context, q configstate.DBTX, kind, id, team, project, environment string) error {
	var existing string
	err := q.QueryRowContext(ctx, `SELECT tenant_id FROM access_ownership WHERE resource_kind=? AND resource_id=?`, kind, id).Scan(&existing)
	if err == nil {
		return nil
	} // Never reassign an existing team on a later transition.
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	var projectArg, envArg any
	if project != "" {
		projectArg = project
	}
	if environment != "" {
		envArg = environment
	}
	_, err = q.ExecContext(ctx, `INSERT INTO access_ownership(resource_kind,resource_id,tenant_id,owner_id,project_id,environment_id) VALUES(?,?,?,NULL,?,?)`, kind, id, team, projectArg, envArg)
	return err
}
func legacyEnvironment(ctx context.Context, q configstate.DBTX, project, name string) (string, error) {
	if !validName(name) {
		return "", ErrConflict
	}
	var team string
	if err := q.QueryRowContext(ctx, `SELECT tenant_id FROM access_ownership WHERE resource_kind='project' AND resource_id=?`, project).Scan(&team); err != nil {
		return "", err
	}
	var id string
	err := q.QueryRowContext(ctx, `SELECT id FROM access_environments WHERE tenant_id=? AND project_id=? AND name=?`, team, project, name).Scan(&id)
	if err == nil {
		return id, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return "", err
	}
	id = uuid.NewString()
	if _, err = q.ExecContext(ctx, `INSERT INTO access_environments(id,tenant_id,project_id,name,purpose,revision) VALUES(?,?,?,?,'other',1)`, id, team, project, name); err != nil {
		return "", err
	}
	if err = mapLegacyRoot(ctx, q, "environment", id, team, project, id); err != nil {
		return "", err
	}
	return id, nil
}
