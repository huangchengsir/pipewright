package ai

import (
	"context"
	"errors"
	"regexp"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/huangchengsir/pipewright/internal/mask"
	"github.com/huangchengsir/pipewright/internal/opschat"
	"github.com/huangchengsir/pipewright/internal/vault"
)

// TerminalPreviewer performs bounded local-only redaction; no model or SSH request.
// Values remain separate until each truncated secret suffix has been scrubbed.
type TerminalPreviewer interface {
	PreviewTerminal(context.Context, []string) (*TerminalPreview, error)
}
type TerminalPreview struct {
	Values   []string             `json:"values"`
	Provider opschat.ProviderInfo `json:"provider"`
}

var ErrTerminalSensitive = errors.New("ai: terminal input requires redaction")
var terminalPrivateKey = regexp.MustCompile(`(?s)-----BEGIN [A-Z ]*PRIVATE KEY-----.*?(?:-----END [A-Z ]*PRIVATE KEY-----|$)`)
var terminalBearer = regexp.MustCompile(`(?i)\bBearer[ \t]+[^\s"'<>]+`)
var terminalAssignment = regexp.MustCompile(`(?im)(?:password|passwd|secret|token|api[_-]?key|authorization)[ \t]*[=:][^\r\n]*`)

// ScrubTerminal applies conservative content rules after known-secret redaction.
// It cannot recognize arbitrary unregistered passwords: users must still review.
func ScrubTerminal(ctx context.Context, m *mask.Masker, value string) (string, error) {
	value, err := m.ScrubTerminalFragments(ctx, value)
	if err != nil {
		return "", err
	}
	value = terminalPrivateKey.ReplaceAllString(value, "[MASKED PRIVATE KEY]")
	value = terminalBearer.ReplaceAllString(value, "Bearer [MASKED]")
	return terminalAssignment.ReplaceAllString(value, "[MASKED SENSITIVE ASSIGNMENT]"), nil
}
func TerminalSingleLine(value string) bool {
	return utf8.ValidString(value) && len(value) <= 1024 && !strings.ContainsFunc(value, func(r rune) bool { return unicode.IsControl(r) || r == '\u2028' || r == '\u2029' })
}
func (s *service) terminalMasker(ctx context.Context) (*mask.Masker, error) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	m := mask.NewMasker()
	if err := vault.RegisterMaskSecrets(ctx, s.vault, m); err != nil {
		return nil, err
	}
	_, sealed, err := s.load(ctx)
	if err != nil {
		return nil, err
	}
	if _, err = (&opsModel{service: s, masker: m}).register(sealed); err != nil {
		return nil, err
	}
	return m, nil
}
func (s *service) PreviewTerminal(ctx context.Context, values []string) (*TerminalPreview, error) {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	if len(values) == 0 || len(values) > 12 {
		return nil, ErrTerminalSensitive
	}
	total := 0
	for _, v := range values {
		total += len(v)
		if !utf8.ValidString(v) {
			return nil, ErrTerminalSensitive
		}
	}
	if total > 16<<10 {
		return nil, ErrTerminalSensitive
	}
	m, err := s.terminalMasker(ctx)
	if err != nil {
		return nil, err
	}
	out := &TerminalPreview{Values: make([]string, len(values))}
	for i, v := range values {
		out.Values[i], err = ScrubTerminal(ctx, m, v)
		if err != nil {
			return nil, err
		}
	}
	cfg, sealed, err := s.load(ctx)
	if err != nil {
		return nil, err
	}
	out.Provider = opsProvider(cfg, sealed)
	// Provider/model labels are user configuration too; never return embedded secrets.
	out.Provider.Provider, err = ScrubTerminal(ctx, m, out.Provider.Provider)
	if err != nil {
		return nil, err
	}
	out.Provider.Model, err = ScrubTerminal(ctx, m, out.Provider.Model)
	if err != nil {
		return nil, err
	}
	return out, nil
}
