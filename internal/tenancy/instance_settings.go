package tenancy

import "context"

// AuthorizeInstanceMaintenance is the authenticated adapter boundary for global
// settings operations whose runtime service does not own a database connection.
func (a *Accounts) AuthorizeInstanceMaintenance(ctx context.Context) error {
	if a == nil {
		return ErrForbidden
	}
	return RequireInstanceAdministrator(ctx, a.db)
}
