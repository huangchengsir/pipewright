package mask

import (
	"sort"
	"strings"
	"unicode/utf8"
)

// Stream holds every possible trailing secret prefix, including one byte.
// A truncated/cancelled stream must discard the held suffix, not flush it.
type Stream struct {
	masker  *Masker
	pending string
}

func (m *Masker) NewStream() *Stream       { return &Stream{masker: m} }
func (s *Stream) Write(text string) string { s.pending += text; return s.drain(false) }
func (s *Stream) Finish() string           { return s.drain(true) }
func (s *Stream) drain(final bool) string {
	secrets := []string{}
	if s.masker != nil {
		s.masker.mu.RLock()
		for secret := range s.masker.secrets {
			secrets = append(secrets, secret)
		}
		s.masker.mu.RUnlock()
	}
	sort.Slice(secrets, func(i, j int) bool { return len(secrets[i]) > len(secrets[j]) })
	p := s.pending
	cutoff := len(p)
	coverage := make([]int, len(p)+1)
	for _, secret := range secrets {
		for start := 0; start < len(p); {
			i := strings.Index(p[start:], secret)
			if i < 0 {
				break
			}
			i += start
			coverage[i]++
			coverage[i+len(secret)]--
			start = i + 1
		}
		// Only suffixes can become a secret when another chunk arrives.
		for n := min(len(p), len(secret)-1); n > 0; n-- {
			if strings.HasSuffix(p, secret[:n]) {
				if final {
					coverage[len(p)-n]++
					coverage[len(p)]--
				} else {
					cutoff = min(cutoff, len(p)-n)
				}
				break
			}
		}
	}
	if !final {
		for cutoff > 0 && !utf8.ValidString(p[:cutoff]) {
			cutoff--
		}
	}
	// Never split a masked span across emissions.
	covered, start := 0, 0
	for i := 0; i < len(p); i++ {
		prev := covered
		covered += coverage[i]
		if prev == 0 && covered > 0 {
			start = i
		}
		if i >= cutoff && covered > 0 {
			cutoff = min(cutoff, start)
			break
		}
	}
	var out strings.Builder
	covered = 0
	for i := 0; i < cutoff; i++ {
		prev := covered
		covered += coverage[i]
		if covered > 0 {
			if prev == 0 {
				out.WriteString(Placeholder)
			}
		} else {
			out.WriteByte(p[i])
		}
	}
	s.pending = p[cutoff:]
	return out.String()
}
