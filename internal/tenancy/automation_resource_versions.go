package tenancy

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"github.com/huangchengsir/pipewright/internal/authz"
	"github.com/huangchengsir/pipewright/internal/configstate"
	"strings"
)

func automationResourceVersion(ctx context.Context, q configstate.DBTX, kind, id string) (string, error) {
	table := map[string]string{"server": "servers", "credential": "credentials", "kubernetes": "k8s_connections", "kubernetes_credential": "k8s_credentials", "dns_provider": "dns_providers", "notification_channel": "notification_channels"}[kind]
	if table == "" {
		return "", nil
	}
	rows, err := q.QueryContext(ctx, "SELECT * FROM "+table+" WHERE id=?", id)
	if err != nil {
		return "", err
	}
	defer rows.Close()
	cols, err := rows.Columns()
	if err != nil {
		return "", err
	}
	if !rows.Next() {
		return "", ErrForbidden
	}
	values := make([]sql.NullString, len(cols))
	dest := make([]any, len(cols))
	for i := range values {
		dest[i] = &values[i]
	}
	if err = rows.Scan(dest...); err != nil {
		return "", err
	}
	body := map[string]*string{}
	for i, col := range cols {
		col = strings.ToLower(col)
		if col == "created_at" || col == "updated_at" || col == "last_used_at" {
			continue
		}
		if values[i].Valid {
			v := hex.EncodeToString([]byte(values[i].String))
			body[col] = &v
		} else {
			body[col] = nil
		}
	}
	if rows.Next() {
		return "", ErrForbidden
	}
	if err = rows.Err(); err != nil {
		return "", err
	}
	encoded, err := json.Marshal(body)
	if err != nil || len(encoded) > 4<<20 {
		return "", ErrInvalidInput
	}
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:]), nil
}
func captureAutomationResourceVersions(ctx context.Context, q configstate.DBTX, permissions []AutomationPermission) (map[string]string, error) {
	versions := map[string]string{}
	for _, p := range permissions {
		definition, ok := authz.Lookup(p.Action)
		if !ok {
			return nil, ErrForbidden
		}
		if p.ResourceID == "" {
			continue
		}
		key := definition.ResourceKind + "/" + p.ResourceID
		if _, exists := versions[key]; exists {
			continue
		}
		version, err := automationResourceVersion(ctx, q, definition.ResourceKind, p.ResourceID)
		if err != nil {
			return nil, err
		}
		if version != "" {
			versions[key] = version
		}
	}
	return versions, nil
}
func verifyAutomationResourceVersions(ctx context.Context, q configstate.DBTX, versions map[string]string) error {
	if len(versions) > 512 {
		return ErrForbidden
	}
	for key, version := range versions {
		kind, id, ok := strings.Cut(key, "/")
		if !ok || version == "" {
			return ErrForbidden
		}
		current, err := automationResourceVersion(ctx, q, kind, id)
		if err != nil || current != version {
			return ErrForbidden
		}
	}
	return nil
}
