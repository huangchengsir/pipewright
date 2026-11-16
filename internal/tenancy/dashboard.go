package tenancy

import (
	"context"
	"database/sql"
	"github.com/huangchengsir/pipewright/internal/authz"
)

type dashboardScopeKey struct{}

// WithDashboardScope only adds restrictions to the authenticated request.
// Navigation eligibility never substitutes for the resource predicates below.
func WithDashboardScope(ctx context.Context, db *sql.DB) (context.Context, error) {
	enabled, err := Enabled(ctx, db)
	if err != nil || !enabled {
		return ctx, err
	}
	authority, _, err := authorityForRequest(ctx, db)
	if err != nil {
		return nil, err
	}
	if !authority.Service.HasAction(authority.Principal, "dashboard.read") {
		return nil, ErrForbidden
	}
	return context.WithValue(ctx, dashboardScopeKey{}, true), nil
}

// QueryPredicate filters at the database, before counts, pagination or aggregation.
// Dashboard requests retain every module's own action and intersect its project
// scope with dashboard.read. Neither role can lend a broader scope to the other.
func QueryPredicate(ctx context.Context, db *sql.DB, action, column string) (authz.Predicate, error) {
	predicate, err := baseQueryPredicate(ctx, db, action, column)
	if err != nil {
		return predicate, err
	}
	restricted, _ := ctx.Value(dashboardScopeKey{}).(bool)
	if !restricted || action == "dashboard.read" {
		return predicate, nil
	}
	authority, _, err := authorityForRequest(ctx, db)
	if err != nil {
		return authz.Predicate{}, err
	}
	if !authority.Service.HasAction(authority.Principal, "dashboard.read") {
		return authz.Predicate{}, ErrForbidden
	}
	definition, known := authz.Lookup(action)
	if !known || !definition.ProjectScoped {
		return predicate, nil
	}
	projectColumn := column
	if definition.ResourceKind != "project" {
		projectColumn = "azdash.project_id"
	}
	dashboard, err := baseQueryPredicate(ctx, db, "dashboard.read", projectColumn)
	if err != nil {
		return authz.Predicate{}, err
	}
	if definition.ResourceKind == "project" {
		predicate.SQL = "(" + predicate.SQL + ") AND (" + dashboard.SQL + ")"
		predicate.Args = append(predicate.Args, dashboard.Args...)
	} else {
		predicate.SQL = "(" + predicate.SQL + ") AND EXISTS (SELECT 1 FROM access_ownership azdash WHERE azdash.resource_kind=? AND azdash.resource_id=" + column + " AND (" + dashboard.SQL + "))"
		predicate.Args = append(predicate.Args, definition.ResourceKind)
		predicate.Args = append(predicate.Args, dashboard.Args...)
	}
	if len(predicate.Args) > 1800 {
		return authz.Predicate{}, authz.ErrInvalidPolicy
	}
	return predicate, nil
}
