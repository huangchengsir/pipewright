package trigger

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
)

// ResolveExistingEnv uses the existing first-match rule without creating defaults,
// reading webhook secrets, or collapsing storage/decoding errors into no mapping.
func ResolveExistingEnv(ctx context.Context, db *sql.DB, projectID, branch string) (string, []string, error) {
	if strings.TrimSpace(branch) == "" {
		return "", nil, nil
	}
	var raw string
	err := db.QueryRowContext(ctx, `SELECT branch_mappings_json FROM pipeline_triggers WHERE project_id = ?`, projectID).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil, nil
	}
	if err != nil {
		return "", nil, err
	}
	if strings.TrimSpace(raw) == "" {
		return "", nil, nil
	}
	var mappings []BranchMapping
	if err := json.Unmarshal([]byte(raw), &mappings); err != nil {
		return "", nil, err
	}
	mapping, ok := matchBranch(branch, mappings)
	if !ok {
		return "", nil, nil
	}
	return mapping.Environment, mapping.TargetServerIDs, nil
}
