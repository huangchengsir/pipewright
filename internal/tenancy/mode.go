// Package tenancy owns durable team identities and mode transitions. Domain
// adapters must additionally enforce authz before exposing repository operations.
package tenancy

import (
	"errors"
	"github.com/huangchengsir/pipewright/internal/config"
)

var (
	ErrExplicitModeRequired = errors.New("tenancy: previously enabled instances require explicit PIPEWRIGHT_MULTI_TENANT=true or false")
	ErrUnfinishedOperations = errors.New("tenancy: mode change requires all operations and external receipts to finish")
	ErrNotReady             = errors.New("tenancy: authorization coverage is not ready for activation")
	ErrConflict             = errors.New("tenancy: state changed; reload before retry")
)

type ModeState struct {
	Enabled, EverEnabled bool
	Epoch, Revision      uint64
}

// PlanMode does not write or cancel anything. The caller must hold the durable
// controller fence through transition, session revocation and asset mapping.
func PlanMode(current ModeState, requested config.MultiTenantMode, quiescent, coverageReady bool) (ModeState, error) {
	if current.EverEnabled && !requested.Explicit {
		return current, ErrExplicitModeRequired
	}
	if current.Epoch == 0 || current.Revision == 0 {
		return current, ErrConflict
	}
	if requested.Enabled && !coverageReady {
		return current, ErrNotReady
	}
	if requested.Enabled == current.Enabled {
		return current, nil
	}
	if !quiescent {
		return current, ErrUnfinishedOperations
	}
	if requested.Enabled && !coverageReady {
		return current, ErrNotReady
	}
	current.Enabled = requested.Enabled
	current.EverEnabled = current.EverEnabled || requested.Enabled
	current.Epoch++
	current.Revision++
	if current.Epoch == 0 || current.Revision == 0 {
		return ModeState{}, ErrConflict
	}
	return current, nil
}
