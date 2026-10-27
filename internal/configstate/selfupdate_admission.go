package configstate

import (
	"context"
	"errors"
)

var ErrMaintenance = errors.New("configstate: system installation in progress")

// This check is admission only. Completion and cleanup must never call it.
func CheckMaintenanceAdmission(ctx context.Context, q DBTX) error {
	if _, err := q.ExecContext(ctx, "UPDATE selfupdate_control SET task_id=task_id WHERE id=1"); err != nil {
		return err
	}
	var task string
	if err := q.QueryRowContext(ctx, "SELECT task_id FROM selfupdate_control WHERE id=1").Scan(&task); err != nil {
		return err
	}
	if task != "" {
		return ErrMaintenance
	}
	return nil
}
