package build

import (
	"errors"
	"strings"
	"time"
)

func expandImageTag(tag, commit string, createdAt time.Time) (string, error) {
	bad := errors.New("invalid push_image tag variable or date format")
	if len(tag) > 4096 {
		return "", bad
	}
	var out strings.Builder
	for {
		prefix, rest, found := strings.Cut(tag, "${")
		out.WriteString(prefix)
		if !found {
			return out.String(), nil
		}
		variable, suffix, closed := strings.Cut(rest, "}")
		if !closed {
			return "", bad
		}
		switch {
		case variable == "COMMIT_SHA":
			if commit == "" || commit == "latest" {
				return "", errors.New("push_image COMMIT_SHA unavailable")
			}
			out.WriteString(commit)
		case variable == "DATE" || strings.HasPrefix(variable, "DATE:"):
			if createdAt.IsZero() {
				return "", errors.New("push_image DATE run timestamp unavailable")
			}
			format := "yyyy-MM-dd-HH-mm"
			if variable != "DATE" {
				format = strings.TrimPrefix(variable, "DATE:")
			}
			layout, err := imageDateLayout(format)
			if err != nil {
				return "", bad
			}
			// The persisted run time, not job execution time, keeps parallel tags stable.
			out.WriteString(createdAt.UTC().Format(layout))
		default:
			return "", bad
		}
		if out.Len() > 128 {
			return "", bad
		}
		tag = suffix
	}
}

func imageDateLayout(format string) (string, error) {
	bad := errors.New("invalid image date format")
	if format == "" || len(format) > 128 {
		return "", bad
	}
	var layout strings.Builder
	tokens := 0
	for format != "" {
		matched := false
		for _, token := range []struct{ name, layout string }{
			{"yyyy", "2006"}, {"MM", "01"}, {"dd", "02"},
			{"HH", "15"}, {"mm", "04"}, {"ss", "05"},
		} {
			if strings.HasPrefix(format, token.name) {
				layout.WriteString(token.layout)
				format = format[len(token.name):]
				tokens++
				matched = true
				break
			}
		}
		if matched {
			continue
		}
		if !strings.ContainsRune("-_.", rune(format[0])) {
			return "", bad
		}
		layout.WriteByte(format[0])
		format = format[1:]
	}
	if tokens == 0 {
		return "", bad
	}
	return layout.String(), nil
}
