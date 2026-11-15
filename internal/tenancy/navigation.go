package tenancy

import (
	"context"
	"github.com/huangchengsir/pipewright/internal/authz"
)

type Navigation struct {
	UserID         string   `json:"userId"`
	TeamID         string   `json:"teamId"`
	ModeEpoch      uint64   `json:"modeEpoch"`
	Revision       uint64   `json:"revision"`
	CatalogVersion int      `json:"catalogVersion"`
	Pages          []string `json:"pages"`
}

func (r *Repository) Navigation(ctx context.Context, actor Actor, team string) (Navigation, error) {
	a, e := r.Authority(ctx, actor, team)
	if e != nil {
		return Navigation{}, e
	}
	return Navigation{UserID: a.Principal.ID, TeamID: team, ModeEpoch: a.Principal.ModeEpoch, Revision: a.Principal.Revision, CatalogVersion: authz.CatalogVersion, Pages: a.Service.Pages(a.Principal)}, nil
}
