package workspace

import (
	"context"
	"database/sql"
	"errors"
	"strings"

	"github.com/huangchengsir/pipewright/internal/authz"
	"github.com/huangchengsir/pipewright/internal/configstate"
	"github.com/huangchengsir/pipewright/internal/environments"
	"github.com/huangchengsir/pipewright/internal/project"
	"github.com/huangchengsir/pipewright/internal/store"
	"github.com/huangchengsir/pipewright/internal/tenancy"
)

type ApplicationFilter struct {
	BusinessProjectID string
	ApplicationID     string
	ProductID         string
	Unassigned        bool
	Query             string
	Status            string
	Page              int
	PageSize          int
	// IdentityOnly skips display-only run and deployment lookups for batch plans.
	IdentityOnly bool
}

type Application struct {
	Project project.Project
	Assignment
	LatestRun            *LatestRun
	ProductionDeployment *ProductionDeployment
}

type Assignment struct {
	ApplicationID     string `json:"applicationId"`
	ProductID         string `json:"productId,omitempty"`
	BusinessProjectID string `json:"businessProjectId,omitempty"`
	Revision          int64  `json:"assignmentRevision"`
	PreviousProductID string `json:"-"`
}

type ApplicationList struct {
	Items    []Application
	Total    int
	Page     int
	PageSize int
}

type LatestRun struct {
	RunID     string `json:"runId"`
	Status    string `json:"status"`
	Commit    string `json:"commit"`
	CreatedAt string `json:"createdAt"`
}
type ProductionDeployment struct {
	RunID       string `json:"runId"`
	Environment string `json:"environment"`
	Commit      string `json:"commit"`
	DeployedAt  string `json:"deployedAt"`
}

type projectListing interface {
	ListFiltered(context.Context, int, int, authz.Predicate) (*project.ListResult, error)
}
type projectAssociation interface {
	CreateWithAssociation(context.Context, project.CreateInput, func(context.Context, configstate.DBTX, string) error) (*project.Project, error)
}

func (s *Service) assignmentFilter(ctx context.Context) (string, []any, error) {
	bp, err := tenancy.QueryPredicate(ctx, s.db, "business_project.read", "wb.id")
	if err != nil {
		return "", nil, err
	}
	pp, err := tenancy.QueryPredicate(ctx, s.db, "product.read", "wp.id")
	if err != nil {
		return "", nil, err
	}
	return ` AND (` + bp.SQL + `) AND (` + pp.SQL + `)`, append(bp.Args, pp.Args...), nil
}

const assignmentJoin = `workspace_applications wa JOIN workspace_products wp ON wp.id=wa.product_id
	JOIN workspace_business_projects wb ON wb.id=wp.business_project_id`

func (s *Service) ListApplications(ctx context.Context, in ApplicationFilter) (ApplicationList, error) {
	out := ApplicationList{Items: []Application{}}
	list, ok := s.projects.(projectListing)
	if !ok {
		return out, errors.New("workspace: application listing unavailable")
	}
	if (in.Unassigned && (in.BusinessProjectID != "" || in.ProductID != "")) ||
		len(in.Query) > 512 || len(in.Status) > 64 {
		return out, ErrInvalid
	}
	err := configstate.WithReadTxContext(ctx, s.db, func(ctx context.Context, q configstate.DBTX) error {
		if in.ProductID != "" {
			p, err := s.GetProduct(ctx, in.ProductID)
			if err != nil {
				return err
			}
			if in.BusinessProjectID != "" && in.BusinessProjectID != p.BusinessProjectID {
				return ErrNotFound
			}
		}
		assignmentSQL, assignmentArgs, err := s.assignmentFilter(ctx)
		if err != nil {
			return err
		}
		clauses := []string{"1=1"}
		args := []any{}
		if in.ApplicationID != "" {
			clauses = append(clauses, "p.id=?")
			args = append(args, in.ApplicationID)
		}
		switch {
		case in.Unassigned:
			clauses = append(clauses, `NOT EXISTS (SELECT 1 FROM workspace_applications wa WHERE wa.application_id=p.id AND wa.product_id IS NOT NULL)`)
		case in.ProductID != "":
			clauses = append(clauses, `EXISTS (SELECT 1 FROM `+assignmentJoin+` WHERE wa.application_id=p.id AND wp.id=?`+assignmentSQL+`)`)
			args = append(append(args, in.ProductID), assignmentArgs...)
		case in.BusinessProjectID != "":
			clauses = append(clauses, `EXISTS (SELECT 1 FROM `+assignmentJoin+` WHERE wa.application_id=p.id AND wb.id=?`+assignmentSQL+`)`)
			args = append(append(args, in.BusinessProjectID), assignmentArgs...)
		}
		if search := strings.TrimSpace(in.Query); search != "" {
			like := "%" + strings.NewReplacer("!", "!!", "%", "!%", "_", "!_").Replace(strings.ToLower(search)) + "%"
			clauses = append(clauses, `(LOWER(p.name) LIKE ? ESCAPE '!' OR LOWER(p.repo_url) LIKE ? ESCAPE '!' OR LOWER(p.default_branch) LIKE ? ESCAPE '!')`)
			args = append(args, like, like, like)
		}
		runs, err := tenancy.QueryPredicate(ctx, s.db, "run.read", "r.id")
		if err != nil {
			return err
		}
		order := ` ORDER BY r.created_at DESC,r.id DESC` + store.Limit(store.DialectOf(s.db), "1")
		if in.Status != "" {
			states := applicationStatusFilter(in.Status)
			placeholders := strings.TrimSuffix(strings.Repeat("?,", len(states)), ",")
			clauses = append(clauses, `COALESCE((SELECT r.status FROM pipeline_runs r WHERE r.project_id=p.id AND (`+runs.SQL+`)`+order+`),'none') IN (`+placeholders+`)`)
			args = append(args, runs.Args...)
			for _, state := range states {
				args = append(args, state)
			}
		}
		result, err := list.ListFiltered(ctx, in.Page, in.PageSize, authz.Predicate{SQL: strings.Join(clauses, " AND "), Args: args})
		if err != nil {
			return err
		}
		out.Total, out.Page, out.PageSize = result.Total, result.Page, result.PageSize
		for _, p := range result.Items {
			a := Application{Project: p, Assignment: Assignment{ApplicationID: p.ID}}
			err = q.QueryRowContext(ctx, `SELECT wp.id,wp.business_project_id,wa.revision FROM `+assignmentJoin+` WHERE wa.application_id=?`+assignmentSQL, append([]any{p.ID}, assignmentArgs...)...).Scan(&a.ProductID, &a.BusinessProjectID, &a.Revision)
			if errors.Is(err, sql.ErrNoRows) {
				err = q.QueryRowContext(ctx, `SELECT revision FROM workspace_applications WHERE application_id=? AND product_id IS NULL`, p.ID).Scan(&a.Revision)
			}
			if err != nil && !errors.Is(err, sql.ErrNoRows) {
				return err
			}
			if !in.IdentityOnly {
				a.LatestRun, err = s.latestRun(ctx, q, p.ID, runs, order)
				if err != nil {
					return err
				}
			}
			out.Items = append(out.Items, a)
		}
		return nil
	})
	if err != nil || in.IdentityOnly {
		return out, err
	}
	// The Kubernetes history reader may open its own read transaction. Use the
	// same request authority but no enclosing transaction for environment views.
	for i := range out.Items {
		out.Items[i].ProductionDeployment, err = s.productionDeployment(ctx, out.Items[i].Project.ID)
		if err != nil {
			return ApplicationList{}, err
		}
	}
	return out, nil
}

func (s *Service) latestRun(ctx context.Context, q configstate.DBTX, id string, scope authz.Predicate, order string) (*LatestRun, error) {
	var x LatestRun
	err := q.QueryRowContext(ctx, `SELECT r.id,r.status,
		COALESCE(NULLIF(r.resolved_commit_sha,''),r.trigger_commit,''),r.created_at
		FROM pipeline_runs r WHERE r.project_id=? AND (`+scope.SQL+`)`+order, append([]any{id}, scope.Args...)...).Scan(&x.RunID, &x.Status, &x.Commit, &x.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return &x, err
}

func (s *Service) productionDeployment(ctx context.Context, app string) (*ProductionDeployment, error) {
	timelines, err := s.environments.ListEnvironments(ctx, app, 1)
	if errors.Is(err, environments.ErrProjectNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	runs, err := tenancy.QueryPredicate(ctx, s.db, "run.read", "r.id")
	if err != nil {
		return nil, err
	}
	var production *ProductionDeployment
	for _, line := range timelines {
		if line.Active == nil {
			continue
		}
		var productionEnvironment bool
		if line.EnvironmentID == "" {
			// Legacy single-administrator installations do not store purposes.
			name := strings.ToLower(strings.TrimSpace(line.Environment))
			productionEnvironment = name == "prod" || name == "production"
		} else {
			var purpose string
			err = configstate.Queryer(ctx, s.db).QueryRowContext(ctx, `SELECT purpose FROM access_environments WHERE id=?`, line.EnvironmentID).Scan(&purpose)
			if err != nil {
				return nil, err
			}
			productionEnvironment = purpose == "production"
		}
		if !productionEnvironment || (production != nil && production.DeployedAt >= line.Active.DeployedAt) {
			continue
		}
		var actualCommit string
		err = configstate.Queryer(ctx, s.db).QueryRowContext(ctx,
			`SELECT CASE WHEN r.resolved_commit_conflict=0 THEN r.resolved_commit_sha ELSE '' END
			FROM pipeline_runs r WHERE r.id=? AND (`+runs.SQL+`)`,
			append([]any{line.Active.RunID}, runs.Args...)...).Scan(&actualCommit)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		} else if err != nil {
			return nil, err
		}
		production = &ProductionDeployment{RunID: line.Active.RunID, Environment: line.Environment, Commit: actualCommit, DeployedAt: line.Active.DeployedAt}
	}
	return production, nil
}

func (s *Service) CreateApplication(ctx context.Context, productID string, in project.CreateInput) (Application, error) {
	creator, ok := s.projects.(projectAssociation)
	if !ok {
		return Application{}, errors.New("workspace: application creation unavailable")
	}
	if _, err := s.getProduct(ctx, productID, "product.manage"); err != nil {
		return Application{}, err
	}
	var assignment Assignment
	app, err := creator.CreateWithAssociation(ctx, in, func(ctx context.Context, q configstate.DBTX, id string) error {
		p, err := s.getProduct(ctx, productID, "product.manage")
		if err != nil {
			return err
		}
		if err = sameTeam(ctx, q, "project", id, "product", p.ID); err != nil {
			return err
		}
		_, err = q.ExecContext(ctx, `INSERT INTO workspace_applications(application_id,product_id,revision) VALUES(?,?,1)`, id, p.ID)
		if store.IsForeignKeyErr(err) {
			return ErrConflict
		}
		assignment = Assignment{ApplicationID: id, ProductID: p.ID, BusinessProjectID: p.BusinessProjectID, Revision: 1}
		return err
	})
	if err != nil {
		return Application{}, err
	}
	return Application{Project: *app, Assignment: assignment}, nil
}

func (s *Service) AssignApplication(ctx context.Context, appID, newProductID string, expected int64) (Assignment, error) {
	var out Assignment
	if appID == "" || expected < 0 {
		return out, ErrInvalid
	}
	err := s.write(ctx, func(ctx context.Context, q configstate.DBTX) error {
		if err := tenancy.Check(ctx, s.db, "project.edit", "project", appID); err != nil {
			return err
		}
		if _, err := s.projects.Get(ctx, appID); err != nil {
			return err
		}
		if _, err := configstate.LockProject(ctx, q, s.db, appID); err != nil {
			return err
		}
		var existing sql.NullString
		err := q.QueryRowContext(ctx, `SELECT product_id,revision FROM workspace_applications WHERE application_id=?`, appID).Scan(&existing, &out.Revision)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if out.Revision != expected {
			return ErrConflict
		}
		if existing.Valid {
			if _, err = s.getProduct(ctx, existing.String, "product.manage"); err != nil {
				return err
			}
			if err = sameTeam(ctx, q, "project", appID, "product", existing.String); err != nil {
				return err
			}
		}
		out.ApplicationID, out.ProductID, out.PreviousProductID = appID, newProductID, existing.String
		var product any
		if newProductID != "" {
			p, err := s.getProduct(ctx, newProductID, "product.manage")
			if err != nil {
				return err
			}
			if err = sameTeam(ctx, q, "project", appID, "product", p.ID); err != nil {
				return err
			}
			product = p.ID
			out.BusinessProjectID = p.BusinessProjectID
		}
		if newProductID == existing.String {
			return nil
		}
		if expected == 0 {
			_, err = q.ExecContext(ctx, `INSERT INTO workspace_applications(application_id,product_id,revision) VALUES(?,?,1)`, appID, product)
			if store.IsUniqueErr(err) || store.IsForeignKeyErr(err) {
				return ErrConflict
			}
		} else {
			err = changed(q.ExecContext(ctx, `UPDATE workspace_applications SET product_id=?,revision=revision+1 WHERE application_id=? AND revision=?`, product, appID, expected))
			if store.IsForeignKeyErr(err) {
				return ErrConflict
			}
		}
		out.Revision = expected + 1
		return err
	})
	return out, err
}

func applicationStatusFilter(status string) []string {
	switch status {
	case "成功":
		return []string{"success"}
	case "失败":
		return []string{"failed"}
	case "进行中", "running":
		return []string{"running", "waiting_approval", "waiting_action"}
	case "部分失败":
		return []string{"partial_failed"}
	case "已回滚":
		return []string{"rolled_back"}
	case "排队中":
		return []string{"queued"}
	default:
		return []string{status}
	}
}
