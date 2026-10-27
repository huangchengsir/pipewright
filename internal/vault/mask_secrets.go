package vault

import (
	"context"
	"errors"

	secretmask "github.com/huangchengsir/pipewright/internal/mask"
)

// MaskSecretRegistrar is optional so decorators can preserve the bounded local
// reader without expanding Vault or changing older adapters.
type MaskSecretRegistrar interface {
	RegisterMaskSecrets(context.Context, *secretmask.Masker) error
}

// RegisterMaskSecrets performs only bounded, cancellable local reads. It never marks use.
func RegisterMaskSecrets(ctx context.Context, v Vault, masker *secretmask.Masker) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	reader, ok := v.(MaskSecretRegistrar)
	if !ok || masker == nil {
		return errors.New("vault: bounded secret registration unavailable")
	}
	return reader.RegisterMaskSecrets(ctx, masker)
}

func (s *service) RegisterMaskSecrets(ctx context.Context, masker *secretmask.Masker) error {
	if !s.configured() {
		return ErrVaultUnconfigured
	}
	rows, err := s.db.QueryContext(ctx, `SELECT c.type,
	CASE WHEN LENGTH(c.ciphertext) <= 65536 THEN c.ciphertext ELSE NULL END,
	CASE WHEN LENGTH(o.refresh_ciphertext) <= 65536 THEN o.refresh_ciphertext ELSE NULL END,
	CASE WHEN LENGTH(o.refresh_ciphertext) > 65536 THEN 1 ELSE 0 END
	FROM credentials c LEFT JOIN oauth_credentials o ON o.credential_id=c.id
	ORDER BY c.id LIMIT 257`)
	if err != nil {
		return err
	}
	defer rows.Close()
	count := 0
	for rows.Next() {
		count++
		if count > 256 {
			return errors.New("vault: secret registration limit reached")
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		var kind string
		var sealed, refresh []byte
		var oversizedRefresh int
		if err := rows.Scan(&kind, &sealed, &refresh, &oversizedRefresh); err != nil {
			return err
		}
		if len(sealed) > 64<<10 || oversizedRefresh != 0 {
			return errors.New("vault: secret registration value too large")
		}
		plain, err := open(s.key, sealed)
		if err != nil {
			return err
		}
		value := string(plain)
		for i := range plain {
			plain[i] = 0
		}
		masker.RegisterSecret(value)
		if kind == TypeRegistry {
			masker.RegisterRegistrySecret(value)
		}
		if len(refresh) > 0 {
			plain, err := open(s.key, refresh)
			if err != nil {
				return err
			}
			masker.RegisterSecret(string(plain))
			for i := range plain {
				plain[i] = 0
			}
		}
	}
	return rows.Err()
}
