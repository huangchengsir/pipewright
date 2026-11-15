package tenancy

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"github.com/huangchengsir/pipewright/internal/configstate"
)

// ProjectExecutionDigest excludes unrelated scheduling/chain metadata, but
// includes every persisted project input consumed by pipeline preparation.
// Only a digest is retained; no configuration values are logged or returned.
func ProjectExecutionDigest(ctx context.Context, q configstate.DBTX, project string) (string, error) {
	queries := []string{
		`SELECT repo_url,default_branch,credential_id,pac_enabled FROM projects WHERE id=?`,
		`SELECT spec_json FROM pipeline_configs WHERE project_id=?`,
		`SELECT build_json,environments_json,steps_json FROM pipeline_settings WHERE project_id=?`,
		`SELECT branch_mappings_json FROM pipeline_triggers WHERE project_id=?`,
		`SELECT runner_server_id FROM project_runners WHERE project_id=?`,
		`SELECT defs_json FROM project_parameters WHERE project_id=?`,
		`SELECT chain_json FROM project_environments WHERE project_id=?`,
		`SELECT environment,var_key,value,is_secret,credential_id FROM environment_variables WHERE project_id=? ORDER BY environment,var_key`,
		`SELECT enabled,dns_provider_id,base_domain FROM preview_configs WHERE project_id=?`,
	}
	dataset := [][][]*string{}
	for i, query := range queries {
		rows, err := q.QueryContext(ctx, query, project)
		if err != nil {
			return "", err
		}
		columns, err := rows.Columns()
		if err != nil {
			rows.Close()
			return "", err
		}
		records := [][]*string{}
		for rows.Next() {
			values := make([]sql.NullString, len(columns))
			dest := make([]any, len(columns))
			for j := range values {
				dest[j] = &values[j]
			}
			if err = rows.Scan(dest...); err != nil {
				rows.Close()
				return "", err
			}
			record := make([]*string, len(values))
			for j, v := range values {
				if v.Valid {
					x := v.String
					record[j] = &x
				}
			}
			records = append(records, record)
			if len(records) > 1024 {
				rows.Close()
				return "", ErrInvalidInput
			}
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return "", err
		}
		if i == 0 && len(records) != 1 {
			return "", ErrForbidden
		}
		dataset = append(dataset, records)
	}
	encoded, err := json.Marshal(dataset)
	if err != nil || len(encoded) > 4<<20 {
		return "", ErrInvalidInput
	}
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:]), nil
}
func verifyAutomationPlans(ctx context.Context, q configstate.DBTX, project string, e automationEnvelope) error {
	if e.ProjectDigest != "" {
		digest, err := ProjectExecutionDigest(ctx, q, project)
		if err != nil || digest != e.ProjectDigest {
			return ErrForbidden
		}
	}
	if e.SourceProject != "" {
		digest, err := ProjectExecutionDigest(ctx, q, e.SourceProject)
		if err != nil || digest != e.SourceDigest {
			return ErrForbidden
		}
	}
	return nil
}
