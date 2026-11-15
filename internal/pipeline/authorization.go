package pipeline

import (
	"context"
	"database/sql"
	"encoding/json"
	"github.com/huangchengsir/pipewright/internal/tenancy"
	"strings"
)

// References are permissions to bind a configured resource, not permission to
// execute a plan. Dispatch must separately check the immutable execution plan.
func authorizeConfigReferences(ctx context.Context, db *sql.DB, value any) error {
	enabled, err := tenancy.Enabled(ctx, db)
	if err != nil || !enabled {
		return err
	}
	seen := map[string]bool{}
	check := func(action, kind, id string) error {
		id = strings.TrimSpace(id)
		if id == "" {
			return nil
		}
		key := kind + ":" + id
		if seen[key] {
			return nil
		}
		seen[key] = true
		return tenancy.Check(ctx, db, action, kind, id)
	}
	var walk func(any) error
	walk = func(value any) error {
		switch item := value.(type) {
		case map[string]any:
			for key, v := range item {
				if text, ok := v.(string); ok {
					switch key {
					case "credentialId", "registryCredentialId", "gitCredentialId":
						kind, action := "credential", "credential.use"
						if strings.HasPrefix(text, "kcred_") {
							kind, action = "kubernetes_credential", "kubernetes_credential.use"
						}
						if err := check(action, kind, text); err != nil {
							return err
						}
					case "serverId", "runnerServerId":
						if err := check("server.use", "server", text); err != nil {
							return err
						}
					case "connectionId":
						if err := check("kubernetes.use", "kubernetes", text); err != nil {
							return err
						}
					case "executor":
						var nested any
						if json.Unmarshal([]byte(text), &nested) == nil {
							if err := walk(nested); err != nil {
								return err
							}
						}
					}
				}
				if key == "targetServerIds" || key == "serverIds" {
					if ids, ok := v.([]any); ok {
						for _, id := range ids {
							if text, ok := id.(string); ok {
								if err := check("server.use", "server", text); err != nil {
									return err
								}
							}
						}
					}
				}
				if err := walk(v); err != nil {
					return err
				}
			}
		case []any:
			for _, child := range item {
				if err := walk(child); err != nil {
					return err
				}
			}
		}
		return nil
	}
	// Convert typed request structs and nested string maps to one JSON shape.
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	var normalized any
	if err = json.Unmarshal(data, &normalized); err != nil {
		return err
	}
	return walk(normalized)
}

// AuthorizeReusableReferences validates resource bindings in reusable definitions
// using the same policy as saved project configuration. Execution is separate.
func AuthorizeReusableReferences(ctx context.Context, db *sql.DB, value any) error {
	return authorizeConfigReferences(ctx, db, value)
}

// AuthorizeAction checks an auxiliary configuration operation before it starts
// parsing, contacting a repository or invoking a model. Unknown adapters fail closed.
func AuthorizeAction(ctx context.Context, svc Service, projectID, action string) error {
	authorizer, ok := svc.(interface {
		AuthorizeAction(context.Context, string, string) error
	})
	if !ok {
		return tenancy.ErrForbidden
	}
	return authorizer.AuthorizeAction(ctx, projectID, action)
}
func (s *service) AuthorizeAction(ctx context.Context, projectID, action string) error {
	if action != "pipeline.read" && action != "pipeline.edit" {
		return tenancy.ErrForbidden
	}
	return tenancy.Check(ctx, s.db, action, "project", projectID)
}
