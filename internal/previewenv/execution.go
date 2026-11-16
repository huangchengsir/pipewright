package previewenv

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"github.com/huangchengsir/pipewright/internal/configstate"
	"github.com/huangchengsir/pipewright/internal/tenancy"
	"regexp"
	"strconv"
	"strings"
)

var prPrefixRe = regexp.MustCompile(`^(?:pr|pull|mr|merge-requests?)[-/](\d+)$`)
var allDigitRe = regexp.MustCompile(`^\d+$`)

// PRNumber is shared by admission and the terminal hook.
func PRNumber(branch string) int {
	b := strings.ToLower(strings.TrimSpace(branch))
	if m := prPrefixRe.FindStringSubmatch(b); m != nil {
		n, _ := strconv.Atoi(m[1])
		if n > 0 {
			return n
		}
	}
	if allDigitRe.MatchString(b) {
		n, _ := strconv.Atoi(b)
		if n > 0 {
			return n
		}
	}
	return 0
}

type ExecutionInputs struct {
	Enabled                                              bool
	ProviderID, CredentialID, Version, ExistingPreviewID string
}

// ExecutionVersion fingerprints the fixed preview configuration and DNS secret
// generation without exposing the ciphertext. Disabled configuration is also
// pinned, so a queued plan cannot acquire newly enabled external side effects.
func ExecutionVersion(ctx context.Context, db *sql.DB, project string) (string, error) {
	_, version, e := executionInputs(ctx, db, project)
	return version, e
}
func executionInputs(ctx context.Context, db *sql.DB, project string) (ExecutionInputs, string, error) {
	var out ExecutionInputs
	if e := tenancy.Check(ctx, db, "pipeline.read", "project", project); e != nil {
		return out, "", e
	}
	q := configstate.Queryer(ctx, db)
	var enabled int
	var domain string
	e := q.QueryRowContext(ctx, `SELECT enabled,dns_provider_id,base_domain FROM preview_configs WHERE project_id=?`, project).Scan(&enabled, &out.ProviderID, &domain)
	if e != nil && !errors.Is(e, sql.ErrNoRows) {
		return out, "", e
	}
	if enabled != 0 && enabled != 1 {
		return out, "", tenancy.ErrForbidden
	}
	out.Enabled = enabled == 1
	data := []any{project, enabled, out.ProviderID, domain}
	if out.Enabled {
		var providerType, providerDomain string
		var ciphertext []byte
		if e = q.QueryRowContext(ctx, `SELECT p.type,p.base_domain,p.credential_id,c.ciphertext FROM dns_providers p JOIN credentials c ON c.id=p.credential_id WHERE p.id=?`, out.ProviderID).Scan(&providerType, &providerDomain, &out.CredentialID, &ciphertext); e != nil {
			return out, "", e
		}
		secretDigest := sha256.Sum256(ciphertext)
		data = append(data, providerType, providerDomain, out.CredentialID, hex.EncodeToString(secretDigest[:]))
	}
	body, _ := json.Marshal(data)
	sum := sha256.Sum256(body)
	out.Version = hex.EncodeToString(sum[:])
	return out, out.Version, nil
}
func PlanInputs(ctx context.Context, db *sql.DB, project string, pr int) (ExecutionInputs, error) {
	out, _, e := executionInputs(ctx, db, project)
	if e != nil || !out.Enabled {
		return out, e
	}
	if e = tenancy.Check(ctx, db, "preview.provision", "project", project); e != nil {
		return out, e
	}
	if e = New(db).authorizeProvider(ctx, project, out.ProviderID); e != nil {
		return out, e
	}
	var id string
	e = configstate.Queryer(ctx, db).QueryRowContext(ctx, `SELECT id FROM preview_envs WHERE project_id=? AND pr_number=?`, project, pr).Scan(&id)
	if errors.Is(e, sql.ErrNoRows) {
		return out, nil
	}
	if e != nil {
		return out, e
	}
	if e = tenancy.Check(ctx, db, "preview.reclaim", "preview", id); e != nil {
		return out, e
	}
	out.ExistingPreviewID = id
	return out, nil
}
