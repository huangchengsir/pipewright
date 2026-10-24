package mask

import (
	"context"
	"errors"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

const terminalFieldBytes = 16 << 10
const terminalSecretScanBytes = 4 << 20
const terminalSecretScanCount = 1024

var ErrTerminalScanUnavailable = errors.New("mask: terminal redaction unavailable")

// ScrubTerminalFragments first replaces complete known secrets while preserving
// useful surrounding evidence. Any remaining exact eight-byte secret fragment
// masks the entire field, covering suffixes and internal slices conservatively.
// Short, unknown or transformed fragments can still evade detection.
//
// Scrub and ScrubTruncated retain their existing semantics. This terminal-only
// path has field, registry-count, registry-byte and time budgets. It copies only
// bounded string references for length ordering, never secret bytes or indexes.
// The fragment index grows only with bounded input. Overall work is
// O(registry count * input bytes + registered secret bytes), bounded by
// 1024 entries, 4 MiB of secrets, 16 KiB of input and a two-second deadline.
func (m *Masker) ScrubTerminalFragments(ctx context.Context, text string) (string, error) {
	return m.scrubTerminalFragments(ctx, text, terminalSecretScanBytes)
}
func (m *Masker) scrubTerminalFragments(ctx context.Context, text string, budget int) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if m == nil || len(text) > terminalFieldBytes || !utf8.ValidString(text) {
		return "", ErrTerminalScanUnavailable
	}
	if text == "" {
		return "", nil
	}
	if !m.mu.TryRLock() {
		return "", ErrTerminalScanUnavailable
	}
	secrets, err := m.terminalSecretRefs(ctx, budget)
	m.mu.RUnlock()
	if err != nil {
		return "", err
	}
	sort.Slice(secrets, func(i, j int) bool { return len(secrets[i]) > len(secrets[j]) })
	cleaned := text
	for _, secret := range secrets {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		if secret != "" {
			cleaned = strings.ReplaceAll(cleaned, secret, Placeholder)
		}
		if len(cleaned) > terminalFieldBytes {
			return "", ErrTerminalScanUnavailable
		}
	}
	// Retain the old trailing-prefix protection below the eight-byte fragment
	// threshold, without unbounded scans or re-entering the registry lock.
	tail := 0
	for _, secret := range secrets {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		limit := min(7, len(secret)-1, len(cleaned))
		for n := limit; n >= 4 && n > tail; n-- {
			if strings.HasSuffix(cleaned, secret[:n]) {
				tail = n
				break
			}
		}
	}
	if tail > 0 {
		cleaned = cleaned[:len(cleaned)-tail] + Placeholder
	}
	if len(cleaned) > terminalFieldBytes {
		return "", ErrTerminalScanUnavailable
	}
	if len(cleaned) < 8 {
		return cleaned, nil
	}
	windows := make(map[uint64]struct{}, len(cleaned)-7)
	var window uint64
	for i := 0; i < len(cleaned); i++ {
		if i&255 == 0 {
			if err := ctx.Err(); err != nil {
				return "", err
			}
		}
		window = window<<8 | uint64(cleaned[i])
		if i >= 7 {
			windows[window] = struct{}{}
		}
	}
	for _, secret := range secrets {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		if len(secret) < 8 {
			continue
		}
		var key uint64
		for i := 0; i < len(secret); i++ {
			if i&255 == 0 {
				if err := ctx.Err(); err != nil {
					return "", err
				}
			}
			key = key<<8 | uint64(secret[i])
			if i >= 7 {
				if _, ok := windows[key]; ok {
					return Placeholder, nil
				}
			}
		}
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	return cleaned, nil
}

// Caller holds a read lock. Only immutable string headers are copied.
func (m *Masker) terminalSecretRefs(ctx context.Context, budget int) ([]string, error) {
	if len(m.secrets) > terminalSecretScanCount {
		return nil, ErrTerminalScanUnavailable
	}
	secrets := make([]string, 0, len(m.secrets))
	total := 0
	for secret := range m.secrets {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if len(secret) > budget-total {
			return nil, ErrTerminalScanUnavailable
		}
		total += len(secret)
		secrets = append(secrets, secret)
	}
	return secrets, nil
}
