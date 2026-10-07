package opschat

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"github.com/huangchengsir/pipewright/internal/vault"
	"io"
	"strings"
	"time"
	"unicode/utf8"
)

func digest(v any) string {
	b, _ := json.Marshal(v)
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}
func (s *Service) seal(v any) ([]byte, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return nil, ErrInvalid
	}
	out, err := s.vault.SealSecret(b)
	clear(b)
	if err != nil {
		if errors.Is(err, vault.ErrVaultUnconfigured) {
			return nil, ErrUnavailable
		}
		return nil, ErrDecrypt
	}
	return out, nil
}
func (s *Service) open(b []byte, v any) error {
	p, err := s.vault.OpenSecret(b)
	if err != nil {
		return ErrDecrypt
	}
	defer clear(p)
	if json.Unmarshal(p, v) != nil {
		return ErrDecrypt
	}
	return nil
}
func strict(b []byte, v any) error {
	if len(b) == 0 {
		b = []byte("{}")
	}
	if len(b) > 8<<10 || !bytes.HasPrefix(bytes.TrimSpace(b), []byte("{")) {
		return ErrInvalid
	}
	// Duplicate keys are rejected as well as unknown fields.
	var obj map[string]json.RawMessage
	d := json.NewDecoder(bytes.NewReader(b))
	tok, err := d.Token()
	if err != nil || tok != json.Delim('{') {
		return ErrInvalid
	}
	seen := map[string]bool{}
	for d.More() {
		t, e := d.Token()
		if e != nil {
			return ErrInvalid
		}
		k, ok := t.(string)
		if !ok || seen[k] {
			return ErrInvalid
		}
		seen[k] = true
		var val json.RawMessage
		if d.Decode(&val) != nil || bytes.Equal(bytes.TrimSpace(val), []byte("null")) {
			return ErrInvalid
		}
	}
	if _, err = d.Token(); err != nil {
		return ErrInvalid
	}
	if json.Unmarshal(b, &obj) != nil {
		return ErrInvalid
	}
	d = json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	if d.Decode(v) != nil {
		return ErrInvalid
	}
	if d.Decode(new(any)) != io.EOF {
		return ErrInvalid
	}
	return nil
}
func clip(text string, n int) string {
	text = strings.ToValidUTF8(text, "")
	if len(text) <= n {
		return text
	}
	text = text[:n]
	for !utf8.ValidString(text) {
		text = text[:len(text)-1]
	}
	return text
}
func (s *Service) scrub(text string, n int) string { return clip(s.masker.ScrubTruncated(text), n) }

// Never call this with an open DB transaction: vault.List/Reveal use the same SQLite connection.
func (s *Service) registerSecrets() error {
	if registrar, ok := s.model.(SecretRegistrar); ok {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		err := registrar.RegisterSecrets(ctx)
		expired := ctx.Err() != nil
		cancel()
		if err != nil || expired {
			return ErrUnavailable
		}
	}
	creds, err := s.vault.List()
	if err != nil {
		return ErrUnavailable
	}
	for _, c := range creds {
		val, e := s.vault.Reveal(c.ID)
		if e != nil {
			return ErrDecrypt
		}
		s.masker.RegisterSecret(val)
	}
	return nil
}
