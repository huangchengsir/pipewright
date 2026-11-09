// Package logging localizes Pipewright-owned process logs through the standard
// logger. It does not translate pipeline output, protocol messages or errors.
package logging

import (
	"fmt"
	stdlog "log"
	"os"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/huangchengsir/pipewright/internal/i18n"
)

// Package initialization captures the process environment once, before auth or
// worker initialization. A later request/header/environment change cannot alter
// the language of a running process or another user's logs.
var processLocale = i18n.LogLocaleFromEnv(os.Getenv)

// Printf preserves the standard logger's output, prefix, timestamp and caller
// configuration. Catalog lookup happens before formatting diagnostic values.
func Printf(format string, args ...any) {
	_ = stdlog.Output(2, message(format, args...))
}

// Fatalf matches log.Fatalf: write through the standard logger and exit with 1.
func Fatalf(format string, args ...any) {
	_ = stdlog.Output(2, message(format, args...))
	os.Exit(1)
}

func message(format string, args ...any) string {
	return singleLine(fmt.Sprintf(i18n.LogMessage(processLocale, format), args...))
}

// Preserve opaque values without allowing them to forge another process-log
// line or terminal escape sequence. Existing module prefixes remain untouched.
func singleLine(value string) string {
	var out strings.Builder
	for index := 0; index < len(value); {
		char, size := utf8.DecodeRuneInString(value[index:])
		if char == utf8.RuneError && size == 1 {
			fmt.Fprintf(&out, `\x%02x`, value[index])
			index++
			continue
		}
		index += size
		switch char {
		case '\n':
			out.WriteString(`\n`)
		case '\r':
			out.WriteString(`\r`)
		case '\t':
			out.WriteString(`\t`)
		case '\u2028':
			out.WriteString(`\u2028`)
		case '\u2029':
			out.WriteString(`\u2029`)
		default:
			if unicode.IsControl(char) || unicode.Is(unicode.Bidi_Control, char) {
				fmt.Fprintf(&out, `\u%04x`, char)
			} else {
				out.WriteRune(char)
			}
		}
	}
	return out.String()
}
