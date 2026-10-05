// Package onboarding computes a local, read-only first-success snapshot.
package onboarding

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"github.com/huangchengsir/pipewright/internal/pipeline"
	"github.com/huangchengsir/pipewright/internal/run"
)

const (
	Known            = "known"
	Unknown          = "unknown"
	RuntimeStub      = "stub"
	RuntimeAvailable = "available"
	RuntimeUnknown   = "unknown"
)

// Capabilities describes the executor actually selected at startup. Available
// means a non-stub executor is installed, not that Docker/SSH connectivity passed.
type Capabilities struct {
	Runtime            string
	Legacy             bool
	RepositoryOverride bool
	VaultConfigured    bool
}

type ProjectSummary struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}
type SelectedProject struct {
	ProjectSummary
	PACEnabled bool `json:"pacEnabled"`
}
type RunSummary struct {
	ID            string `json:"id"`
	ProjectID     string `json:"projectId"`
	ProjectName   string `json:"projectName"`
	Status        string `json:"status"`
	ExecutionMode string `json:"executionMode"`
	CreatedAt     string `json:"createdAt"`
	ProjectExists bool   `json:"projectExists"`
}
type Issue struct {
	Code  string `json:"code"`
	Scope string `json:"scope"`
}
type PipelineState struct {
	State   string     `json:"state"`
	SavedAt *time.Time `json:"savedAt"`
	Issues  []Issue    `json:"issues"`
}
type Snapshot struct {
	ProjectsState   string           `json:"projectsState"`
	ProjectCount    *int             `json:"projectCount"`
	Projects        []ProjectSummary `json:"projects"`
	RunsState       string           `json:"runsState"`
	RunCount        *int             `json:"runCount"`
	SelectedProject *SelectedProject `json:"selectedProject"`
	SuccessState    string           `json:"successState"`
	Success         *RunSummary      `json:"success"`
	LatestRun       *RunSummary      `json:"latestRun"`
	Pipeline        PipelineState    `json:"pipeline"`
	Runtime         string           `json:"runtime"`
}

type Service struct {
	db           *sql.DB
	capabilities Capabilities
}

func New(db *sql.DB, capabilities Capabilities) *Service {
	if capabilities.Runtime != RuntimeStub && capabilities.Runtime != RuntimeAvailable {
		capabilities.Runtime = RuntimeUnknown
	}
	return &Service{db: db, capabilities: capabilities}
}

type projectRecord struct {
	ProjectSummary
	branch, credentialID string
	pac                  bool
}

// Status deliberately queries success independently of configuration and counts.
// No raw errors, credentials, variables, trigger parameters, or logs leave this package.
func (s *Service) Status(ctx context.Context, preferred string) Snapshot {
	out := Snapshot{ProjectsState: Unknown, RunsState: Unknown, SuccessState: Unknown,
		Projects: []ProjectSummary{}, Pipeline: PipelineState{State: Unknown, Issues: []Issue{}}, Runtime: s.capabilities.Runtime}
	if s.db == nil {
		return out
	}
	if success, err := s.readRun(ctx, `WHERE status = ? AND execution_mode IN (?, ?)`, run.StatusSuccess, run.ExecutionReal, run.ExecutionLegacyUnknown); err == nil {
		out.SuccessState, out.Success = Known, success
	}
	if count, err := s.count(ctx, `SELECT COUNT(*) FROM projects`); err == nil {
		out.ProjectCount, out.ProjectsState = count, Known
	}
	if count, err := s.count(ctx, `SELECT COUNT(*) FROM pipeline_runs`); err == nil {
		out.RunCount, out.RunsState = count, Known
	}
	projects, err := s.projects(ctx)
	if err != nil {
		out.ProjectsState = Unknown
		return out
	}
	for _, p := range projects {
		out.Projects = append(out.Projects, p.ProjectSummary)
	}
	if len(projects) == 0 {
		out.Pipeline.State = "absent"
		return out
	}
	selected := -1
	for i, p := range projects {
		if p.ID == strings.TrimSpace(preferred) {
			selected = i
			break
		}
	}
	if selected < 0 {
		var latestProject string
		err := s.db.QueryRowContext(ctx, `SELECT r.project_id FROM pipeline_runs r JOIN projects p ON p.id = r.project_id ORDER BY r.created_at DESC, r.id DESC LIMIT 1`).Scan(&latestProject)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			out.RunsState = Unknown
		}
		for i, p := range projects {
			if p.ID == latestProject {
				selected = i
				break
			}
		}
		if selected < 0 {
			selected = 0
		}
	}
	p := projects[selected]
	out.SelectedProject = &SelectedProject{ProjectSummary: p.ProjectSummary, PACEnabled: p.pac || s.capabilities.RepositoryOverride}
	if latest, err := s.readRun(ctx, `WHERE project_id = ?`, p.ID); err == nil {
		out.LatestRun = latest
	} else {
		out.RunsState = Unknown
	}
	if out.SelectedProject.PACEnabled {
		out.Pipeline.State = "repository"
		if !s.capabilities.Legacy && s.capabilities.Runtime != RuntimeStub {
			var serverID string
			err := s.db.QueryRowContext(ctx, `SELECT runner_server_id FROM project_runners WHERE project_id = ?`, p.ID).Scan(&serverID)
			if strings.TrimSpace(serverID) != "" || (err != nil && !errors.Is(err, sql.ErrNoRows)) {
				out.Runtime = RuntimeUnknown
			}
		}
		return out
	}
	cfg, err := pipeline.LoadExisting(ctx, s.db, p.ID)
	if errors.Is(err, sql.ErrNoRows) {
		out.Pipeline.State = "absent"
		return out
	}
	if err != nil {
		return out
	}
	out.Pipeline.SavedAt = cfg.SavedAt
	s.prepare(ctx, p, cfg, &out)
	return out
}

func (s *Service) count(ctx context.Context, query string) (*int, error) {
	var count int
	err := s.db.QueryRowContext(ctx, query).Scan(&count)
	if err != nil {
		return nil, err
	}
	return &count, nil
}

func (s *Service) projects(ctx context.Context) ([]projectRecord, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, name, default_branch, credential_id, pac_enabled FROM projects ORDER BY created_at DESC, id DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var projects []projectRecord
	for rows.Next() {
		var p projectRecord
		if err := rows.Scan(&p.ID, &p.Name, &p.branch, &p.credentialID, &p.pac); err != nil {
			return nil, err
		}
		projects = append(projects, p)
	}
	return projects, rows.Err()
}

func (s *Service) readRun(ctx context.Context, where string, args ...any) (*RunSummary, error) {
	var r RunSummary
	err := s.db.QueryRowContext(ctx, `SELECT id, project_id, status, execution_mode, created_at FROM pipeline_runs `+where+` ORDER BY created_at DESC, id DESC LIMIT 1`, args...).Scan(&r.ID, &r.ProjectID, &r.Status, &r.ExecutionMode, &r.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	// A failed optional name lookup cannot invalidate authoritative run evidence.
	if err := s.db.QueryRowContext(ctx, `SELECT name FROM projects WHERE id = ?`, r.ProjectID).Scan(&r.ProjectName); err == nil {
		r.ProjectExists = true
	}
	return &r, nil
}
