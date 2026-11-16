package tenancy

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"github.com/huangchengsir/pipewright/internal/configstate"
)

// ConfirmationBinding binds a private interactive confirmation to its initiating
// verified session. It is a comparison digest, never a bearer capability.
func ConfirmationBinding(ctx context.Context, db *sql.DB) (string, error) {
	on, err := modeEnabled(ctx, configstate.Queryer(ctx, db))
	if err != nil || !on {
		return "", err
	}
	_, request, err := authorityForRequest(ctx, db)
	if err != nil {
		return "", err
	}
	if request.actor.SessionHash == "" || request.serviceRunID != "" {
		return "", ErrForbidden
	}
	raw, _ := json.Marshal([]any{request.teamID, request.actor.UserID, request.actor.SessionHash, request.actor.ModeEpoch, request.actor.SecurityGeneration})
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}
