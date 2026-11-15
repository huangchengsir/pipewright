package tenancy

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"github.com/google/uuid"
	"github.com/huangchengsir/pipewright/internal/configstate"
	"time"
)

func externalIdentityKey(provider, issuer, subject string) string {
	raw, _ := json.Marshal([]string{provider, issuer, subject})
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

type bootstrapIdentity struct {
	provider, issuer, subject, label, bound string
	generation                              uint64
}

// The controller has exclusive startup admission and the mode fence here.
// Only the original administrator is mirrored; employee identities are never
// replaced, and a conflicting subject aborts the whole mode transition.
func syncBootstrapIdentities(ctx context.Context, q configstate.DBTX, userID string, toEnabled bool) error {
	var query string
	if toEnabled {
		query = `SELECT provider,issuer,subject,display_label,bound_at,identity_generation FROM login_identities`
	} else {
		query = `SELECT i.provider,i.issuer,i.subject,i.display_label,'',i.generation FROM access_identities i JOIN access_users u ON u.id=i.user_id WHERE i.user_id=?`
	}
	var args []any
	if !toEnabled {
		args = []any{userID}
	}
	rows, err := q.QueryContext(ctx, query, args...)
	if err != nil {
		return err
	}
	identities := []bootstrapIdentity{}
	providers := map[string]bool{}
	for rows.Next() {
		var item bootstrapIdentity
		if err = rows.Scan(&item.provider, &item.issuer, &item.subject, &item.label, &item.bound, &item.generation); err != nil {
			rows.Close()
			return err
		}
		if providers[item.provider] || item.generation == 0 {
			rows.Close()
			return ErrConflict
		}
		providers[item.provider] = true
		identities = append(identities, item)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	if toEnabled {
		for _, item := range identities {
			var conflicts int
			if err = q.QueryRowContext(ctx, `SELECT COUNT(*) FROM access_identities WHERE identity_key=? AND user_id<>?`, externalIdentityKey(item.provider, item.issuer, item.subject), userID).Scan(&conflicts); err != nil {
				return err
			}
			if conflicts != 0 {
				return ErrConflict
			}
		}
		if _, err = q.ExecContext(ctx, `DELETE FROM access_identities WHERE user_id=?`, userID); err != nil {
			return err
		}
		for _, item := range identities {
			if _, err = q.ExecContext(ctx, `INSERT INTO access_identities(id,user_id,provider,issuer,subject,identity_key,display_label,generation,created_at) VALUES(?,?,?,?,?,?,?,?,?)`, uuid.NewString(), userID, item.provider, item.issuer, item.subject, externalIdentityKey(item.provider, item.issuer, item.subject), item.label, item.generation, time.Now().Unix()); err != nil {
				return err
			}
		}
	} else {
		// Preserve the display label for unchanged legacy bindings. A different
		// binding must never inherit an old subject's presentation or identifier.
		for i := range identities {
			item := &identities[i]
			var label, bound string
			if q.QueryRowContext(ctx, `SELECT display_label,bound_at FROM login_identities WHERE provider=? AND issuer=? AND subject=?`, item.provider, item.issuer, item.subject).Scan(&label, &bound) == nil {
				item.label, item.bound = label, bound
			}
			if item.bound == "" {
				item.bound = time.Now().UTC().Format(time.RFC3339)
			}
		}
		if _, err = q.ExecContext(ctx, `DELETE FROM login_identities`); err != nil {
			return err
		}
		for _, item := range identities {
			if _, err = q.ExecContext(ctx, `INSERT INTO login_identities(provider,issuer,subject,display_label,identity_generation,bound_at) VALUES(?,?,?,?,?,?)`, item.provider, item.issuer, item.subject, item.label, item.generation, item.bound); err != nil {
				return err
			}
		}
	}
	return nil
}
