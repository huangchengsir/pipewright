package workspace

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/huangchengsir/pipewright/internal/configstate"
	"github.com/huangchengsir/pipewright/internal/environments"
	"github.com/huangchengsir/pipewright/internal/project"
	"github.com/huangchengsir/pipewright/internal/store"
	"github.com/huangchengsir/pipewright/internal/tenancy"
)

var (
	ErrInvalid  = errors.New("workspace: invalid input")
	ErrNotFound = errors.New("workspace: resource not found")
	ErrConflict = errors.New("workspace: resource changed")
	ErrNotEmpty = errors.New("workspace: container is not empty")
)

type BusinessProject struct {
	ID       string    `json:"id"`
	Name     string    `json:"name"`
	Revision int64     `json:"revision"`
	Products []Product `json:"products"`
}

type Product struct {
	ID                string `json:"id"`
	BusinessProjectID string `json:"businessProjectId"`
	Name              string `json:"name"`
	Revision          int64  `json:"revision"`
}

type Directory struct {
	BusinessProjects []BusinessProject `json:"businessProjects"`
}

type Service struct {
	db           *sql.DB
	projects     project.Service
	environments *environments.Service
}

func New(db *sql.DB, projects project.Service) *Service {
	return &Service{db: db, projects: projects, environments: environments.NewService(db)}
}

// WithEnvironments shares the runtime's deployment timelines, including its
// Kubernetes history readers. Set during startup.
func (s *Service) WithEnvironments(service *environments.Service) *Service {
	if service != nil {
		s.environments = service
	}
	return s
}

func (s *Service) List(ctx context.Context) (Directory, error) {
	out := Directory{BusinessProjects: []BusinessProject{}}
	err := configstate.WithReadTxContext(ctx, s.db, func(ctx context.Context, q configstate.DBTX) error {
		bp, err := tenancy.QueryPredicate(ctx, s.db, "business_project.read", "b.id")
		if err != nil {
			return err
		}
		pp, err := tenancy.QueryPredicate(ctx, s.db, "product.read", "p.id")
		if err != nil {
			return err
		}
		rows, err := q.QueryContext(ctx, `SELECT b.id,b.name,b.revision,p.id,p.name,p.revision
			FROM workspace_business_projects b LEFT JOIN workspace_products p
			ON p.business_project_id=b.id AND (`+pp.SQL+`) WHERE `+bp.SQL+`
			ORDER BY b.name,b.id,p.name,p.id`, append(pp.Args, bp.Args...)...)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			b := BusinessProject{Products: []Product{}}
			var productID, productName sql.NullString
			var productRevision sql.NullInt64
			if err := rows.Scan(&b.ID, &b.Name, &b.Revision, &productID, &productName, &productRevision); err != nil {
				return err
			}
			i := len(out.BusinessProjects) - 1
			if i < 0 || out.BusinessProjects[i].ID != b.ID {
				out.BusinessProjects = append(out.BusinessProjects, b)
				i++
			}
			if productID.Valid {
				out.BusinessProjects[i].Products = append(out.BusinessProjects[i].Products, Product{
					ID: productID.String, BusinessProjectID: b.ID, Name: productName.String, Revision: productRevision.Int64,
				})
			}
		}
		return rows.Err()
	})
	return out, err
}

func (s *Service) GetProduct(ctx context.Context, id string) (Product, error) {
	return s.getProduct(ctx, id, "product.read")
}
func (s *Service) getProduct(ctx context.Context, id, action string) (Product, error) {
	var p Product
	bp, err := tenancy.QueryPredicate(ctx, s.db, "business_project.read", "b.id")
	if err != nil {
		return p, err
	}
	pp, err := tenancy.QueryPredicate(ctx, s.db, action, "p.id")
	if err != nil {
		return p, err
	}
	err = configstate.Queryer(ctx, s.db).QueryRowContext(ctx,
		`SELECT p.id,p.business_project_id,p.name,p.revision FROM workspace_products p
		JOIN workspace_business_projects b ON b.id=p.business_project_id
		WHERE p.id=? AND (`+bp.SQL+`) AND (`+pp.SQL+`)`,
		append(append([]any{id}, bp.Args...), pp.Args...)...,
	).Scan(&p.ID, &p.BusinessProjectID, &p.Name, &p.Revision)
	if errors.Is(err, sql.ErrNoRows) {
		return p, ErrNotFound
	}
	return p, err
}

func (s *Service) CreateBusinessProject(ctx context.Context, name string) (BusinessProject, error) {
	out := BusinessProject{ID: uuid.NewString(), Name: strings.TrimSpace(name), Revision: 1, Products: []Product{}}
	if !validName(out.Name) {
		return BusinessProject{}, ErrInvalid
	}
	err := s.write(ctx, func(ctx context.Context, q configstate.DBTX) error {
		if err := tenancy.CheckNew(ctx, s.db, "business_project.create", "business_project", out.ID); err != nil {
			return err
		}
		if _, err := q.ExecContext(ctx, `INSERT INTO workspace_business_projects(id,name,revision) VALUES(?,?,1)`, out.ID, out.Name); err != nil {
			return err
		}
		return tenancy.RecordOwnership(ctx, s.db, "business_project", out.ID, "")
	})
	return out, err
}

func (s *Service) CreateProduct(ctx context.Context, parentID, name string) (Product, error) {
	out := Product{ID: uuid.NewString(), BusinessProjectID: parentID, Name: strings.TrimSpace(name), Revision: 1}
	if parentID == "" || !validName(out.Name) {
		return Product{}, ErrInvalid
	}
	err := s.write(ctx, func(ctx context.Context, q configstate.DBTX) error {
		if err := tenancy.Check(ctx, s.db, "business_project.manage", "business_project", parentID); err != nil {
			return err
		}
		if err := tenancy.CheckNew(ctx, s.db, "product.create", "product", out.ID); err != nil {
			return err
		}
		if _, err := q.ExecContext(ctx, `INSERT INTO workspace_products(id,business_project_id,name,revision) VALUES(?,?,?,1)`, out.ID, parentID, out.Name); err != nil {
			if store.IsForeignKeyErr(err) {
				return ErrNotFound
			}
			return err
		}
		if err := tenancy.RecordOwnership(ctx, s.db, "product", out.ID, ""); err != nil {
			return err
		}
		return sameTeam(ctx, q, "business_project", parentID, "product", out.ID)
	})
	return out, err
}

func (s *Service) RenameBusinessProject(ctx context.Context, id, name string, revision int64) (BusinessProject, error) {
	out := BusinessProject{ID: id, Name: strings.TrimSpace(name), Revision: revision + 1, Products: []Product{}}
	if id == "" || revision < 1 || !validName(out.Name) {
		return BusinessProject{}, ErrInvalid
	}
	err := s.write(ctx, func(ctx context.Context, q configstate.DBTX) error {
		if err := tenancy.Check(ctx, s.db, "business_project.manage", "business_project", id); err != nil {
			return err
		}
		return changed(q.ExecContext(ctx, `UPDATE workspace_business_projects SET name=?, revision=revision+1 WHERE id=? AND revision=?`, out.Name, id, revision))
	})
	return out, err
}

func (s *Service) RenameProduct(ctx context.Context, id, name string, revision int64) (Product, error) {
	name = strings.TrimSpace(name)
	if id == "" || revision < 1 || !validName(name) {
		return Product{}, ErrInvalid
	}
	var out Product
	err := s.write(ctx, func(ctx context.Context, q configstate.DBTX) error {
		var err error
		out, err = s.getProduct(ctx, id, "product.manage")
		if err != nil {
			return err
		}
		if err = changed(q.ExecContext(ctx, `UPDATE workspace_products SET name=?, revision=revision+1 WHERE id=? AND revision=?`, name, id, revision)); err != nil {
			return err
		}
		out.Name = name
		out.Revision = revision + 1
		return nil
	})
	return out, err
}

func (s *Service) DeleteBusinessProject(ctx context.Context, id string, revision int64) error {
	if id == "" || revision < 1 {
		return ErrInvalid
	}
	return s.write(ctx, func(ctx context.Context, q configstate.DBTX) error {
		if err := tenancy.Check(ctx, s.db, "business_project.manage", "business_project", id); err != nil {
			return err
		}
		return deleteContainer(ctx, q, "business_project", id, revision,
			`SELECT revision FROM workspace_business_projects WHERE id=?`,
			`SELECT COUNT(*) FROM workspace_products WHERE business_project_id=?`,
			`DELETE FROM workspace_business_projects WHERE id=? AND revision=?`)
	})
}

func (s *Service) DeleteProduct(ctx context.Context, id string, revision int64) error {
	if id == "" || revision < 1 {
		return ErrInvalid
	}
	return s.write(ctx, func(ctx context.Context, q configstate.DBTX) error {
		if _, err := s.getProduct(ctx, id, "product.manage"); err != nil {
			return err
		}
		return deleteContainer(ctx, q, "product", id, revision,
			`SELECT revision FROM workspace_products WHERE id=?`,
			`SELECT COUNT(*) FROM workspace_applications WHERE product_id=?`,
			`DELETE FROM workspace_products WHERE id=? AND revision=?`)
	})
}

func (s *Service) write(ctx context.Context, fn func(context.Context, configstate.DBTX) error) error {
	return configstate.WithWriteTxContext(ctx, s.db, func(ctx context.Context, q configstate.DBTX) error {
		if err := configstate.CheckMaintenanceAdmission(ctx, q); err != nil {
			return err
		}
		if err := tenancy.LockAuthorization(ctx, s.db); err != nil {
			return err
		}
		return fn(ctx, q)
	})
}
func validName(name string) bool {
	return name != "" && utf8.ValidString(name) && utf8.RuneCountInString(name) <= 128
}
func changed(result sql.Result, err error) error {
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err == nil && count != 1 {
		return ErrConflict
	}
	return err
}

func deleteContainer(ctx context.Context, q configstate.DBTX, kind, id string, revision int64, revisionSQL, countSQL, deleteSQL string) error {
	var actual int64
	if err := q.QueryRowContext(ctx, revisionSQL, id).Scan(&actual); errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	} else if err != nil {
		return err
	}
	if revision != actual {
		return ErrConflict
	}
	var children int
	if err := q.QueryRowContext(ctx, countSQL, id).Scan(&children); err != nil {
		return err
	}
	if children != 0 {
		return ErrNotEmpty
	}
	// The restrictive FK also protects against concurrent attachments.
	if err := changed(q.ExecContext(ctx, deleteSQL, id, revision)); store.IsForeignKeyErr(err) {
		return ErrNotEmpty
	} else if err != nil {
		return err
	}
	_, err := q.ExecContext(ctx, `DELETE FROM access_ownership WHERE resource_kind=? AND resource_id=?`, kind, id)
	return err
}

func sameTeam(ctx context.Context, q configstate.DBTX, leftKind, leftID, rightKind, rightID string) error {
	const ownership = `SELECT tenant_id FROM access_ownership WHERE resource_kind=? AND resource_id=?`
	var left, right string
	if err := q.QueryRowContext(ctx, ownership, leftKind, leftID).Scan(&left); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if err := q.QueryRowContext(ctx, ownership, rightKind, rightID).Scan(&right); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if left != right {
		return tenancy.ErrForbidden
	}
	return nil
}
