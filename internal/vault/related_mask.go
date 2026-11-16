package vault

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"github.com/huangchengsir/pipewright/internal/configstate"
	secretmask "github.com/huangchengsir/pipewright/internal/mask"
	"github.com/huangchengsir/pipewright/internal/tenancy"
	"strings"
)

// RelatedMaskRegistrar never returns secret values. Read-only project/run
// viewers can receive scrubbed output without acquiring use or reveal rights.
type RelatedMaskRegistrar interface {
	RegisterRelatedMaskSecrets(context.Context, string, string, *secretmask.Masker) (bool, error)
}

func RegisterRelatedMaskSecrets(ctx context.Context, v Vault, kind, id string, m *secretmask.Masker) (bool, error) {
	if reader, ok := v.(RelatedMaskRegistrar); ok {
		return reader.RegisterRelatedMaskSecrets(ctx, kind, id, m)
	}
	return false, nil
}
func (s *service) RegisterRelatedMaskSecrets(ctx context.Context, kind, id string, m *secretmask.Masker) (bool, error) {
	enabled, err := tenancy.Enabled(ctx, s.db)
	if err != nil {
		return true, err
	}
	if !enabled {
		return false, nil
	}
	if m == nil || !s.configured() {
		return true, ErrVaultUnconfigured
	}
	action := "project.read"
	if kind == "run" {
		action = "run.read"
	} else if kind != "project" {
		return true, tenancy.ErrForbidden
	}
	err = configstate.WithReadTxContext(ctx, s.db, func(txctx context.Context, q configstate.DBTX) error {
		if err := tenancy.Check(txctx, s.db, action, kind, id); err != nil {
			return err
		}
		projectID := id
		if kind == "run" {
			if err := q.QueryRowContext(txctx, `SELECT project_id FROM pipeline_runs WHERE id=?`, id).Scan(&projectID); err != nil {
				return err
			}
		}
		var team, git string
		if err := q.QueryRowContext(txctx, `SELECT o.tenant_id,p.credential_id FROM projects p JOIN access_ownership o ON o.resource_kind='project' AND o.resource_id=p.id WHERE p.id=?`, projectID).Scan(&team, &git); err != nil {
			return err
		}
		ids := map[string]bool{}
		if git != "" {
			ids[git] = true
		}
		var walk func(any)
		walk = func(value any) {
			switch v := value.(type) {
			case map[string]any:
				for k, x := range v {
					key := strings.ToLower(k)
					if key == "credentialid" || key == "registrycredentialid" || key == "gitcredentialid" {
						if id, ok := x.(string); ok && id != "" && !strings.HasPrefix(id, "kcred_") {
							ids[id] = true
						}
					}
					walk(x)
				}
			case []any:
				for _, x := range v {
					walk(x)
				}
			}
		}
		decode := func(text string) error {
			var value any
			if err := json.Unmarshal([]byte(text), &value); err != nil {
				return ErrDecrypt
			}
			walk(value)
			return nil
		}
		var spec string
		err := q.QueryRowContext(txctx, `SELECT spec_json FROM pipeline_configs WHERE project_id=?`, projectID).Scan(&spec)
		if err == nil {
			if err = decode(spec); err != nil {
				return err
			}
		} else if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		var build, environments, steps string
		err = q.QueryRowContext(txctx, `SELECT build_json,environments_json,steps_json FROM pipeline_settings WHERE project_id=?`, projectID).Scan(&build, &environments, &steps)
		if err == nil {
			for _, text := range []string{build, environments, steps} {
				if err = decode(text); err != nil {
					return err
				}
			}
		} else if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		// Captured capabilities preserve runtime references after configuration edits.
		if kind == "run" {
			var body string
			err = q.QueryRowContext(txctx, `SELECT permissions_json FROM access_run_authorizations WHERE run_id=?`, id).Scan(&body)
			if err == nil {
				permissions, decodeErr := tenancy.DecodeExecutionPermissions(body)
				if decodeErr != nil {
					return ErrDecrypt
				}
				for _, p := range permissions {
					if p.Action == "credential.use" {
						ids[p.ResourceID] = true
					}
				}
			} else if !errors.Is(err, sql.ErrNoRows) {
				return err
			}
		}
		if len(ids) > 256 {
			return errors.New("vault: related secret registration limit reached")
		}
		for credential := range ids {
			var typ string
			var sealed []byte
			err = q.QueryRowContext(txctx, `SELECT c.type,c.ciphertext FROM credentials c JOIN access_ownership o ON o.resource_kind='credential' AND o.resource_id=c.id WHERE c.id=? AND o.tenant_id=?`, credential, team).Scan(&typ, &sealed)
			if err != nil {
				return err
			}
			if len(sealed) > 65536 {
				return errors.New("vault: related secret too large")
			}
			plain, err := open(s.key, sealed)
			if err != nil {
				return err
			}
			m.RegisterExactSecret(string(plain))
			if typ == TypeRegistry {
				m.RegisterRegistrySecret(string(plain))
			}
			clear(plain)
		}
		return nil
	})
	return true, err
}
