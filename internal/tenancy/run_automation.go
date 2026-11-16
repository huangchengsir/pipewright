package tenancy

import (
	"context"
	"encoding/json"
	"github.com/huangchengsir/pipewright/internal/authz"
	"github.com/huangchengsir/pipewright/internal/configstate"
	"strings"
)

type runAutomationReference struct {
	Kind     string `json:"kind"`
	ID       string `json:"id"`
	Approval string `json:"approval"`
	Digest   string `json:"digest"`
}
type runPermissionEnvelope struct {
	Version     int                     `json:"version"`
	Permissions []ExecutionPermission   `json:"permissions"`
	Automation  *runAutomationReference `json:"automation,omitempty"`
}

func decodeRunPermissions(body string) (runPermissionEnvelope, error) {
	var out runPermissionEnvelope
	if len(body) > 131072 {
		return out, ErrForbidden
	}
	// Earlier rows in the same migration used the plain permission array.
	if strings.HasPrefix(strings.TrimSpace(body), "[") {
		out.Version = 1
		if json.Unmarshal([]byte(body), &out.Permissions) != nil {
			return out, ErrForbidden
		}
	} else if json.Unmarshal([]byte(body), &out) != nil {
		return out, ErrForbidden
	}
	if out.Version != 1 || len(out.Permissions) == 0 || len(out.Permissions) > 512 {
		return out, ErrForbidden
	}
	if out.Automation != nil && (!validAutomationSource(out.Automation.Kind, out.Automation.ID, out.Automation.Digest) || out.Automation.Approval == "") {
		return out, ErrForbidden
	}
	return out, nil
}

// A queued run remains bound to the exact automation approval that admitted it.
// Saving, disabling or deleting that source rotates/revokes this row atomically;
// a newly approved source must not resurrect runs admitted under its predecessor.
func validateRunAutomation(ctx context.Context, q configstate.DBTX, run runAuthorization, parent *runAutomationReference) error {
	var body, project string
	err := q.QueryRowContext(ctx, `SELECT permissions_json,project_id FROM access_automation_authorizations WHERE source_kind=? AND source_id=? AND approval_id=? AND config_digest=? AND user_id=? AND tenant_id=? AND mode_epoch=? AND user_generation=? AND catalog_version=? AND revoked_at IS NULL`, parent.Kind, parent.ID, parent.Approval, parent.Digest, run.user, run.team, run.epoch, run.generation, authz.CatalogVersion).Scan(&body, &project)
	var envelope automationEnvelope
	if err != nil || len(body) > 131072 || json.Unmarshal([]byte(body), &envelope) != nil {
		return ErrForbidden
	}
	if err = verifyAutomationResourceVersions(ctx, q, envelope.ResourceVersions); err != nil {
		return err
	}
	if err = verifyAutomationPlans(ctx, q, project, envelope); err != nil {
		return err
	}

	return nil
}

// DecodeExecutionPermissions reads stored ceilings for trusted redaction/indexing.
// It does not validate a live principal and must never authorize an operation.
func DecodeExecutionPermissions(body string) ([]ExecutionPermission, error) {
	envelope, err := decodeRunPermissions(body)
	return envelope.Permissions, err
}
