package promotion

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/google/uuid"
	"github.com/huangchengsir/pipewright/internal/authz"
	"github.com/huangchengsir/pipewright/internal/configstate"
	"github.com/huangchengsir/pipewright/internal/store"
	"github.com/huangchengsir/pipewright/internal/tenancy"
	"strings"
	"time"
)

func (s *Store) GetChain(ctx context.Context, projectID string) (Chain, error) {
	projectID = strings.TrimSpace(projectID)
	if err := tenancy.Check(ctx, s.db, "pipeline.read", "project", projectID); err != nil {
		return Chain{}, err
	}
	chain, err := s.getChain(ctx, projectID)
	if err != nil {
		return Chain{}, err
	}
	if err = tenancy.Check(ctx, s.db, "pipeline.read", "project", projectID); err != nil {
		return Chain{}, err
	}
	return chain, nil
}
func (s *Store) ListVariables(ctx context.Context, projectID, env string) ([]Variable, error) {
	projectID = strings.TrimSpace(projectID)
	env = strings.TrimSpace(env)
	if err := tenancy.Check(ctx, s.db, "pipeline.read", "project", projectID); err != nil {
		return nil, err
	}
	vars, err := s.listVariables(ctx, projectID, env)
	if err != nil {
		return nil, err
	}
	if err = tenancy.Check(ctx, s.db, "pipeline.read", "project", projectID); err != nil {
		return nil, err
	}
	return vars, nil
}
func (s *Store) editConfiguration(ctx context.Context, projectID string, fn func(context.Context, configstate.DBTX) error) error {
	if err := tenancy.Check(ctx, s.db, "pipeline.edit", "project", projectID); err != nil {
		return err
	}
	err := configstate.MutateProject(configstate.WithWriteAdmission(ctx, tenancy.LockAuthorization), s.db, projectID, func(c context.Context, q configstate.DBTX) error {
		if err := tenancy.LockAuthorization(c, s.db); err != nil {
			return err
		}
		if err := tenancy.Check(c, s.db, "pipeline.edit", "project", projectID); err != nil {
			return err
		}
		return fn(c, q)
	})
	if errors.Is(err, configstate.ErrNotFound) || isForeignKeyErr(err) {
		return ErrProjectNotFound
	}
	return err
}
func (s *Store) SaveChain(ctx context.Context, projectID string, chain Chain) (Chain, error) {
	return s.SaveConfiguration(ctx, projectID, chain, nil)
}

// SaveConfiguration writes the complete settings request atomically and advances
// its shared configuration revision once. A denied variable binding cannot leave
// a new chain or an earlier environment behind.
func (s *Store) SaveConfiguration(ctx context.Context, projectID string, chain Chain, variables map[string][]Variable) (Chain, error) {
	projectID = strings.TrimSpace(projectID)
	var valid Chain
	err := s.editConfiguration(ctx, projectID, func(c context.Context, q configstate.DBTX) error {
		var err error
		valid, err = validateChain(chain)
		if err != nil {
			return err
		}
		normalized := map[string][]Variable{}
		for env, vars := range variables {
			// Keep the established contract: unreferenced environment inputs are ignored.
			if valid.IndexOf(env) < 0 {
				continue
			}
			normalized[env], err = normalizeVariables(vars)
			if err != nil {
				return err
			}
			if err = s.authorizeVariables(c, normalized[env]); err != nil {
				return err
			}
		}
		for _, env := range valid.Environments {
			if err = tenancy.RegisterEnvironment(c, s.db, projectID, env.Name); err != nil {
				return err
			}
		}
		raw, err := json.Marshal(valid.Environments)
		if err != nil {
			return err
		}
		_, err = q.ExecContext(c, store.InsertSQL(store.DialectOf(s.db), "project_environments", []string{"project_id", "chain_json", "updated_at"}, []string{"?", "?", "?"}, []string{"project_id"}, []string{"chain_json", "updated_at"}), projectID, string(raw), time.Now().UTC().Format(time.RFC3339))
		if err != nil {
			return err
		}
		for env, vars := range normalized {
			if err = s.replaceVariables(c, q, projectID, env, vars); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return Chain{}, err
	}
	return valid, nil
}
func normalizeVariables(input []Variable) ([]Variable, error) {
	out := make([]Variable, 0, len(input))
	seen := map[string]bool{}
	for _, v := range input {
		v.Key = strings.TrimSpace(v.Key)
		if v.Key == "" {
			return nil, ErrVarKeyEmpty
		}
		if seen[v.Key] {
			return nil, ErrVarKeyDuplicate
		}
		seen[v.Key] = true
		if v.Secret {
			v.Value = ""
			v.CredentialID = strings.TrimSpace(v.CredentialID)
		} else {
			v.CredentialID = ""
		}
		out = append(out, v)
	}
	return out, nil
}
func (s *Store) authorizeVariables(ctx context.Context, vars []Variable) error {
	for _, v := range vars {
		if v.Secret {
			if err := tenancy.CheckSameTeam(ctx, s.db, authz.ResourceRef{Kind: "credential", ID: v.CredentialID}); err != nil {
				return err
			}
			if err := tenancy.Check(ctx, s.db, "credential.use", "credential", v.CredentialID); err != nil {
				return err
			}
		}
	}
	return nil
}
func (s *Store) SetVariables(ctx context.Context, projectID, env string, variables []Variable) error {
	projectID = strings.TrimSpace(projectID)
	env = strings.TrimSpace(env)
	return s.editConfiguration(ctx, projectID, func(c context.Context, q configstate.DBTX) error {
		if env == "" {
			return ErrInvalidEnvName
		}
		vars, err := normalizeVariables(variables)
		if err != nil {
			return err
		}
		if err = s.authorizeVariables(c, vars); err != nil {
			return err
		}
		if err = tenancy.RegisterEnvironment(c, s.db, projectID, env); err != nil {
			return err
		}
		return s.replaceVariables(c, q, projectID, env, vars)
	})
}
func (s *Store) replaceVariables(ctx context.Context, q configstate.DBTX, projectID, env string, vars []Variable) error {
	if _, err := q.ExecContext(ctx, `DELETE FROM environment_variables WHERE project_id=? AND environment=?`, projectID, env); err != nil {
		return err
	}
	now := time.Now().UTC().Format(time.RFC3339)
	for _, v := range vars {
		secret := 0
		if v.Secret {
			secret = 1
		}
		if _, err := q.ExecContext(ctx, `INSERT INTO environment_variables(id,project_id,environment,var_key,value,is_secret,credential_id,updated_at) VALUES(?,?,?,?,?,?,?,?)`, uuid.NewString(), projectID, env, v.Key, v.Value, secret, v.CredentialID, now); err != nil {
			return err
		}
	}
	return nil
}
