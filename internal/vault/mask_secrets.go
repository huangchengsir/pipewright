package vault

import (
	"context"
	"errors"

	secretmask "github.com/huangchengsir/pipewright/internal/mask"
)

// RegisterMaskSecrets performs only bounded, cancellable local reads. It never marks use.
func RegisterMaskSecrets(ctx context.Context, v Vault, masker *secretmask.Masker) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	reader, ok := v.(interface {
		registerMaskSecrets(context.Context, *secretmask.Masker) error
	})
	if !ok || masker == nil {
		return errors.New("vault: bounded secret registration unavailable")
	}
	return reader.registerMaskSecrets(ctx, masker)
}

func (s *service) registerMaskSecrets(ctx context.Context, masker *secretmask.Masker) error {
	if !s.configured() {
		return ErrVaultUnconfigured
	}
	rows, err := s.db.QueryContext(ctx, `SELECT type, CASE WHEN LENGTH(ciphertext) <= 65536 THEN ciphertext ELSE NULL END FROM credentials ORDER BY id LIMIT 257`)
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
		var sealed []byte
		if err := rows.Scan(&kind, &sealed); err != nil {
			return err
		}
		if len(sealed) > 64<<10 {
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
	}
	return rows.Err()
}
